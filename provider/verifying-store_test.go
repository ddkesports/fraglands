package provider

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/paralin/fraglands/core"
)

// The verifying decorator must refuse every request before the store is
// consulted, so the inner store records nothing.
func TestVerifyingStoreRequiresAuthority(t *testing.T) {
	if _, err := NewVerifyingStore(nil, newFakeStore()); err != ErrGrantAuthorityRequired {
		t.Fatalf("expected ErrGrantAuthorityRequired, got %v", err)
	}
	a := newTestAuthority()
	if _, err := NewVerifyingStore(a, nil); err != ErrStoreRequired {
		t.Fatalf("expected ErrStoreRequired, got %v", err)
	}
}

func TestVerifyingStoreRefusesBeforeStoreCall(t *testing.T) {
	store := newFakeStore()
	store.add("replay-1", []byte("demo-bytes"))
	a := newTestAuthority()
	vs, err := NewVerifyingStore(a, store)
	if err != nil {
		t.Fatal(err)
	}

	// Empty grant: refused, no store call.
	if _, err := vs.Replay(context.Background(), core.ReplayRequest{PreparationID: "prep-1", ReplayID: "replay-1"}); err != core.ErrGrantUnknown {
		t.Fatalf("expected ErrGrantUnknown, got %v", err)
	}
	if len(store.calls) != 0 {
		t.Fatalf("store must not be called without a valid grant, got %d calls", len(store.calls))
	}

	// Wrong grant: refused, no store call.
	if _, err := vs.Replay(context.Background(), core.ReplayRequest{PreparationID: "prep-1", ReplayID: "replay-1", Grant: "bogus"}); err != core.ErrGrantUnknown {
		t.Fatalf("expected ErrGrantUnknown, got %v", err)
	}
	if len(store.calls) != 0 {
		t.Fatalf("store must not be called with an invalid grant, got %d calls", len(store.calls))
	}

	// A valid grant is consumed exactly once: the second request is refused
	// without reaching the store.
	g, err := a.Mint("prep-1", "acct-a", "replay-1")
	if err != nil {
		t.Fatal(err)
	}
	req := core.ReplayRequest{PreparationID: "prep-1", ReplayID: "replay-1", Grant: g.Token()}
	rc, err := vs.Replay(context.Background(), req)
	if err != nil {
		t.Fatalf("first authorized request should pass: %v", err)
	}
	rc.Close()
	if _, err := vs.Replay(context.Background(), req); err != core.ErrGrantAlreadyUsed {
		t.Fatalf("expected ErrGrantAlreadyUsed on reuse, got %v", err)
	}
	if len(store.calls) != 1 {
		t.Fatalf("store should have been called exactly once, got %d", len(store.calls))
	}
}

func TestVerifyingStoreBindsPreparationAndReplay(t *testing.T) {
	store := newFakeStore()
	store.add("replay-1", []byte("demo-bytes"))
	store.add("replay-2", []byte("other-bytes"))
	a := newTestAuthority()
	vs, err := NewVerifyingStore(a, store)
	if err != nil {
		t.Fatal(err)
	}

	// A grant minted for prep-1/replay-1 cannot authorize prep-2/replay-1.
	g, err := a.Mint("prep-1", "acct-a", "replay-1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = vs.Replay(context.Background(), core.ReplayRequest{PreparationID: "prep-2", ReplayID: "replay-1", Grant: g.Token()})
	if err != core.ErrGrantMismatch {
		t.Fatalf("expected ErrGrantMismatch for preparation substitution, got %v", err)
	}
	_, err = vs.Replay(context.Background(), core.ReplayRequest{PreparationID: "prep-1", ReplayID: "replay-2", Grant: g.Token()})
	if err != core.ErrGrantMismatch {
		t.Fatalf("expected ErrGrantMismatch for replay substitution, got %v", err)
	}
	if len(store.calls) != 0 {
		t.Fatalf("store must never see a substituted request, got %d calls", len(store.calls))
	}
}

func TestVerifyingStoreExpiry(t *testing.T) {
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
	g, err := a.Mint("prep-1", "acct-a", "replay-1")
	if err != nil {
		t.Fatal(err)
	}
	req := core.ReplayRequest{PreparationID: "prep-1", ReplayID: "replay-1", Grant: g.Token()}

	now = g.ExpiresAt().Add(-time.Second)
	if _, err := vs.Replay(context.Background(), req); err != nil {
		t.Fatalf("request before expiry should pass: %v", err)
	}
	// The one-use consume already happened; a second request is reuse, not
	// expiry. A fresh grant at expiry is refused as expired.
	g2, err := a.Mint("prep-2", "acct-a", "replay-1")
	if err != nil {
		t.Fatal(err)
	}
	now = g2.ExpiresAt()
	if _, err := vs.Replay(context.Background(), core.ReplayRequest{PreparationID: "prep-2", ReplayID: "replay-1", Grant: g2.Token()}); err != core.ErrGrantExpired {
		t.Fatalf("expected ErrGrantExpired at expiry, got %v", err)
	}
	if len(store.calls) != 1 {
		t.Fatalf("store should have been called exactly once, got %d", len(store.calls))
	}
}

func TestVerifyingStoreRevoked(t *testing.T) {
	store := newFakeStore()
	store.add("replay-1", []byte("demo-bytes"))
	a := newTestAuthority()
	vs, err := NewVerifyingStore(a, store)
	if err != nil {
		t.Fatal(err)
	}
	g, err := a.Mint("prep-1", "acct-a", "replay-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Revoke("prep-1"); err != nil {
		t.Fatal(err)
	}
	_, err = vs.Replay(context.Background(), core.ReplayRequest{PreparationID: "prep-1", ReplayID: "replay-1", Grant: g.Token()})
	if err != core.ErrGrantRevoked {
		t.Fatalf("expected ErrGrantRevoked, got %v", err)
	}
	if len(store.calls) != 0 {
		t.Fatalf("store must not be called for a revoked grant, got %d calls", len(store.calls))
	}
}

func TestProviderFailsClosedWithoutGrant(t *testing.T) {
	store := newFakeStore()
	store.add("replay-1", []byte("demo-bytes"))
	p := newTestProvider(store, fakeFacts)
	// A preparation without a grant (nil) is refused before any store call.
	prep := core.NewScenarioPreparation("prep-1", "replay-1", 0, 100, nil)
	err := p.Prepare(context.Background(), prep)
	if err == nil {
		t.Fatal("expected failure without a grant")
	}
	if prep.Failure() == nil || prep.Failure().Code != "grant_required" {
		t.Fatalf("expected grant_required, got %+v", prep.Failure())
	}
	if len(store.calls) != 0 {
		t.Fatalf("store must not be called without a grant, got %d calls", len(store.calls))
	}
}

func TestProviderGrantRefusalIsTyped(t *testing.T) {
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

	// The provider wires its own verifying store; a raw store wrapped here
	// would bypass verification, so New must be given the decorator.
	_ = p

	// Expired grant: typed refusal on the preparation.
	now := time.Unix(1_700_000_000, 0)
	a2, err := core.NewHMACGrantAuthority(core.GrantAuthorityConfig{
		Clock: func() time.Time { return now },
		TTL:   time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	vs2, err := NewVerifyingStore(a2, store)
	if err != nil {
		t.Fatal(err)
	}
	g, err := a2.Mint("prep-9", "acct-a", "replay-1")
	if err != nil {
		t.Fatal(err)
	}
	now = g.ExpiresAt().Add(time.Minute)
	p2, err := New(a2, vs2, fakeFacts, 5)
	if err != nil {
		t.Fatal(err)
	}
	p2.prober = fakeProber
	prep := core.NewScenarioPreparation("prep-9", "replay-1", 0, 100, g)
	if err := p2.Prepare(context.Background(), prep); err == nil {
		t.Fatal("expected expiry failure")
	}
	if prep.Failure() == nil || prep.Failure().Code != "grant_expired" {
		t.Fatalf("expected grant_expired, got %+v", prep.Failure())
	}
	if len(store.calls) != 0 {
		t.Fatalf("store must not be called for an expired grant, got %d calls", len(store.calls))
	}
	_ = io.Discard
	_ = errors.New
}
