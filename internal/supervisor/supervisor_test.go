package supervisor

import (
	"testing"
	"time"
)

func TestSupervisorStartStop(t *testing.T) {
	sup := New()
	sup.AddProcess(ProcessSpec{
		Name:    "sleep",
		Command: "sleep",
		Args:    []string{"10"},
	})

	sup.StartAll()

	sup.mu.Lock()
	proc, ok := sup.processes["sleep"]
	sup.mu.Unlock()

	if !ok || !proc.Running {
		t.Fatal("process sleep should be running")
	}

	sup.StopAll()

	time.Sleep(100 * time.Millisecond)
	sup.mu.Lock()
	running := proc.Running
	sup.mu.Unlock()

	if running {
		t.Fatal("process sleep should be stopped")
	}
}

func TestSupervisorRestart(t *testing.T) {
	sup := New()
	sup.AddProcess(ProcessSpec{
		Name:    "sleep",
		Command: "sleep",
		Args:    []string{"10"},
	})

	sup.StartAll()
	if err := sup.RestartProcess("sleep"); err != nil {
		t.Fatalf("RestartProcess failed: %v", err)
	}

	sup.mu.Lock()
	proc := sup.processes["sleep"]
	running := proc.Running
	sup.mu.Unlock()

	if !running {
		t.Fatal("process sleep should be running after restart")
	}

	sup.StopAll()
}
