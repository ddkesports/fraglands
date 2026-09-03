package provider

import (
	"context"
	"testing"

	"github.com/paralin/fraglands/core"
	"github.com/paralin/fraglands/orchestrator"
	"github.com/paralin/s2replay/analysis"
)

// The adapter must satisfy the orchestrator.Preparer contract.
var _ orchestrator.Preparer = (*Preparer)(nil)

func TestPreparerAdapterNilProvider(t *testing.T) {
	if _, err := NewPreparer(nil); err != ErrNilProvider {
		t.Fatalf("expected ErrNilProvider, got %v", err)
	}
}

// The ready path: the provider compiles a revision and the adapter hands
// the orchestrator a ready preparation.
func TestPreparerAdapterReady(t *testing.T) {
	store := newFakeStore()
	store.add("replay-1", []byte("demo-bytes"))
	p := newTestProvider(store, fakeFacts)
	a, err := NewPreparer(p)
	if err != nil {
		t.Fatal(err.Error())
	}

	prep := newAuthorizedPreparation(newTestAuthority(), "prep-1", "replay-1", 100)
	a.Prepare(context.Background(), prep)

	if prep.State() != core.PreparationReady {
		t.Fatalf("expected ready, got %s", prep.State())
	}
	rev := prep.Revision()
	if rev == nil {
		t.Fatal("expected a revision on the ready preparation")
	}
	if rev.ID == "" {
		t.Fatal("expected a non-empty revision id")
	}
	if prep.Failure() != nil {
		t.Fatalf("expected no failure, got %+v", prep.Failure())
	}
}

// A store refusal is recorded on the preparation as one typed reason by the
// provider itself; the adapter adds nothing and overwrites nothing.
func TestPreparerAdapterTypedFailure(t *testing.T) {
	store := newFakeStore()
	store.failOn = "replay-1"
	store.failWith = ErrReplayNotFound
	p := newTestProvider(store, fakeFacts)
	a, err := NewPreparer(p)
	if err != nil {
		t.Fatal(err.Error())
	}

	prep := newAuthorizedPreparation(newTestAuthority(), "prep-1", "replay-1", 100)
	a.Prepare(context.Background(), prep)

	if prep.State() != core.PreparationFailed {
		t.Fatalf("expected failed, got %s", prep.State())
	}
	failure := prep.Failure()
	if failure == nil {
		t.Fatal("expected a typed failure")
	}
	if failure.Code != "store_denied" {
		t.Fatalf("expected store_denied, got %s", failure.Code)
	}
	if prep.Revision() != nil {
		t.Fatal("no revision must be attached on failure")
	}
}

// A compilation refusal fails the preparation closed with the compiler's
// own typed reason.
func TestPreparerAdapterCompileRefusal(t *testing.T) {
	store := newFakeStore()
	store.add("replay-1", []byte("demo-bytes"))
	// The fake facts report a zero tick: the compiler refuses.
	refusingFacts := func(demo []byte, req analysis.RunbackRequest) (analysis.RunbackFacts, error) {
		facts, err := fakeFacts(demo, req)
		if err != nil {
			return analysis.RunbackFacts{}, err
		}
		facts.Tick = 0
		return facts, nil
	}
	p := newTestProvider(store, refusingFacts)
	a, err := NewPreparer(p)
	if err != nil {
		t.Fatal(err.Error())
	}

	prep := newAuthorizedPreparation(newTestAuthority(), "prep-1", "replay-1", 100)
	a.Prepare(context.Background(), prep)

	if prep.State() != core.PreparationFailed {
		t.Fatalf("expected failed, got %s", prep.State())
	}
	if failure := prep.Failure(); failure == nil || failure.Code != "compile_refused" {
		t.Fatalf("expected compile_refused, got %+v", prep.Failure())
	}
}

// A preparation already in a terminal state is refused by the provider; the
// adapter must not overwrite the terminal outcome.
func TestPreparerAdapterRefusesTerminalPreparation(t *testing.T) {
	store := newFakeStore()
	store.add("replay-1", []byte("demo-bytes"))
	p := newTestProvider(store, fakeFacts)
	a, err := NewPreparer(p)
	if err != nil {
		t.Fatal(err.Error())
	}

	prep := newAuthorizedPreparation(newTestAuthority(), "prep-1", "replay-1", 100)
	if err := prep.MarkFailed(&core.FailureReason{Code: "original", Message: "original failure"}); err != nil {
		t.Fatal(err.Error())
	}

	a.Prepare(context.Background(), prep)

	if prep.State() != core.PreparationFailed {
		t.Fatalf("expected failed, got %s", prep.State())
	}
	if failure := prep.Failure(); failure == nil || failure.Code != "original" {
		t.Fatalf("the original failure must be preserved, got %+v", prep.Failure())
	}
	if len(store.calls) != 0 {
		t.Fatal("the store must not be consulted for a terminal preparation")
	}
}

// A cancelled context fails the preparation through the provider's own
// path: the adapter drops the diagnostic error and the typed reason lands
// on the preparation.
func TestPreparerAdapterCancelledContext(t *testing.T) {
	store := newFakeStore()
	store.add("replay-1", []byte("demo-bytes"))
	p := newTestProvider(store, fakeFacts)
	a, err := NewPreparer(p)
	if err != nil {
		t.Fatal(err.Error())
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	prep := newAuthorizedPreparation(newTestAuthority(), "prep-1", "replay-1", 100)
	a.Prepare(ctx, prep)

	if prep.State() != core.PreparationFailed {
		t.Fatalf("expected failed, got %s", prep.State())
	}
	if failure := prep.Failure(); failure == nil {
		t.Fatal("expected a typed failure on cancellation")
	}
}
