package orchestrator

import (
	"context"
	"testing"

	"github.com/paralin/fraglands/core"
)

// setupReady prepares one replay to ready with an allocated process.
func setupReady(t *testing.T) (*Orchestrator, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	sources := []core.ReplaySource{{ID: "replay-1"}}
	o := NewOrchestrator(ctx, sources, &mockPreparer{}, &mockAllocator{})

	id, err := o.Prepare("replay-1", 0, 63280)
	if err != nil {
		t.Fatal(err.Error())
	}
	waitAllocated(t, o, id)
	return o, id
}

func TestLobbyClaim(t *testing.T) {
	o, id := setupReady(t)

	slot, err := o.Claim(id, "acct-a")
	if err != nil {
		t.Fatal(err.Error())
	}
	if slot != 0 {
		t.Fatalf("expected slot 0, got %d", slot)
	}

	// A repeated claim is idempotent.
	slot, err = o.Claim(id, "acct-a")
	if err != nil {
		t.Fatal(err.Error())
	}
	if slot != 0 {
		t.Fatalf("expected idempotent slot 0, got %d", slot)
	}
}

func TestLobbyRelease(t *testing.T) {
	o, id := setupReady(t)

	if _, err := o.Claim(id, "acct-a"); err != nil {
		t.Fatal(err.Error())
	}
	if err := o.Release(id, "acct-a"); err != nil {
		t.Fatal(err.Error())
	}
	if err := o.Release(id, "acct-a"); err != core.ErrNoSlotClaimed {
		t.Fatalf("expected ErrNoSlotClaimed, got %v", err)
	}
}

func TestClaimUnknownPreparation(t *testing.T) {
	o, _ := setupReady(t)

	if _, err := o.Claim("nonexistent", "acct-a"); err != ErrUnknownPreparation {
		t.Fatalf("expected ErrUnknownPreparation, got %v", err)
	}
	if err := o.Release("nonexistent", "acct-a"); err != ErrUnknownPreparation {
		t.Fatalf("expected ErrUnknownPreparation, got %v", err)
	}
}
