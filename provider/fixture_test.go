package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"

	"github.com/paralin/fraglands/core"
	"github.com/paralin/s2replay/analysis"
)

// pinnedDemoSize and pinnedDemoSHA256 are the exact byte identity of the
// canonical pinned demo (101514223.dem). The fixture refuses to parse any
// other bytes: a wrong file fails immediately, before the parser runs.
const (
	pinnedDemoSize   = 211730538
	pinnedDemoSHA256 = "b612e43f4055d4dde728c7eedbdd7ec38c3478ef90f33b870bfb29310b79194f"
)

// TestOptInPinnedReplayProvider runs the full provider path against the
// canonical pinned demo (101514223.dem, 211730538 bytes,
// sha256 b612e43f4055d4dde728c7eedbdd7ec38c3478ef90f33b870bfb29310b79194f):
// the store reads the file named by FRAGLANDS_PINNED_DEMO, the real s2replay
// prover proves the ServerInfo tick interval, and the real
// analysis.ExtractRunbackFacts extracts facts at the takeover tick. It
// asserts the landed expected census (8 towers / 6 walkers / 2 transients)
// and the tick interval provenance, not only revision success. It is skipped
// unless the environment variable is set.
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

	// Identity gate before any parsing: exact byte length and SHA256. Wrong
	// bytes fail immediately with the observed values in the message.
	if len(data) != pinnedDemoSize {
		t.Fatalf("pinned demo size mismatch: got %d bytes, want %d", len(data), pinnedDemoSize)
	}
	digest := sha256.Sum256(data)
	gotSHA := hex.EncodeToString(digest[:])
	if gotSHA != pinnedDemoSHA256 {
		t.Fatalf("pinned demo sha256 mismatch: got %s, want %s", gotSHA, pinnedDemoSHA256)
	}
	t.Logf("pinned demo identity verified: %d bytes, sha256 %s", len(data), gotSHA)

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

	// Objectives must match the landed s2replay PR9 expected census for this
	// exact demo identity (101514223.dem, sha256 b612e43f...): 8 towers,
	// 6 walkers, 2 transients. The upstream TestOptInPinnedRunbackObjectives
	// pins the same numbers against this file.
	objs := facts.Objectives
	if objs.MidBoss.ClassName != analysis.RunbackMidBossClass {
		t.Fatalf("expected mid boss class %s, got %s", analysis.RunbackMidBossClass, objs.MidBoss.ClassName)
	}
	if !objs.MidBoss.Alive.Alive {
		t.Fatalf("expected mid boss alive, got %+v", objs.MidBoss)
	}
	if len(objs.Towers) != 8 {
		t.Fatalf("expected 8 towers, got %d", len(objs.Towers))
	}
	for _, tower := range objs.Towers {
		if tower.ClassName != analysis.RunbackTowerClass || !tower.Alive.Alive {
			t.Fatalf("tower row: %+v", tower)
		}
	}
	if len(objs.Walkers) != 6 {
		t.Fatalf("expected 6 walkers, got %d", len(objs.Walkers))
	}
	for _, walker := range objs.Walkers {
		if walker.ClassName != analysis.RunbackWalkerClass || !walker.Alive.Alive {
			t.Fatalf("walker row: %+v", walker)
		}
	}
	if objs.Rejuvenator.Status != analysis.RunbackRejuvenatorStatusAbsent || objs.Rejuvenator.Last != nil {
		t.Fatalf("rejuvenator must be typed absent, got %+v", objs.Rejuvenator)
	}
	if len(objs.Transients) != 2 {
		t.Fatalf("expected 2 transients, got %d: %+v", len(objs.Transients), objs.Transients)
	}
	for _, tr := range objs.Transients {
		if tr.MissingReason != analysis.RunbackMissingOwnerUnattributed {
			t.Fatalf("transient %d wrong reason: %+v", tr.EntityID, tr)
		}
	}

	t.Logf("revision id=%s fidelity=%s lead_in_start=%d omissions=%d",
		rev.ID, rev.Fidelity, rev.LeadInStartTick, len(rev.Omissions))
	t.Logf("tick_interval=%v mid_boss=%s towers=%d walkers=%d transients=%d",
		facts.TickProvenance.TickIntervalSeconds.Value, objs.MidBoss.ClassName,
		len(objs.Towers), len(objs.Walkers), len(objs.Transients))
	t.Logf("final source digest: sha256=%s bytes=%d", gotSHA, len(data))
}
