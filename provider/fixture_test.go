package provider

import (
	"context"
	"os"
	"testing"

	"github.com/paralin/fraglands/core"
)

// TestOptInPinnedReplayProvider runs the full provider path against a real
// pinned demo: the store reads the file named by FRAGLANDS_PINNED_DEMO, the
// real s2replay prover proves the ServerInfo tick interval, and the real
// analysis.ExtractRunbackFacts extracts facts at the takeover tick. It is
// skipped unless the environment variable is set.
func TestOptInPinnedReplayProvider(t *testing.T) {
	path := os.Getenv("FRAGLANDS_PINNED_DEMO")
	if path == "" {
		t.Skip("set FRAGLANDS_PINNED_DEMO to run the pinned replay provider test")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	store := newFakeStore()
	store.add("replay-1", data)
	p := New(store, nil, 5) // nil facts: production analysis.ExtractRunbackFacts

	prep := core.NewScenarioPreparation("prep-1", "replay-1", 0, 63280)
	if err := p.Prepare(context.Background(), prep); err != nil {
		t.Fatal(err.Error())
	}
	if prep.State() != core.PreparationReady {
		t.Fatalf("expected ready, got %s", prep.State())
	}
	rev := prep.Revision()
	if rev == nil {
		t.Fatal("expected a revision")
	}
	if rev.TakeoverTick != 63280 {
		t.Fatalf("expected takeover tick 63280, got %d", rev.TakeoverTick)
	}
	t.Logf("revision id=%s fidelity=%s lead_in_start=%d omissions=%d",
		rev.ID, rev.Fidelity, rev.LeadInStartTick, len(rev.Omissions))
}
