package orchestrator

import (
	"testing"

	"github.com/paralin/fraglands/core"
)

func TestAcceptAndRetrieveResult(t *testing.T) {
	o, id, owner, _ := setupReady(t)

	if _, err := o.Claim(owner, id); err != nil {
		t.Fatal(err.Error())
	}
	target, err := o.IssueJoinIntent(owner, id)
	if err != nil {
		t.Fatal(err.Error())
	}

	// The server participant accepts one private result for the attempt.
	result := &core.AttemptResult{
		ID:                "res-1",
		AccountID:         owner.ID,
		RevisionID:        target.Intent.RevisionID,
		ProcessGeneration: target.Intent.Generation,
		AttemptGeneration: 1,
		ReplayID:          "replay-1",
		TakeoverTick:      63280,
	}
	if err := o.AcceptResult(result); err != nil {
		t.Fatal(err.Error())
	}

	// The debrief retrieval is private: only the owning account reads it.
	got, err := o.Result(owner, target.Intent.Generation, 1)
	if err != nil {
		t.Fatal(err.Error())
	}
	if got != result {
		t.Fatal("expected the accepted result")
	}

	// A second result for one attempt is refused.
	if err := o.AcceptResult(result); err != core.ErrResultAlreadyAccepted {
		t.Fatalf("expected ErrResultAlreadyAccepted, got %v", err)
	}

	// No result exists for another attempt.
	if _, err := o.Result(owner, target.Intent.Generation, 2); err != core.ErrNoResult {
		t.Fatalf("expected ErrNoResult, got %v", err)
	}
}
