package orchestrator

import (
	"context"
	"testing"

	"github.com/paralin/fraglands/core"
)

func TestIssueJoinIntent(t *testing.T) {
	o, id, owner, _ := setupReady(t)

	if _, err := o.Claim(owner, id); err != nil {
		t.Fatal(err.Error())
	}

	target, err := o.IssueJoinIntent(owner, id)
	if err != nil {
		t.Fatal(err.Error())
	}
	if target.Intent.AccountID != owner.ID {
		t.Fatalf("expected %s, got %s", owner.ID, target.Intent.AccountID)
	}
	if target.Intent.SteamID != owner.SteamID {
		t.Fatal("expected intent bound to the principal steam identity")
	}
	if target.Intent.RevisionID == "" {
		t.Fatal("expected intent bound to a revision")
	}
	if target.Intent.Generation == 0 {
		t.Fatal("expected intent bound to a process generation")
	}
	if target.Process != nil && target.Process.Generation != target.Intent.Generation {
		t.Fatal("expected intent bound to the target process generation")
	}
}

func TestIssueJoinIntentWithoutSlot(t *testing.T) {
	o, id, owner, _ := setupReady(t)

	if _, err := o.IssueJoinIntent(owner, id); err != core.ErrNoSlotClaimed {
		t.Fatalf("expected ErrNoSlotClaimed, got %v", err)
	}
}

func TestIssueJoinIntentUnknownPreparation(t *testing.T) {
	o, _, owner, _ := setupReady(t)

	if _, err := o.IssueJoinIntent(owner, "nonexistent"); err != ErrUnknownPreparation {
		t.Fatalf("expected ErrUnknownPreparation, got %v", err)
	}
}

func TestIssueJoinIntentAllocationFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	owner, _ := testAccounts()
	sources := []core.ReplaySource{{ID: "replay-1"}}
	o := NewOrchestrator(ctx, sources, &mockPreparer{}, &mockAllocator{fail: true}, testIdentityAuthority())

	id, err := o.Prepare(owner, "replay-1", 0, 63280)
	if err != nil {
		t.Fatal(err.Error())
	}
	if _, err := o.Claim(owner, id); err != nil {
		t.Fatal(err.Error())
	}

	waitAllocated(t, o, id)
	_, err = o.IssueJoinIntent(owner, id)
	var allocErr *AllocationError
	if !errorsAs(err, &allocErr) {
		t.Fatalf("expected AllocationError, got %v", err)
	}
	if allocErr.Reason.Code != AllocationFailureCode {
		t.Fatalf("expected %s, got %s", AllocationFailureCode, allocErr.Reason.Code)
	}
}

func TestConsumeJoinIntent(t *testing.T) {
	o, id, owner, _ := setupReady(t)

	if _, err := o.Claim(owner, id); err != nil {
		t.Fatal(err.Error())
	}
	target, err := o.IssueJoinIntent(owner, id)
	if err != nil {
		t.Fatal(err.Error())
	}

	// Consume with the bound revision, generation, and Steam identity.
	if err := o.ConsumeJoinIntent(target.Intent.ID, target.Intent.RevisionID, target.Intent.Generation, owner.SteamID); err != nil {
		t.Fatal(err.Error())
	}

	// One-use: the second consume is refused.
	if err := o.ConsumeJoinIntent(target.Intent.ID, target.Intent.RevisionID, target.Intent.Generation, owner.SteamID); err != core.ErrIntentAlreadyUsed {
		t.Fatalf("expected ErrIntentAlreadyUsed, got %v", err)
	}
}

func TestConsumeJoinIntentMismatch(t *testing.T) {
	o, id, owner, _ := setupReady(t)

	if _, err := o.Claim(owner, id); err != nil {
		t.Fatal(err.Error())
	}
	target, err := o.IssueJoinIntent(owner, id)
	if err != nil {
		t.Fatal(err.Error())
	}

	// Refused consumes never burn the intent.
	if err := o.ConsumeJoinIntent(target.Intent.ID, "rev-other", target.Intent.Generation, owner.SteamID); err != core.ErrRevisionMismatch {
		t.Fatalf("expected ErrRevisionMismatch, got %v", err)
	}
	if err := o.ConsumeJoinIntent(target.Intent.ID, target.Intent.RevisionID, target.Intent.Generation+1, owner.SteamID); err != core.ErrGenerationMismatch {
		t.Fatalf("expected ErrGenerationMismatch, got %v", err)
	}
	if err := o.ConsumeJoinIntent(target.Intent.ID, target.Intent.RevisionID, target.Intent.Generation, core.SteamID(999)); err != core.ErrSteamIDAlreadyBound {
		t.Fatalf("expected ErrSteamIDAlreadyBound, got %v", err)
	}
	if target.Intent.Used() {
		t.Fatal("refused consumes must not burn the intent")
	}
	if err := o.ConsumeJoinIntent("nonexistent", "rev", 1, owner.SteamID); err != ErrUnknownIntent {
		t.Fatalf("expected ErrUnknownIntent, got %v", err)
	}
}
