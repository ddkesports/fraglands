package provider

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/paralin/fraglands/core"
)

// End-to-end: a provider wired with its own verifying store fetches bytes
// only for a preparation holding a valid, unrevoked, unexpired, unused
// grant, and the fetch is one-use.
func TestProviderAuthorizedFetchOneUse(t *testing.T) {
	store := newFakeStore()
	store.add("replay-1", []byte("demo-bytes"))
	a := newTestAuthority()
	vs, err := NewVerifyingStore(a, store)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(a, vs, fakeFacts, 5)
	if err != nil {
		t.Fatal(err)
	}
	p.prober = fakeProber

	prep := newAuthorizedPreparation(a, "prep-1", "replay-1", 100)
	if err := p.Prepare(context.Background(), prep); err != nil {
		t.Fatalf("authorized preparation should succeed: %v", err)
	}
	if prep.State() != core.PreparationReady {
		t.Fatalf("expected ready, got %s", prep.State())
	}
	if len(store.calls) != 1 {
		t.Fatalf("store called %d times, want 1", len(store.calls))
	}

	// A second preparation with a fresh grant fetches independently.
	prep2 := newAuthorizedPreparation(a, "prep-2", "replay-1", 100)
	if err := p.Prepare(context.Background(), prep2); err != nil {
		t.Fatalf("second authorized preparation should succeed: %v", err)
	}
	if len(store.calls) != 2 {
		t.Fatalf("store called %d times, want 2", len(store.calls))
	}
}

// Concurrent provider runs on preparations sharing one grant (an orchestrator
// bug) must yield exactly one successful fetch.
func TestProviderConcurrentOneGrantExactlyOneFetch(t *testing.T) {
	store := newFakeStore()
	store.add("replay-1", []byte("demo-bytes"))
	a := newTestAuthority()
	vs, err := NewVerifyingStore(a, store)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(a, vs, fakeFacts, 5)
	if err != nil {
		t.Fatal(err)
	}
	p.prober = fakeProber

	grant, err := a.Mint("prep-shared", "acct-a", "replay-1")
	if err != nil {
		t.Fatal(err)
	}
	const n = 8
	preps := make([]*core.ScenarioPreparation, n)
	for i := range preps {
		// All preparations present the same grant: only one may win.
		preps[i] = core.NewScenarioPreparation("prep-shared", "replay-1", 0, 100, grant)
	}
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = p.Prepare(context.Background(), preps[i])
		}(i)
	}
	wg.Wait()

	success := 0
	for i, err := range errs {
		if err == nil {
			success++
			continue
		}
		code := ""
		if preps[i].Failure() != nil {
			code = preps[i].Failure().Code
		}
		if code != "grant_already_used" {
			t.Fatalf("expected grant_already_used, got %q (%v)", code, err)
		}
	}
	if success != 1 {
		t.Fatalf("expected exactly one successful fetch, got %d", success)
	}
	if len(store.calls) != 1 {
		t.Fatalf("store called %d times, want 1", len(store.calls))
	}
}

// The provider surfaces expiry with the typed reason under an injected
// clock, and never calls the store.
func TestProviderExpiryInjectedClock(t *testing.T) {
	store := newFakeStore()
	store.add("replay-1", []byte("demo-bytes"))
	now := time.Unix(1_700_000_000, 0)
	a, err := core.NewHMACGrantAuthority(core.GrantAuthorityConfig{
		Clock: func() time.Time { return now },
		TTL:   time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	vs, err := NewVerifyingStore(a, store)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(a, vs, fakeFacts, 5)
	if err != nil {
		t.Fatal(err)
	}
	p.prober = fakeProber

	grant, err := a.Mint("prep-1", "acct-a", "replay-1")
	if err != nil {
		t.Fatal(err)
	}
	prep := core.NewScenarioPreparation("prep-1", "replay-1", 0, 100, grant)

	// One tick before expiry: success.
	now = grant.ExpiresAt().Add(-time.Millisecond)
	if err := p.Prepare(context.Background(), prep); err != nil {
		t.Fatalf("fetch before expiry should succeed: %v", err)
	}

	// A fresh preparation whose grant expires before use: typed refusal,
	// no store call.
	grant2, err := a.Mint("prep-2", "acct-a", "replay-1")
	if err != nil {
		t.Fatal(err)
	}
	prep2 := core.NewScenarioPreparation("prep-2", "replay-1", 0, 100, grant2)
	now = grant2.ExpiresAt()
	if err := p.Prepare(context.Background(), prep2); err == nil {
		t.Fatal("expected expiry failure")
	}
	if prep2.Failure() == nil || prep2.Failure().Code != "grant_expired" {
		t.Fatalf("expected grant_expired, got %+v", prep2.Failure())
	}
	if len(store.calls) != 1 {
		t.Fatalf("store called %d times, want 1", len(store.calls))
	}
}
