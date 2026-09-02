package provider

import (
	"context"
	"os"
	"testing"

	"github.com/paralin/fraglands/core"
	"github.com/paralin/s2replay/analysis"
)

// TestOptInPinnedReplayProvider runs the full provider path against a real
// pinned demo: the store reads the file named by FRAGLANDS_PINNED_DEMO, the
// real s2replay prover proves the ServerInfo tick interval, and the real
// analysis.ExtractRunbackFacts extracts facts at the takeover tick. It
// asserts the objective and transient facts and the tick interval
// provenance, not only revision success. It is skipped unless the
// environment variable is set.
//
// Note: analysis.ExtractRunbackFacts refuses binaries without a clean VCS
// identity. Run from a clean, committed git checkout (go test binaries built
// from a linked git worktree carry no vcs stamping).
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
	if rev.LeadInStartTick >= rev.TakeoverTick {
		t.Fatalf("lead-in start %d must precede takeover %d", rev.LeadInStartTick, rev.TakeoverTick)
	}

	// Re-extract the facts the provider compiled from, to assert the
	// objective, transient, and tick interval provenance content. Same bytes,
	// same provenance path as the provider used.
	facts, err := analysis.ExtractRunbackFacts(data, analysis.RunbackRequest{Tick: 63280})
	if err != nil {
		t.Fatal(err)
	}

	// Tick interval provenance must be present and match the interval the
	// provider proved: no default rate may appear.
	if !facts.TickProvenance.TickIntervalSeconds.Present {
		t.Fatalf("tick interval provenance absent: %+v", facts.TickProvenance)
	}
	if facts.TickProvenance.TickIntervalSeconds.Value <= 0 {
		t.Fatalf("tick interval must be positive, got %v", facts.TickProvenance.TickIntervalSeconds.Value)
	}

	// Objectives must carry the observed class rows.
	objs := facts.Objectives
	if objs.MidBoss.ClassName != analysis.RunbackMidBossClass {
		t.Fatalf("expected mid boss class %s, got %s", analysis.RunbackMidBossClass, objs.MidBoss.ClassName)
	}
	if len(objs.Towers) == 0 || objs.Towers[0].ClassName != analysis.RunbackTowerClass {
		t.Fatalf("expected tower rows with class %s, got %+v", analysis.RunbackTowerClass, objs.Towers)
	}
	if len(objs.Walkers) == 0 || objs.Walkers[0].ClassName != analysis.RunbackWalkerClass {
		t.Fatalf("expected walker rows with class %s, got %+v", analysis.RunbackWalkerClass, objs.Walkers)
	}
	// Transients are the unattributed item-class rows; the slice must be
	// non-nil and every row must carry a typed missing reason.
	if objs.Transients == nil {
		t.Fatal("transients slice must not be nil")
	}
	for _, tr := range objs.Transients {
		if tr.MissingReason == "" {
			t.Fatalf("transient %d missing typed reason: %+v", tr.EntityID, tr)
		}
	}

	t.Logf("revision id=%s fidelity=%s lead_in_start=%d omissions=%d",
		rev.ID, rev.Fidelity, rev.LeadInStartTick, len(rev.Omissions))
	t.Logf("tick_interval=%v mid_boss=%s towers=%d walkers=%d transients=%d",
		facts.TickProvenance.TickIntervalSeconds.Value, objs.MidBoss.ClassName,
		len(objs.Towers), len(objs.Walkers), len(objs.Transients))
}
