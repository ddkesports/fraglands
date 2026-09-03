package core

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func testClock(t *time.Time) func() time.Time {
	return func() time.Time { return *t }
}

func mustAuthority(t *testing.T, clock func() time.Time, ttl time.Duration) *HMACGrantAuthority {
	t.Helper()
	a, err := NewHMACGrantAuthority(GrantAuthorityConfig{Clock: clock, TTL: ttl})
	if err != nil {
		t.Fatalf("NewHMACGrantAuthority: %v", err)
	}
	return a
}

func TestGrantAuthorityRequiresClock(t *testing.T) {
	if _, err := NewHMACGrantAuthority(GrantAuthorityConfig{TTL: time.Minute}); err != ErrGrantClockRequired {
		t.Fatalf("expected ErrGrantClockRequired, got %v", err)
	}
}

func TestGrantAuthorityRequiresPositiveTTL(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	for _, ttl := range []time.Duration{0, -time.Minute} {
		if _, err := NewHMACGrantAuthority(GrantAuthorityConfig{Clock: testClock(&now), TTL: ttl}); err != ErrGrantTTLRequired {
			t.Fatalf("expected ErrGrantTTLRequired for ttl %v, got %v", ttl, err)
		}
	}
}

func TestGrantMintBindsAllFields(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	a := mustAuthority(t, testClock(&now), 15*time.Minute)
	g, err := a.Mint("prep-1", "acct-a", "replay-1")
	if err != nil {
		t.Fatal(err)
	}
	if g.PreparationID() != "prep-1" || g.OwnerAccountID() != "acct-a" || g.ReplayID() != "replay-1" {
		t.Fatalf("grant binding mismatch: %+v", g)
	}
	if !g.ExpiresAt().Equal(now.Add(15 * time.Minute)) {
		t.Fatalf("expiry not bound to configured TTL: %v", g.ExpiresAt())
	}
	if g.Token() == "" {
		t.Fatal("expected a non-empty opaque token")
	}
}

func TestGrantMintRequiresCompleteBinding(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	a := mustAuthority(t, testClock(&now), time.Minute)
	if _, err := a.Mint("", "acct-a", "replay-1"); err != ErrGrantInvalidBinding {
		t.Fatalf("expected ErrGrantInvalidBinding, got %v", err)
	}
	if _, err := a.Mint("prep-1", "", "replay-1"); err != ErrGrantInvalidBinding {
		t.Fatalf("expected ErrGrantInvalidBinding, got %v", err)
	}
	if _, err := a.Mint("prep-1", "acct-a", ""); err != ErrGrantInvalidBinding {
		t.Fatalf("expected ErrGrantInvalidBinding, got %v", err)
	}
}

func TestGrantOneUsePerPreparation(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	a := mustAuthority(t, testClock(&now), time.Minute)
	if _, err := a.Mint("prep-1", "acct-a", "replay-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Mint("prep-1", "acct-a", "replay-1"); err != ErrGrantAlreadyMinted {
		t.Fatalf("expected ErrGrantAlreadyMinted, got %v", err)
	}
}

func TestGrantVerifyConsumesOnce(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	a := mustAuthority(t, testClock(&now), time.Minute)
	g, err := a.Mint("prep-1", "acct-a", "replay-1")
	if err != nil {
		t.Fatal(err)
	}
	req := ReplayRequest{PreparationID: "prep-1", ReplayID: "replay-1", Grant: g.Token()}
	if err := a.Verify(req); err != nil {
		t.Fatalf("first verify should succeed: %v", err)
	}
	if err := a.Verify(req); err != ErrGrantAlreadyUsed {
		t.Fatalf("second verify should be ErrGrantAlreadyUsed, got %v", err)
	}
}

func TestGrantVerifyBindsPreparationAndReplay(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	a := mustAuthority(t, testClock(&now), time.Minute)
	g, err := a.Mint("prep-1", "acct-a", "replay-1")
	if err != nil {
		t.Fatal(err)
	}

	// Same replay, different preparation: refused.
	crossPrep := ReplayRequest{PreparationID: "prep-2", ReplayID: "replay-1", Grant: g.Token()}
	if err := a.Verify(crossPrep); err != ErrGrantMismatch {
		t.Fatalf("expected ErrGrantMismatch for cross-preparation substitution, got %v", err)
	}

	// Same preparation, different replay: refused.
	crossReplay := ReplayRequest{PreparationID: "prep-1", ReplayID: "replay-2", Grant: g.Token()}
	if err := a.Verify(crossReplay); err != ErrGrantMismatch {
		t.Fatalf("expected ErrGrantMismatch for cross-replay substitution, got %v", err)
	}

	// The refused verifications must not consume the grant.
	if err := a.Verify(ReplayRequest{PreparationID: "prep-1", ReplayID: "replay-1", Grant: g.Token()}); err != nil {
		t.Fatalf("grant should still be usable after refusals: %v", err)
	}
}

func TestGrantVerifyEmptyAndUnknown(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	a := mustAuthority(t, testClock(&now), time.Minute)
	if err := a.Verify(ReplayRequest{PreparationID: "prep-1", ReplayID: "replay-1"}); err != ErrGrantUnknown {
		t.Fatalf("expected ErrGrantUnknown for empty grant, got %v", err)
	}
	if err := a.Verify(ReplayRequest{PreparationID: "prep-1", ReplayID: "replay-1", Grant: "bogus"}); err != ErrGrantUnknown {
		t.Fatalf("expected ErrGrantUnknown for bogus grant, got %v", err)
	}
}

func TestGrantExpiryBoundaries(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	ttl := 5 * time.Minute
	a := mustAuthority(t, testClock(&now), ttl)
	g, err := a.Mint("prep-1", "acct-a", "replay-1")
	if err != nil {
		t.Fatal(err)
	}
	req := ReplayRequest{PreparationID: "prep-1", ReplayID: "replay-1", Grant: g.Token()}

	// Just before expiry: accepted.
	now = g.ExpiresAt().Add(-time.Second)
	if err := a.Verify(req); err != nil {
		t.Fatalf("verify one tick before expiry should succeed: %v", err)
	}

	// A fresh grant at exactly the expiry instant: refused (expiry is
	// exclusive) and typed as ErrGrantExpired.
	g2, err := a.Mint("prep-2", "acct-a", "replay-1")
	if err != nil {
		t.Fatal(err)
	}
	req2 := ReplayRequest{PreparationID: "prep-2", ReplayID: "replay-1", Grant: g2.Token()}
	now = g2.ExpiresAt()
	if err := a.Verify(req2); err != ErrGrantExpired {
		t.Fatalf("expected ErrGrantExpired at expiry instant, got %v", err)
	}

	// After expiry: refused.
	now = g2.ExpiresAt().Add(time.Minute)
	if err := a.Verify(req2); err != ErrGrantExpired {
		t.Fatalf("expected ErrGrantExpired after expiry, got %v", err)
	}
}

func TestGrantRevoke(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	a := mustAuthority(t, testClock(&now), time.Minute)
	g, err := a.Mint("prep-1", "acct-a", "replay-1")
	if err != nil {
		t.Fatal(err)
	}
	req := ReplayRequest{PreparationID: "prep-1", ReplayID: "replay-1", Grant: g.Token()}

	if err := a.Revoke("prep-1"); err != nil {
		t.Fatal(err)
	}
	if err := a.Verify(req); err != ErrGrantRevoked {
		t.Fatalf("expected ErrGrantRevoked, got %v", err)
	}

	// Revoking an unknown preparation or a consumed grant is a no-op.
	if err := a.Revoke("prep-unknown"); err != nil {
		t.Fatalf("revoking unknown preparation should be a no-op, got %v", err)
	}
}

func TestGrantConcurrentVerifyExactlyOne(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	a := mustAuthority(t, testClock(&now), time.Minute)
	g, err := a.Mint("prep-1", "acct-a", "replay-1")
	if err != nil {
		t.Fatal(err)
	}
	req := ReplayRequest{PreparationID: "prep-1", ReplayID: "replay-1", Grant: g.Token()}

	const n = 64
	errs := make([]error, n)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := a.Verify(req)
			mu.Lock()
			errs[i] = err
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	success := 0
	for _, err := range errs {
		if err == nil {
			success++
			continue
		}
		if err != ErrGrantAlreadyUsed {
			t.Fatalf("expected ErrGrantAlreadyUsed for losers, got %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("expected exactly one successful verify, got %d", success)
	}
}

func TestGrantNeverSerialized(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	a := mustAuthority(t, testClock(&now), time.Minute)
	g, err := a.Mint("prep-1", "acct-a", "replay-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := json.Marshal(g); err == nil {
		t.Fatal("ReplayGrant must refuse JSON serialization")
	}
	if _, err := json.Marshal(struct {
		Grant *ReplayGrant `json:"grant"`
	}{Grant: g}); err == nil {
		t.Fatal("ReplayGrant nested in a struct must refuse JSON serialization")
	}
	if got := g.String(); got == g.Token() || got == "" {
		t.Fatalf("String() must redact the token, got %q", got)
	}
}
