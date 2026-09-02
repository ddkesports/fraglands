package orchestrator

import (
	"context"
	"testing"

	"github.com/paralin/fraglands/core"
)

// setupReady prepares one replay to ready with an allocated process,
// owned by the test owner principal.
func setupReady(t *testing.T) (*Orchestrator, string, *core.Account, *core.Account) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	owner, other := testAccounts()
	sources := []core.ReplaySource{{ID: "replay-1"}}
	o := NewOrchestrator(ctx, sources, &mockPreparer{}, &mockAllocator{}, testIdentityAuthority())

	id, err := o.Prepare(owner, "replay-1", 0, 63280)
	if err != nil {
		t.Fatal(err.Error())
	}
	waitAllocated(t, o, id)
	return o, id, owner, other
}

func TestLobbyClaim(t *testing.T) {
	o, id, owner, _ := setupReady(t)

	slot, err := o.Claim(owner, id)
	if err != nil {
		t.Fatal(err.Error())
	}
	if slot != 0 {
		t.Fatalf("expected slot 0, got %d", slot)
	}

	// A repeated claim is idempotent.
	slot, err = o.Claim(owner, id)
	if err != nil {
		t.Fatal(err.Error())
	}
	if slot != 0 {
		t.Fatalf("expected idempotent slot 0, got %d", slot)
	}
}

func TestLobbyRelease(t *testing.T) {
	o, id, owner, _ := setupReady(t)

	if _, err := o.Claim(owner, id); err != nil {
		t.Fatal(err.Error())
	}
	if err := o.Release(owner, id); err != nil {
		t.Fatal(err.Error())
	}
	if err := o.Release(owner, id); err != core.ErrNoSlotClaimed {
		t.Fatalf("expected ErrNoSlotClaimed, got %v", err)
	}
}

func TestClaimUnknownPreparation(t *testing.T) {
	o, _, owner, _ := setupReady(t)

	if _, err := o.Claim(owner, "nonexistent"); err != ErrUnknownPreparation {
		t.Fatalf("expected ErrUnknownPreparation, got %v", err)
	}
	if err := o.Release(owner, "nonexistent"); err != ErrUnknownPreparation {
		t.Fatalf("expected ErrUnknownPreparation, got %v", err)
	}
}
