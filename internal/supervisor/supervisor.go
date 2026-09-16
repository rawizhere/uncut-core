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
	// Stopped marks an operator-requested stop: the monitor reaps but does
	// not restart, so a disabled process stays down.
	Stopped bool
	Started time.Time
	Backoff time.Duration
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
	// One live process per ManagedProcess: a blind start here spawns a
	// duplicate that dies on a busy bind and restarts forever.
	if mp.Running {
		return
	}
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
	mp.Started = time.Now()
	slog.Info("Started managed process", "process", name, "pid", cmd.Process.Pid)

	// The monitor waits on ITS OWN cmd exactly once, then hands the restart to
	// startProcess: looping Wait on the reaped cmd cascades duplicate starts.
	cmdLocal := cmd
	go func(n string, proc *ManagedProcess) {
		_ = cmdLocal.Wait()
		s.mu.Lock()
		proc.Running = false
		stopped := s.ctx.Err() != nil || proc.Stopped
		if time.Since(proc.Started) > 30*time.Second {
			proc.Backoff = 0
		}
		proc.Backoff *= 2
		if proc.Backoff > 30*time.Second {
			proc.Backoff = 30 * time.Second
		}
		backoff := proc.Backoff
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
		defer s.mu.Unlock()
		if s.ctx.Err() != nil {
			return
		}
		s.startProcess(n, proc)
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

// ReplaceSpec swaps the args of the named process in place (keeping its live
// state), or registers a new one; pair it with RestartProcess to apply.
func (s *Supervisor) ReplaceSpec(name string, spec ProcessSpec) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if mp, ok := s.processes[name]; ok {
		mp.Spec = spec
		return
	}
	s.processes[spec.Name] = &ManagedProcess{Spec: spec}
}

// StopProcess stops the named process and keeps it down until StartProcess.
func (s *Supervisor) StopProcess(name string) error {
	s.mu.Lock()
	mp, ok := s.processes[name]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("process %s not registered", name)
	}
	if mp.Running && mp.Cmd != nil && mp.Cmd.Process != nil {
		mp.Stopped = true
		slog.Info("Stopping process", "process", name, "pid", mp.Cmd.Process.Pid)
		_ = mp.Cmd.Process.Signal(syscall.SIGTERM)
	} else {
		mp.Stopped = true
	}
	s.mu.Unlock()
	return nil
}

func (s *Supervisor) RestartProcess(name string) error {
	s.mu.Lock()
	s.processes[name].Stopped = false
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

	// Wait for the monitor goroutine to reap the old process instead of a
	// blind sleep: starting earlier races the monitor's own restart.
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.mu.Lock()
		running := mp.Running
		s.mu.Unlock()
		if !running || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if mp.Running {
		return fmt.Errorf("process %s did not exit", name)
	}
	s.startProcess(name, mp)
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
