package orchestrator

import (
	"context"
	"testing"

	"claudworker/internal/assignment"
)

// TestCancelTask — cancelling a non-running task errors; cancelling a running one invokes its cancel
// func (which kills the worker CLI + stops the pipeline).
func TestCancelTask(t *testing.T) {
	o := New(&Orchestrator{Store: assignment.NewMemoryStore()}, WithClock(steady()))

	if err := o.CancelTask("X-1"); err == nil {
		t.Fatal("cancelling a task that isn't running should error")
	}
	if o.TaskRunning("X-1") {
		t.Fatal("X-1 should not be running")
	}

	// Simulate an active run by registering a cancel func.
	called := false
	o.mu.Lock()
	if o.cancels == nil {
		o.cancels = map[string]context.CancelFunc{}
	}
	o.cancels["X-1"] = func() { called = true }
	o.mu.Unlock()

	if !o.TaskRunning("X-1") {
		t.Fatal("X-1 should be running after registering a cancel")
	}
	if err := o.CancelTask("X-1"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if !called {
		t.Fatal("CancelTask should invoke the run's cancel func")
	}
}
