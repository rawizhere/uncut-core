package supervisor

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	// ProbeAddr: optional loopback address for a TLS liveness probe; empty disables probing.
	ProbeAddr string
	// ProbeSNI: ServerName for the probe handshake.
	ProbeSNI string
}

type ManagedProcess struct {
	Spec    ProcessSpec
	Cmd     *exec.Cmd
	Running bool
	Started time.Time
	Backoff time.Duration
	// ProbeFails: consecutive failed liveness probes; at 3 the process is killed so the monitor restarts it.
	ProbeFails int
}

type Supervisor struct {
	mu        sync.Mutex
	processes map[string]*ManagedProcess
	ctx       context.Context
	cancel    context.CancelFunc
}

func New() *Supervisor {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Supervisor{
		processes: make(map[string]*ManagedProcess),
		ctx:       ctx,
		cancel:    cancel,
	}
	go s.watchProbes()
	return s
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
	// One live process per ManagedProcess: a blind start here spawns a duplicate that dies on a busy bind and restarts forever.
	if mp.Running || s.ctx.Err() != nil {
		return
	}
	cmd := exec.CommandContext(s.ctx, mp.Spec.Command, mp.Spec.Args...)
	// Stop is SIGTERM, not CommandContext's default SIGKILL; the grace stays under docker's own 10s kill.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	if mp.Spec.Dir != "" {
		cmd.Dir = mp.Spec.Dir
	}

	stdoutW := io.Writer(os.Stdout)
	stderrW := io.Writer(os.Stderr)

	// Opened per start, closed by the monitor after Wait: a crash loop would otherwise leak one fd per restart.
	var logFile *os.File
	if mp.Spec.LogPath != "" {
		_ = os.MkdirAll(filepath.Dir(mp.Spec.LogPath), 0o755)
		if f, err := os.OpenFile(mp.Spec.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640); err == nil {
			logFile = f
			stdoutW = io.MultiWriter(os.Stdout, f)
			stderrW = io.MultiWriter(os.Stderr, f)
		}
	}

	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	if err := cmd.Start(); err != nil {
		if logFile != nil {
			_ = logFile.Close()
		}
		slog.Error("Failed to start process", "process", name, "error", err)
		return
	}

	mp.Cmd = cmd
	mp.Running = true
	mp.ProbeFails = 0
	mp.Started = time.Now()
	slog.Info("Started managed process", "process", name, "pid", cmd.Process.Pid)

	// The monitor waits on ITS OWN cmd exactly once, then hands the restart to startProcess: looping Wait on the reaped cmd cascades duplicate starts.
	cmdLocal := cmd
	go func(n string, proc *ManagedProcess) {
		_ = cmdLocal.Wait()
		if logFile != nil {
			_ = logFile.Close()
		}
		s.mu.Lock()
		proc.Running = false
		stopped := s.ctx.Err() != nil
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

// Respec replaces a registered process's spec for its next start and reports whether it changed; the caller restarts it.
func (s *Supervisor) Respec(spec ProcessSpec) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	mp, ok := s.processes[spec.Name]
	if !ok {
		return false
	}
	cur := mp.Spec
	if cur.Command == spec.Command && slices.Equal(cur.Args, spec.Args) && cur.Dir == spec.Dir &&
		cur.LogPath == spec.LogPath && cur.ProbeAddr == spec.ProbeAddr && cur.ProbeSNI == spec.ProbeSNI {
		return false
	}
	mp.Spec = spec
	return true
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

	// Wait for the monitor goroutine to reap the old process instead of a blind sleep: starting earlier races the monitor's own restart.
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

// watchProbes kills processes that stopped serving: restart-on-exit cannot see a hung process, a TLS handshake can.
func (s *Supervisor) watchProbes() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}

		type target struct {
			name string
			mp   *ManagedProcess
		}
		s.mu.Lock()
		var targets []target
		for name, mp := range s.processes {
			if mp.Spec.ProbeAddr != "" && mp.Running && mp.Cmd != nil && mp.Cmd.Process != nil {
				targets = append(targets, target{name, mp})
			}
		}
		s.mu.Unlock()

		for _, t := range targets {
			ok := probeTLS(t.mp.Spec.ProbeAddr, t.mp.Spec.ProbeSNI)
			s.mu.Lock()
			if ok {
				t.mp.ProbeFails = 0
			} else if t.mp.Running && t.mp.ProbeFails < 3 {
				t.mp.ProbeFails++
				if t.mp.ProbeFails == 3 {
					slog.Warn("Liveness probe failed repeatedly, killing process", "process", t.name, "addr", t.mp.Spec.ProbeAddr)
					_ = t.mp.Cmd.Process.Kill()
				}
			}
			s.mu.Unlock()
		}
	}
}

func probeTLS(addr, sni string) bool {
	d := net.Dialer{Timeout: 3 * time.Second}
	conn, err := tls.DialWithDialer(&d, "tcp", addr, &tls.Config{ServerName: sni, InsecureSkipVerify: true})
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (s *Supervisor) GetStatus() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	status := make(map[string]bool)
	for name, mp := range s.processes {
		status[name] = mp.Running && mp.ProbeFails < 3
	}
	return status
}

// StopAll cancels the context, which SIGTERMs every child through its Cancel, and waits for them to exit.
func (s *Supervisor) StopAll() {
	s.cancel()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		running := false
		for _, mp := range s.processes {
			running = running || mp.Running
		}
		s.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
