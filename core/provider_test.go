package core

import (
	"reflect"
	"testing"

	"github.com/paralin/s2replay/analysis"
)

// runbackFactsFixture builds a minimal eligible RunbackFacts record: one hero
// with all nine kinematic fields observed exactly at the tick.
func runbackFactsFixture() analysis.RunbackFacts {
	tick := uint32(63280)
	field := func() analysis.RunbackFloat {
		return analysis.RunbackFloat{
			Value: 100, Present: true, SourceTick: tick, FreshnessTicks: 0,
		}
	}
	vec := func() [3]analysis.RunbackFloat { return [3]analysis.RunbackFloat{field(), field(), field()} }
	return analysis.RunbackFacts{
		SchemaVersion: analysis.RunbackFactsSchemaVersion,
		Tick:          tick,
		Source: analysis.ReplaySourceIdentity{
			SHA256: "abc123", Game: "deadlock", Map: "dl_midtown", GameBuild: 9001,
		},
		Heroes: []analysis.RunbackHero{{
			PlayerSlot: 3,
			Position:   vec(),
			Facing:     vec(),
			Velocity:   vec(),
		}},
		Eligibility: analysis.ReplayEligibilityEligible,
	}
}

func TestCompileGrantsCompleteWithNoOmissions(t *testing.T) {
	facts := runbackFactsFixture()
	out := Compile(CompileRequest{
		Facts:              facts,
		Capabilities:       DefaultRunbackCapabilities(),
		MaxFreshnessTicks:  5,
		ProvenTickInterval: 1.0 / 64.0,
		RevisionID:         "rev-1",
	})
	if out.Refusal != nil {
		t.Fatalf("unexpected refusal: %v", out.Refusal)
	}
	if out.Revision == nil {
		t.Fatal("expected a revision")
	}
	if out.Revision.Fidelity != FidelityComplete {
		t.Fatalf("expected complete fidelity, got %s", out.Revision.Fidelity)
	}
	if len(out.Omissions) != 0 {
		t.Fatalf("expected zero omissions, got %+v", out.Omissions)
	}
	if out.Revision.TakeoverTick != 63280 {
		t.Fatalf("expected takeover tick 63280, got %d", out.Revision.TakeoverTick)
	}
	if out.Revision.ReplayID != "abc123" {
		t.Fatalf("expected replay id from sha256, got %s", out.Revision.ReplayID)
	}
	if out.Revision.LeadInStartTick != 63280-320 {
		t.Fatalf("expected lead-in start 63280-320, got %d", out.Revision.LeadInStartTick)
	}
}

func TestCompileMissingFieldIsTypedNotObserved(t *testing.T) {
	facts := runbackFactsFixture()
	facts.Heroes[0].Velocity[1] = analysis.RunbackFloat{MissingReason: analysis.RunbackMissingNotInSample}

	out := Compile(CompileRequest{
		Facts:              facts,
		Capabilities:       DefaultRunbackCapabilities(),
		MaxFreshnessTicks:  5,
		ProvenTickInterval: 1.0 / 64.0,
		RevisionID:         "rev-1",
	})
	if out.Refusal != nil {
		t.Fatalf("unexpected refusal: %v", out.Refusal)
	}
	if out.Revision.Fidelity != FidelityPreview {
		t.Fatalf("expected preview fidelity, got %s", out.Revision.Fidelity)
	}
	want := Omission{
		Kind:     OmissionNotObserved,
		Subject:  "hero.3.velocity.y",
		Required: true,
		Reason:   analysis.RunbackMissingNotInSample,
	}
	if len(out.Omissions) != 1 || !reflect.DeepEqual(out.Omissions[0], want) {
		t.Fatalf("omissions = %+v, want [%+v]", out.Omissions, want)
	}
}

func TestCompileStaleFieldForcesPreview(t *testing.T) {
	facts := runbackFactsFixture()
	facts.Heroes[0].Position[0].FreshnessTicks = 20

	out := Compile(CompileRequest{
		Facts:              facts,
		Capabilities:       DefaultRunbackCapabilities(),
		MaxFreshnessTicks:  5,
		ProvenTickInterval: 1.0 / 64.0,
		RevisionID:         "rev-1",
	})
	if out.Revision.Fidelity != FidelityPreview {
		t.Fatalf("expected preview, got %s", out.Revision.Fidelity)
	}
	if len(out.Omissions) != 1 || out.Omissions[0].Kind != OmissionStale {
		t.Fatalf("expected one stale omission, got %+v", out.Omissions)
	}
}

func TestCompileUnsupportedCapabilityForcesPreview(t *testing.T) {
	facts := runbackFactsFixture()
	caps := DefaultRunbackCapabilities()
	// The runtime cannot reset velocity: the row is not fully supported.
	partial := caps["velocity.x"]
	partial.Reset = false
	caps["velocity.x"] = partial

	out := Compile(CompileRequest{
		Facts:              facts,
		Capabilities:       caps,
		MaxFreshnessTicks:  5,
		ProvenTickInterval: 1.0 / 64.0,
		RevisionID:         "rev-1",
	})
	if out.Revision.Fidelity != FidelityPreview {
		t.Fatalf("expected preview, got %s", out.Revision.Fidelity)
	}
	if len(out.Omissions) != 1 || out.Omissions[0].Kind != OmissionUnsupported {
		t.Fatalf("expected one unsupported omission, got %+v", out.Omissions)
	}
	if out.Omissions[0].Subject != "hero.3.velocity.x" {
		t.Fatalf("expected velocity.x named, got %s", out.Omissions[0].Subject)
	}
}

func TestCompileUndeclaredFreshnessBudgetForcesPreview(t *testing.T) {
	facts := runbackFactsFixture()
	out := Compile(CompileRequest{
		Facts:              facts,
		Capabilities:       DefaultRunbackCapabilities(),
		MaxFreshnessTicks:  0,
		ProvenTickInterval: 1.0 / 64.0,
		RevisionID:         "rev-1",
	})
	if out.Revision.Fidelity != FidelityPreview {
		t.Fatalf("expected preview with no declared budget, got %s", out.Revision.Fidelity)
	}
	for _, o := range out.Omissions {
		if o.Kind != OmissionStale {
			t.Fatalf("expected stale omissions, got %+v", out.Omissions)
		}
	}
}

func TestCompileIneligibleFactsNeverGrantsComplete(t *testing.T) {
	facts := runbackFactsFixture()
	facts.Eligibility = analysis.ReplayEligibilityIneligible
	facts.EligibilityReasons = []string{"hero row has missing or stale exact fields"}

	out := Compile(CompileRequest{
		Facts:              facts,
		Capabilities:       DefaultRunbackCapabilities(),
		MaxFreshnessTicks:  5,
		ProvenTickInterval: 1.0 / 64.0,
		RevisionID:         "rev-1",
	})
	if out.Revision.Fidelity != FidelityPreview {
		t.Fatalf("expected preview, got %s", out.Revision.Fidelity)
	}
	found := false
	for _, o := range out.Omissions {
		if o.Kind == OmissionIneligible {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an ineligible omission, got %+v", out.Omissions)
	}
}

func TestCompileRefusalsProduceNoPartialState(t *testing.T) {
	cases := []struct {
		name string
		req  CompileRequest
	}{
		{
			name: "zero tick",
			req: CompileRequest{
				Facts:             analysis.RunbackFacts{SchemaVersion: analysis.RunbackFactsSchemaVersion},
				RevisionID:        "rev-1",
				MaxFreshnessTicks: 5,
			},
		},
		{
			name: "unsupported schema",
			req: CompileRequest{
				Facts:             analysis.RunbackFacts{Tick: 100, SchemaVersion: 99},
				RevisionID:        "rev-1",
				MaxFreshnessTicks: 5,
			},
		},
		{
			name: "empty revision id",
			req: CompileRequest{
				Facts:             runbackFactsFixture(),
				RevisionID:        "",
				MaxFreshnessTicks: 5,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := Compile(tc.req)
			if out.Refusal == nil {
				t.Fatalf("expected refusal, got %+v", out)
			}
			if out.Revision != nil {
				t.Fatal("a refusal must not carry a revision")
			}
			if len(out.Omissions) != 0 {
				t.Fatal("a refusal must not carry omissions")
			}
			rev, err := CompileOrRefuse(tc.req)
			if err == nil || rev != nil {
				t.Fatal("CompileOrRefuse must refuse too")
			}
		})
	}
}

func TestCompileDoesNotMutateInputFacts(t *testing.T) {
	facts := runbackFactsFixture()
	before := facts.Heroes[0]
	_ = Compile(CompileRequest{
		Facts:              facts,
		Capabilities:       DefaultRunbackCapabilities(),
		MaxFreshnessTicks:  5,
		ProvenTickInterval: 1.0 / 64.0,
		RevisionID:         "rev-1",
	})
	if !reflect.DeepEqual(facts.Heroes[0], before) {
		t.Fatal("compile mutated the input facts")
	}
}

func TestPrepareScenarioWiresFactsIntoRevision(t *testing.T) {
	// The end-to-end slice: facts + capabilities go in, a preparation carrying
	// an immutable revision comes out. No possession, bots, or native mutation
	// anywhere in the path.
	facts := runbackFactsFixture()
	prep := NewScenarioPreparation("prep-1", "replay-1", 0, 63280)

	rev, omissions, err := PrepareScenario(prep, facts, DefaultRunbackCapabilities(), 5, 1.0/64.0)
	if err != nil {
		t.Fatal(err.Error())
	}
	if rev == nil {
		t.Fatal("expected a revision")
	}
	wantID := RevisionID(facts)
	if rev.ID != wantID || rev.TakeoverTick != 63280 {
		t.Fatalf("unexpected revision: %+v (want id %s)", rev, wantID)
	}
	if rev.Fidelity != FidelityComplete {
		t.Fatalf("expected complete, got %s", rev.Fidelity)
	}
	if len(omissions) != 0 {
		t.Fatalf("expected no omissions, got %+v", omissions)
	}
	if prep.Revision() != rev {
		t.Fatal("preparation must expose the compiled revision")
	}
	if prep.State() != PreparationReady {
		t.Fatalf("expected ready, got %s", prep.State())
	}
}

func TestPrepareScenarioFailsClosedWithTypedReason(t *testing.T) {
	facts := runbackFactsFixture()
	facts.Heroes[0].Facing[2] = analysis.RunbackFloat{MissingReason: analysis.RunbackMissingNotRecorded}

	prep := NewScenarioPreparation("prep-2", "replay-1", 0, 63280)
	_, _, err := PrepareScenario(prep, facts, DefaultRunbackCapabilities(), 5, 1.0/64.0)
	if err != nil {
		t.Fatal(err.Error())
	}
	if prep.State() != PreparationReady {
		t.Fatalf("expected ready, got %s", prep.State())
	}
	if prep.Revision().Fidelity != FidelityPreview {
		t.Fatalf("expected preview, got %s", prep.Revision().Fidelity)
	}
	oms := prep.Revision().Omissions
	if len(oms) == 0 {
		t.Fatal("expected omissions on the revision")
	}
	if oms[0].Kind != OmissionNotObserved || oms[0].Subject != "hero.3.facing.z" {
		t.Fatalf("unexpected omission: %+v", oms[0])
	}
	if oms[0].Reason != analysis.RunbackMissingNotRecorded {
		t.Fatalf("expected parser reason to be carried, got %s", oms[0].Reason)
	}
}

func TestPrepareScenarioRefusalFailsClosed(t *testing.T) {
	facts := runbackFactsFixture()
	facts.Tick = 0 // malformed: no moment selected

	prep := NewScenarioPreparation("prep-3", "replay-1", 0, 0)
	_, _, err := PrepareScenario(prep, facts, DefaultRunbackCapabilities(), 5, 1.0/64.0)
	if err == nil {
		t.Fatal("expected a typed failure")
	}
	if prep.State() != PreparationFailed {
		t.Fatalf("expected failed, got %s", prep.State())
	}
	if prep.Failure() == nil || prep.Failure().Code != "compile_refused" {
		t.Fatalf("expected compile_refused, got %+v", prep.Failure())
	}
	if prep.Revision() != nil {
		t.Fatal("a failed preparation must not carry a revision")
	}
}

func TestRevisionIDIsDeterministic(t *testing.T) {
	facts := runbackFactsFixture()
	a := RevisionID(facts)
	b := RevisionID(facts)
	if a != b {
		t.Fatalf("revision id must be deterministic: %s vs %s", a, b)
	}
	// A different moment yields a different revision.
	facts.Tick = 64000
	if RevisionID(facts) == a {
		t.Fatal("a different tick must yield a different revision id")
	}
}
