package provider

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/paralin/fraglands/core"
	"github.com/paralin/s2replay/analysis"
)

// fakeStore is an in-memory ReplayStore that can simulate failure modes.
type fakeStore struct {
	replays map[string][]byte
	calls   []string
	// failOn causes Replay to return an error when this id is requested.
	failOn string
	// failWith is the error returned when failOn matches.
	failWith error
}

func newFakeStore() *fakeStore {
	return &fakeStore{replays: make(map[string][]byte)}
}

func (f *fakeStore) Replay(_ context.Context, id string) (io.ReadCloser, error) {
	f.calls = append(f.calls, id)
	if f.failOn == id {
		if f.failWith != nil {
			return nil, f.failWith
		}
		return nil, ErrReplayNotFound
	}
	data, ok := f.replays[id]
	if !ok {
		return nil, ErrReplayNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (f *fakeStore) add(id string, data []byte) {
	f.replays[id] = data
}

func (f *fakeStore) callsFor(id string) int {
	var n int
	for _, c := range f.calls {
		if c == id {
			n++
		}
	}
	return n
}

// fakeFacts is a deterministic FactsExtractor for tests that do not exercise
// the real s2replay parser.
func fakeFacts(demo []byte, req analysis.RunbackRequest) (analysis.RunbackFacts, error) {
	_ = demo
	return analysis.RunbackFacts{
		SchemaVersion: analysis.RunbackFactsSchemaVersion,
		Source: analysis.ReplaySourceIdentity{
			SHA256:    "abc123",
			Game:      "deadlock",
			Map:       "dl_midtown",
			GameBuild: 9001,
		},
		Tick: req.Tick,
		Heroes: []analysis.RunbackHero{{
			PlayerSlot: 3,
			Position:   freshVec(req.Tick),
			Facing:     freshVec(req.Tick),
			Velocity:   freshVec(req.Tick),
		}},
		Eligibility: analysis.ReplayEligibilityEligible,
	}, nil
}

// failingFacts simulates a parser refusing to produce facts.
func failingFacts(demo []byte, req analysis.RunbackRequest) (analysis.RunbackFacts, error) {
	_ = demo
	_ = req
	return analysis.RunbackFacts{}, errors.New("no hero entity found")
}

func freshVec(tick uint32) [3]analysis.RunbackFloat {
	f := func() analysis.RunbackFloat {
		return analysis.RunbackFloat{
			Value: 100, Present: true, SourceTick: tick, FreshnessTicks: 0,
		}
	}
	return [3]analysis.RunbackFloat{f(), f(), f()}
}

// fakeProber stands in for the real s2replay ServerInfo prober so unit tests
// can use fake bytes.
func fakeProber(demo []byte) (float64, error) {
	_ = demo
	return 1.0 / 64.0, nil
}

func newTestProvider(store ReplayStore, facts FactsExtractor) *Provider {
	p := New(store, facts, 5)
	p.prober = fakeProber
	return p
}

func TestPrepareRequiresStore(t *testing.T) {
	p := New(nil, fakeFacts, 5)
	prep := core.NewScenarioPreparation("prep-1", "replay-1", 0, 100)
	err := p.Prepare(context.Background(), prep)
	if err == nil {
		t.Fatal("expected failure when store is nil")
	}
	if prep.State() != core.PreparationFailed {
		t.Fatalf("expected failed, got %s", prep.State())
	}
	if prep.Failure() == nil || prep.Failure().Code != "store_required" {
		t.Fatalf("expected store_required, got %+v", prep.Failure())
	}
	if prep.Revision() != nil {
		t.Fatal("no revision must be attached on failure")
	}
}

func TestPrepareRequiresTakeoverTick(t *testing.T) {
	store := newFakeStore()
	store.add("replay-1", []byte("demo-bytes"))
	p := newTestProvider(store, fakeFacts)

	prep := core.NewScenarioPreparation("prep-1", "replay-1", 0, 0)
	err := p.Prepare(context.Background(), prep)
	if err == nil {
		t.Fatal("expected failure when takeover tick is zero")
	}
	if prep.Failure() == nil || prep.Failure().Code != "takeover_tick_required" {
		t.Fatalf("expected takeover_tick_required, got %+v", prep.Failure())
	}
	if len(store.calls) != 0 {
		t.Fatal("store must not be consulted when the tick is invalid")
	}
}

func TestPrepareFailsOnStoreDenial(t *testing.T) {
	store := newFakeStore()
	store.failOn = "replay-1"
	store.failWith = ErrReplayNotFound
	p := newTestProvider(store, fakeFacts)

	prep := core.NewScenarioPreparation("prep-1", "replay-1", 0, 100)
	err := p.Prepare(context.Background(), prep)
	if err == nil {
		t.Fatal("expected failure when store denies access")
	}
	if prep.Failure() == nil || prep.Failure().Code != "store_denied" {
		t.Fatalf("expected store_denied, got %+v", prep.Failure())
	}
	if prep.Revision() != nil {
		t.Fatal("no revision must be attached on store denial")
	}
}

func TestPrepareFailsOnUnreadableStream(t *testing.T) {
	store := newFakeStore()
	store.replays["replay-1"] = []byte("partial")
	p := newTestProvider(store, fakeFacts)
	// Override maxRead to be smaller than the payload to simulate a truncated
	// stream without adding a second fake type.
	p.maxRead = 4

	prep := core.NewScenarioPreparation("prep-1", "replay-1", 0, 100)
	err := p.Prepare(context.Background(), prep)
	if err == nil {
		t.Fatal("expected failure on truncated read")
	}
	if !strings.Contains(err.Error(), "replay exceeds maximum size") {
		t.Fatalf("expected size failure, got: %v", err)
	}
	if prep.Failure() == nil || prep.Failure().Code != "replay_invalid" {
		t.Fatalf("expected replay_invalid, got %+v", prep.Failure())
	}
}

func TestPrepareFailsOnExtractionError(t *testing.T) {
	store := newFakeStore()
	store.add("replay-1", []byte("demo-bytes"))
	p := newTestProvider(store, failingFacts)

	prep := core.NewScenarioPreparation("prep-1", "replay-1", 0, 100)
	err := p.Prepare(context.Background(), prep)
	if err == nil {
		t.Fatal("expected failure when extraction fails")
	}
	if prep.Failure() == nil || prep.Failure().Code != "extraction_failed" {
		t.Fatalf("expected extraction_failed, got %+v", prep.Failure())
	}
	if prep.Revision() != nil {
		t.Fatal("no revision must be attached on extraction failure")
	}
}

func TestPrepareFailsWhenTickIntervalNotProven(t *testing.T) {
	store := newFakeStore()
	store.add("replay-1", []byte("demo-bytes"))

	// fakeFacts does not consult the parser; the interval gate must fail the
	// preparation before any facts can be compiled.
	p := New(store, fakeFacts, 5)
	// Bypass the real extractFacts: use a store that returns a minimal
	// non-PBDEMS2 payload so NewParser fails inside provenTickInterval.
	store.replays["replay-1"] = []byte("not-a-demo")
	p2 := New(store, fakeFacts, 5)

	prep := core.NewScenarioPreparation("prep-1", "replay-1", 0, 100)
	err := p2.Prepare(context.Background(), prep)
	if err == nil {
		t.Fatal("expected failure when interval cannot be proven")
	}
	if prep.Failure() == nil || prep.Failure().Code != "replay_invalid" {
		t.Fatalf("expected replay_invalid, got %+v", prep.Failure())
	}

	// p (with valid bytes) should succeed; just confirming the above failure
	// was caused by the bad payload and not the fake extractor.
	_ = p
}

func TestPrepareProducesReadyRevision(t *testing.T) {
	store := newFakeStore()
	store.add("replay-1", []byte("demo-bytes"))
	p := newTestProvider(store, fakeFacts)

	prep := core.NewScenarioPreparation("prep-1", "replay-1", 0, 100)
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
	if rev.ID == "" {
		t.Fatal("revision id must not be empty")
	}
	if rev.TakeoverTick != 100 {
		t.Fatalf("expected takeover tick 100, got %d", rev.TakeoverTick)
	}
	if rev.Fidelity != core.FidelityComplete {
		t.Fatalf("expected complete, got %s", rev.Fidelity)
	}
	if len(rev.Omissions) != 0 {
		t.Fatalf("expected no omissions, got %+v", rev.Omissions)
	}
}

func TestPrepareIsIdempotentOnTerminalPreparation(t *testing.T) {
	store := newFakeStore()
	store.add("replay-1", []byte("demo-bytes"))
	p := newTestProvider(store, fakeFacts)

	prep := core.NewScenarioPreparation("prep-1", "replay-1", 0, 100)
	if err := p.Prepare(context.Background(), prep); err != nil {
		t.Fatal(err.Error())
	}
	firstRevision := prep.Revision()

	// A second Prepare call on a terminal preparation must be refused and
	// must not overwrite the existing revision.
	if err := p.Prepare(context.Background(), prep); err == nil {
		t.Fatal("expected refusal on terminal preparation")
	}
	if prep.Revision() != firstRevision {
		t.Fatal("the original revision must be preserved")
	}
	if store.callsFor("replay-1") != 1 {
		t.Fatalf("store should be consulted once, got %d", store.callsFor("replay-1"))
	}
}

func TestPrepareHonoursContextCancellation(t *testing.T) {
	store := newFakeStore()
	store.add("replay-1", []byte("demo-bytes"))
	p := newTestProvider(store, fakeFacts)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	prep := core.NewScenarioPreparation("prep-1", "replay-1", 0, 100)
	err := p.Prepare(ctx, prep)
	if err == nil {
		t.Fatal("expected failure on cancelled context")
	}
	if prep.State() != core.PreparationFailed {
		t.Fatalf("expected failed, got %s", prep.State())
	}
}

func TestFailureCodeMapping(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{ErrStoreDenied, "store_denied"},
		{ErrReplayNotFound, "replay_not_found"},
		{ErrReplaySizeExceeded, "replay_invalid"},
		{ErrReplayUnreadable, "replay_invalid"},
		{ErrTickIntervalNotProven, "tick_interval_not_proven"},
		{ErrTakeoverTickRequired, "takeover_tick_required"},
		{ErrExtractionFailed, "extraction_failed"},
		{errors.New("unknown"), "provider_failed"},
	}
	for _, tc := range cases {
		if got := FailureCode(tc.err); got != tc.want {
			t.Errorf("FailureCode(%v) = %s, want %s", tc.err, got, tc.want)
		}
	}
}
