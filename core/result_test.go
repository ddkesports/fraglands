package core

import (
	"testing"
)

func TestResultStoreAcceptAndLookup(t *testing.T) {
	s := NewResultStore()
	res := &AttemptResult{
		ID:                "res-1",
		AccountID:         "acct-1",
		RevisionID:        "rev-1",
		ProcessGeneration: 2,
		AttemptGeneration: 7,
		ReplayID:          "replay-1",
		TakeoverTick:      63280,
	}
	if err := s.Accept(res); err != nil {
		t.Fatal(err.Error())
	}

	got, err := s.Lookup("acct-1", 2, 7)
	if err != nil {
		t.Fatal(err.Error())
	}
	if got != res {
		t.Fatal("expected the accepted result")
	}

	// Duplicate acceptance for the same attempt is refused.
	if err := s.Accept(res); err != ErrResultAlreadyAccepted {
		t.Fatalf("expected ErrResultAlreadyAccepted, got %v", err)
	}

	// A different attempt generation is a distinct result.
	if err := s.Accept(&AttemptResult{ID: "res-2", AccountID: "acct-1", ProcessGeneration: 2, AttemptGeneration: 8}); err != nil {
		t.Fatal(err.Error())
	}

	if _, err := s.Lookup("acct-1", 2, 99); err != ErrNoResult {
		t.Fatalf("expected ErrNoResult, got %v", err)
	}
}
