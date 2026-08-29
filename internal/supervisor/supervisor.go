package supervisor

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type ProcessSpec struct {
	Name    string
	Command string
	Args    []string
	Dir     string
	LogPath string
}

type ManagedProcess struct {
	Spec    ProcessSpec
	Cmd     *exec.Cmd
	Running bool
}

type Supervisor struct {
	mu        sync.Mutex
	processes map[string]*ManagedProcess
	ctx       context.Context
	cancel    context.CancelFunc
}

func New() *Supervisor {
	ctx, cancel := context.WithCancel(context.Background())
	return &Supervisor{
		processes: make(map[string]*ManagedProcess),
		ctx:       ctx,
		cancel:    cancel,
	}
}

func (s *Supervisor) AddProcess(spec ProcessSpec) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.processes[spec.Name] = &ManagedProcess{
		Spec: spec,
	}
}

func (s *Supervisor) StartAll() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for name, mp := range s.processes {
		if mp.Running {
			continue
		}
		s.startProcess(name, mp)
	}
}

func (s *Supervisor) startProcess(name string, mp *ManagedProcess) {
	cmd := exec.CommandContext(s.ctx, mp.Spec.Command, mp.Spec.Args...)
	if mp.Spec.Dir != "" {
		cmd.Dir = mp.Spec.Dir
	}

	stdoutW := io.Writer(os.Stdout)
	stderrW := io.Writer(os.Stderr)

	if mp.Spec.LogPath != "" {
		_ = os.MkdirAll(filepath.Dir(mp.Spec.LogPath), 0o755)
		if f, err := os.OpenFile(mp.Spec.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640); err == nil {
			stdoutW = io.MultiWriter(os.Stdout, f)
			stderrW = io.MultiWriter(os.Stderr, f)
		}
	}

	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	if err := cmd.Start(); err != nil {
		slog.Error("Failed to start process", "process", name, "error", err)
		return
	}

	mp.Cmd = cmd
	mp.Running = true
	slog.Info("Started managed process", "process", name, "pid", cmd.Process.Pid)

	go func(n string, proc *ManagedProcess) {
		backoff := 1 * time.Second
		for {
			_ = proc.Cmd.Wait()
			s.mu.Lock()
			proc.Running = false
			stopped := s.ctx.Err() != nil
			s.mu.Unlock()

			if stopped {
				slog.Info("Managed process stopped", "process", n)
				return
			}

			slog.Warn("Managed process crashed, restarting...", "process", n, "backoff", backoff)
			select {
			case <-s.ctx.Done():
				return
			case <-time.After(backoff):
			}

			s.mu.Lock()
			if s.ctx.Err() != nil {
				s.mu.Unlock()
				return
			}
			s.startProcess(n, proc)
			s.mu.Unlock()

			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}(name, mp)
}

func (s *Supervisor) ReloadSingBox() error {
	s.mu.Lock()
	mp, ok := s.processes["sing-box"]
	s.mu.Unlock()

	if !ok || !mp.Running || mp.Cmd == nil || mp.Cmd.Process == nil {
		return fmt.Errorf("sing-box is not running")
	}

	slog.Info("Reloading sing-box configuration via SIGHUP", "pid", mp.Cmd.Process.Pid)
	return mp.Cmd.Process.Signal(syscall.SIGHUP)
}

func (s *Supervisor) RestartProcess(name string) error {
	s.mu.Lock()
	mp, ok := s.processes[name]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("process %s not registered", name)
	}

	if mp.Running && mp.Cmd != nil && mp.Cmd.Process != nil {
		slog.Info("Restarting process", "process", name, "pid", mp.Cmd.Process.Pid)
		_ = mp.Cmd.Process.Signal(syscall.SIGTERM)
	}
	s.mu.Unlock()

	time.Sleep(300 * time.Millisecond)

	s.mu.Lock()
	defer s.mu.Unlock()
	if !mp.Running {
		s.startProcess(name, mp)
	}
	return nil
}

func (s *Supervisor) ReloadNginx() error {
	slog.Info("Reloading nginx configuration")
	cmd := exec.Command("nginx", "-s", "reload")
	return cmd.Run()
}

func (s *Supervisor) GetStatus() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	status := make(map[string]bool)
	for name, mp := range s.processes {
		status[name] = mp.Running
	}
	return status
}

func (s *Supervisor) StopAll() {
	s.cancel()
	s.mu.Lock()
	defer s.mu.Unlock()

	for name, mp := range s.processes {
		if mp.Running && mp.Cmd != nil && mp.Cmd.Process != nil {
			slog.Info("Stopping process gracefully", "process", name, "pid", mp.Cmd.Process.Pid)
			_ = mp.Cmd.Process.Signal(syscall.SIGTERM)
		}
	}

	time.Sleep(500 * time.Millisecond)
}
