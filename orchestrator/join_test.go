package orchestrator

import (
	"context"
	"testing"

	"github.com/paralin/fraglands/core"
)

var testSteam = core.SteamID(76561198000000001)

func TestIssueJoinIntent(t *testing.T) {
	o, id := setupReady(t)

	if _, err := o.Claim(id, "acct-a"); err != nil {
		t.Fatal(err.Error())
	}

	target, err := o.IssueJoinIntent(id, "acct-a", testSteam)
	if err != nil {
		t.Fatal(err.Error())
	}
	if target.Intent.AccountID != "acct-a" {
		t.Fatalf("expected acct-a, got %s", target.Intent.AccountID)
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
	o, id := setupReady(t)

	if _, err := o.IssueJoinIntent(id, "acct-a", testSteam); err != core.ErrNoSlotClaimed {
		t.Fatalf("expected ErrNoSlotClaimed, got %v", err)
	}
}

func TestIssueJoinIntentUnknownPreparation(t *testing.T) {
	o, _ := setupReady(t)

	if _, err := o.IssueJoinIntent("nonexistent", "acct-a", testSteam); err != ErrUnknownPreparation {
		t.Fatalf("expected ErrUnknownPreparation, got %v", err)
	}
}

func TestIssueJoinIntentAllocationFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sources := []core.ReplaySource{{ID: "replay-1"}}
	o := NewOrchestrator(ctx, sources, &mockPreparer{}, &mockAllocator{fail: true})

	id, err := o.Prepare("replay-1", 0, 63280)
	if err != nil {
		t.Fatal(err.Error())
	}
	if _, err := o.Claim(id, "acct-a"); err != nil {
		t.Fatal(err.Error())
	}

	waitAllocated(t, o, id)
	_, err = o.IssueJoinIntent(id, "acct-a", testSteam)
	if err == nil {
		t.Fatal("expected join intent refused on allocation failure")
	}
}

func TestConsumeJoinIntent(t *testing.T) {
	o, id := setupReady(t)

	if _, err := o.Claim(id, "acct-a"); err != nil {
		t.Fatal(err.Error())
	}
	target, err := o.IssueJoinIntent(id, "acct-a", testSteam)
	if err != nil {
		t.Fatal(err.Error())
	}

	// Consume with the bound revision, generation, and Steam identity.
	if err := o.ConsumeJoinIntent(target.Intent.ID, target.Intent.RevisionID, target.Intent.Generation, testSteam); err != nil {
		t.Fatal(err.Error())
	}

	// One-use: the second consume is refused.
	if err := o.ConsumeJoinIntent(target.Intent.ID, target.Intent.RevisionID, target.Intent.Generation, testSteam); err != core.ErrIntentAlreadyUsed {
		t.Fatalf("expected ErrIntentAlreadyUsed, got %v", err)
	}
}

func TestConsumeJoinIntentMismatch(t *testing.T) {
	o, id := setupReady(t)

	if _, err := o.Claim(id, "acct-a"); err != nil {
		t.Fatal(err.Error())
	}
	target, err := o.IssueJoinIntent(id, "acct-a", testSteam)
	if err != nil {
		t.Fatal(err.Error())
	}

	// Refused consumes never burn the intent.
	if err := o.ConsumeJoinIntent(target.Intent.ID, "rev-other", target.Intent.Generation, testSteam); err != core.ErrRevisionMismatch {
		t.Fatalf("expected ErrRevisionMismatch, got %v", err)
	}
	if err := o.ConsumeJoinIntent(target.Intent.ID, target.Intent.RevisionID, target.Intent.Generation+1, testSteam); err != core.ErrGenerationMismatch {
		t.Fatalf("expected ErrGenerationMismatch, got %v", err)
	}
	if err := o.ConsumeJoinIntent(target.Intent.ID, target.Intent.RevisionID, target.Intent.Generation, core.SteamID(999)); err != core.ErrSteamIDAlreadyBound {
		t.Fatalf("expected ErrSteamIDAlreadyBound, got %v", err)
	}
	if target.Intent.Used() {
		t.Fatal("refused consumes must not burn the intent")
	}
	if err := o.ConsumeJoinIntent("nonexistent", "rev", 1, testSteam); err != ErrUnknownIntent {
		t.Fatalf("expected ErrUnknownIntent, got %v", err)
	}
}
