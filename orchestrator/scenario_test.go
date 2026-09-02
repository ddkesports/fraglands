package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/paralin/fraglands/core"
)

// waitAllocated waits until the preparation has a process or an allocation
// failure recorded.
func waitAllocated(t *testing.T, o *Orchestrator, id string) PreparationStatus {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, err := o.Preparation(id)
		if err != nil {
			t.Fatal(err.Error())
		}
		if status.Process != nil || status.AllocationFailure != nil {
			return status
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for allocation")
	return PreparationStatus{}
}

func TestPrepareLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sources := []core.ReplaySource{{ID: "replay-1", DisplayName: "Replay One", FileName: "one.dem"}}
	o := NewOrchestrator(ctx, sources, &mockPreparer{}, &mockAllocator{})

	id, err := o.Prepare("replay-1", 0, 63280)
	if err != nil {
		t.Fatal(err.Error())
	}

	status := waitAllocated(t, o, id)
	if status.Preparation.State() != core.PreparationReady {
		t.Fatalf("expected ready, got %s", status.Preparation.State())
	}
	if status.Preparation.Revision() == nil {
		t.Fatal("expected revision on ready preparation")
	}
	if status.Lobby == nil {
		t.Fatal("expected lobby to be created")
	}
	if status.Lobby.Capacity != defaultLobbyCapacity {
		t.Fatalf("expected %d slots, got %d", defaultLobbyCapacity, status.Lobby.Capacity)
	}
	if status.Process == nil {
		t.Fatal("expected process to be allocated")
	}
	if !status.Process.Ready() {
		t.Fatal("expected process readiness evidence")
	}
	if status.Process.Evidence() == "" {
		t.Fatal("expected non-empty readiness evidence")
	}
	if status.AllocationFailure != nil {
		t.Fatal("unexpected allocation failure")
	}
}

func TestPrepareUnknownReplay(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sources := []core.ReplaySource{{ID: "replay-1"}}
	o := NewOrchestrator(ctx, sources, &mockPreparer{}, &mockAllocator{})

	if _, err := o.Prepare("replay-other", 0, 63280); err != ErrUnknownReplay {
		t.Fatalf("expected ErrUnknownReplay, got %v", err)
	}
	if _, err := o.Preparation("prep-1"); err != ErrUnknownPreparation {
		t.Fatalf("expected ErrUnknownPreparation, got %v", err)
	}
}

func TestPrepareFailureTypedReason(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sources := []core.ReplaySource{{ID: "replay-1"}}
	o := NewOrchestrator(ctx, sources, &mockPreparer{fail: true}, &mockAllocator{})

	id, err := o.Prepare("replay-1", 0, 63280)
	if err != nil {
		t.Fatal(err.Error())
	}

	state, err := o.preparation(id).WaitReady(ctx)
	if err != nil {
		t.Fatal(err.Error())
	}
	if state != core.PreparationFailed {
		t.Fatalf("expected failed, got %s", state)
	}

	status, err := o.Preparation(id)
	if err != nil {
		t.Fatal(err.Error())
	}
	if status.Preparation.State() != core.PreparationFailed {
		t.Fatalf("expected failed, got %s", status.Preparation.State())
	}
	if status.Preparation.Failure() == nil || status.Preparation.Failure().Code != "test_fail" {
		t.Fatal("expected typed failure reason")
	}
	if status.Process != nil {
		t.Fatal("failed preparation must not allocate a process")
	}
}

func TestPrepareAllocationFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sources := []core.ReplaySource{{ID: "replay-1"}}
	o := NewOrchestrator(ctx, sources, &mockPreparer{}, &mockAllocator{fail: true})

	id, err := o.Prepare("replay-1", 0, 63280)
	if err != nil {
		t.Fatal(err.Error())
	}

	status := waitAllocated(t, o, id)
	if status.AllocationFailure == nil {
		t.Fatal("expected allocation failure")
	}
	if status.AllocationFailure.Reason.Code != AllocationFailureCode {
		t.Fatalf("expected %s, got %s", AllocationFailureCode, status.AllocationFailure.Reason.Code)
	}
	if status.Process != nil {
		t.Fatal("failed allocation must not leave a process")
	}
}
