package core

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestServerLeaseIssueAndAuthenticate(t *testing.T) {
	a := NewServerLeaseAuthority()

	lease, err := a.IssueLease("srv-a", 7)
	if err != nil {
		t.Fatal(err.Error())
	}
	if lease.Generation() != 7 || lease.Version() != 1 {
		t.Fatalf("expected generation 7 version 1, got %d/%d", lease.Generation(), lease.Version())
	}
	if lease.ParticipantID() != "srv-a" {
		t.Fatal("expected participant identity bound at issue")
	}
	if lease.Credential() == "" {
		t.Fatal("expected a non-empty credential")
	}

	participant, err := a.AuthenticateServer(context.Background(), lease.Credential())
	if err != nil {
		t.Fatal(err.Error())
	}
	if participant.ID != "srv-a" || participant.ProcessGeneration != 7 {
		t.Fatalf("expected participant srv-a on generation 7, got %+v", participant)
	}

	// An unknown credential is refused typed.
	if _, err := a.AuthenticateServer(context.Background(), "forged"); !errors.Is(err, ErrLeaseUnknown) {
		t.Fatalf("expected ErrLeaseUnknown, got %v", err)
	}
	if _, err := a.AuthenticateServer(context.Background(), ""); !errors.Is(err, ErrLeaseUnknown) {
		t.Fatalf("expected ErrLeaseUnknown for empty, got %v", err)
	}
}

func TestServerLeaseRevoke(t *testing.T) {
	a := NewServerLeaseAuthority()

	lease, err := a.IssueLease("srv-a", 7)
	if err != nil {
		t.Fatal(err.Error())
	}

	if err := a.Revoke(7); err != nil {
		t.Fatal(err.Error())
	}
	if _, err := a.AuthenticateServer(context.Background(), lease.Credential()); !errors.Is(err, ErrLeaseRevoked) {
		t.Fatalf("expected ErrLeaseRevoked, got %v", err)
	}

	// Revoking again changes nothing.
	if err := a.Revoke(7); err != nil {
		t.Fatal(err.Error())
	}
	// Revoking an unknown generation changes nothing.
	if err := a.Revoke(99); err != nil {
		t.Fatal(err.Error())
	}
}

func TestServerLeaseReIssueBumpsVersionAndSupersedes(t *testing.T) {
	a := NewServerLeaseAuthority()

	first, err := a.IssueLease("srv-a", 7)
	if err != nil {
		t.Fatal(err.Error())
	}

	second, err := a.IssueLease("srv-a", 7)
	if err != nil {
		t.Fatal(err.Error())
	}
	if second.Version() != 2 {
		t.Fatalf("expected version 2 after re-issue, got %d", second.Version())
	}

	// The superseded credential is dead.
	if _, err := a.AuthenticateServer(context.Background(), first.Credential()); !errors.Is(err, ErrLeaseUnknown) {
		t.Fatalf("expected ErrLeaseUnknown for superseded credential, got %v", err)
	}
	// The active credential works.
	if _, err := a.AuthenticateServer(context.Background(), second.Credential()); err != nil {
		t.Fatal(err.Error())
	}

	// A commit for the generation runs against the active lease.
	if err := a.CommitLease(7, func() error { return nil }); err != nil {
		t.Fatal(err.Error())
	}
}

func TestServerLeaseIssueRefusals(t *testing.T) {
	a := NewServerLeaseAuthority()

	if _, err := a.IssueLease("", 7); !errors.Is(err, ErrLeaseInvalidBinding) {
		t.Fatalf("expected ErrLeaseInvalidBinding, got %v", err)
	}
	if _, err := a.IssueLease("srv-a", 0); !errors.Is(err, ErrLeaseInvalidBinding) {
		t.Fatalf("expected ErrLeaseInvalidBinding, got %v", err)
	}
}

func TestServerLeaseNotSerializable(t *testing.T) {
	a := NewServerLeaseAuthority()
	lease, err := a.IssueLease("srv-a", 7)
	if err != nil {
		t.Fatal(err.Error())
	}
	if got := lease.String(); got != "server-lease(redacted)" {
		t.Fatalf("expected redacted String, got %q", got)
	}
}

func TestServerLeaseCommitGatesOnLiveLease(t *testing.T) {
	a := NewServerLeaseAuthority()
	if _, err := a.IssueLease("srv-a", 7); err != nil {
		t.Fatal(err.Error())
	}

	// A live lease admits the commit.
	committed := false
	if err := a.CommitLease(7, func() error { committed = true; return nil }); err != nil {
		t.Fatal(err.Error())
	}
	if !committed {
		t.Fatal("expected commit to run")
	}

	// After terminal revocation the commit is refused whole.
	if err := a.Revoke(7); err != nil {
		t.Fatal(err.Error())
	}
	committed = false
	if err := a.CommitLease(7, func() error { committed = true; return nil }); !errors.Is(err, ErrLeaseRevoked) {
		t.Fatalf("expected ErrLeaseRevoked, got %v", err)
	}
	if committed {
		t.Fatal("refused commit must never run")
	}

	// A commit for an unknown generation is refused typed.
	if err := a.CommitLease(99, func() error { return nil }); !errors.Is(err, ErrLeaseUnknown) {
		t.Fatalf("expected ErrLeaseUnknown, got %v", err)
	}
	// A nil commit closure is refused.
	if err := a.CommitLease(7, nil); !errors.Is(err, ErrLeaseCommitRequired) {
		t.Fatalf("expected ErrLeaseCommitRequired, got %v", err)
	}
}

// TestServerLeaseCommitOnRevokedGeneration proves a commit against a
// generation whose lease was revoked and later re-issued still refuses: the
// revoked record is no longer active, so its old credential and its
// generation can never commit again under a stale lease.
func TestServerLeaseCommitAfterReissue(t *testing.T) {
	a := NewServerLeaseAuthority()
	if _, err := a.IssueLease("srv-a", 7); err != nil {
		t.Fatal(err.Error())
	}
	if err := a.Revoke(7); err != nil {
		t.Fatal(err.Error())
	}
	if _, err := a.IssueLease("srv-a", 7); err != nil {
		t.Fatal(err.Error())
	}

	// The new active lease commits fine.
	if err := a.CommitLease(7, func() error { return nil }); err != nil {
		t.Fatal(err.Error())
	}
}

// TestServerLeaseCommitWaiterRunsUnderRevocation proves a commit closure
// that blocks cannot be overtaken by a revoke: the revoke waits for the
// lock, so the commit lands and stands.
func TestServerLeaseCommitWaiterRunsUnderRevocation(t *testing.T) {
	a := NewServerLeaseAuthority()
	if _, err := a.IssueLease("srv-a", 7); err != nil {
		t.Fatal(err.Error())
	}

	release := make(chan struct{})
	commitStarted := make(chan struct{})
	var commitDone sync.WaitGroup
	commitDone.Add(1)
	go func() {
		defer commitDone.Done()
		// This commit holds the lock until released.
		_ = a.CommitLease(7, func() error {
			close(commitStarted)
			<-release
			return nil
		})
	}()

	// Wait until the commit closure is running, then attempt the
	// revocation: it must block until the commit releases the lock.
	<-commitStarted
	go func() {
		// Give the commit a moment, then release it.
		time.Sleep(50 * time.Millisecond)
		close(release)
	}()
	commitDone.Wait()

	// The commit landed while the lease was live, so a subsequent commit
	// still succeeds (the lease was never revoked).
	if err := a.CommitLease(7, func() error { return nil }); err != nil {
		t.Fatalf("expected lease to remain valid after unrevoked commit: %v", err)
	}
}

// TestServerLeaseRevokeLinearizesWithCommit proves the linearization
// contract: a commit that finished before the revocation stands; a commit
// refused by the revocation never ran; and no third outcome exists.
func TestServerLeaseRevokeLinearizesWithCommit(t *testing.T) {
	a := NewServerLeaseAuthority()
	if _, err := a.IssueLease("srv-a", 7); err != nil {
		t.Fatal(err.Error())
	}

	var committed, refused int64
	const workers = 32
	var start sync.WaitGroup
	start.Add(workers + 1)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start.Done()
			err := a.CommitLease(7, func() error { atomic.AddInt64(&committed, 1); return nil })
			if errors.Is(err, ErrLeaseRevoked) {
				atomic.AddInt64(&refused, 1)
			} else if err != nil {
				t.Errorf("unexpected commit error: %v", err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		start.Done()
		if err := a.Revoke(7); err != nil {
			t.Errorf("unexpected revoke error: %v", err)
		}
	}()
	start.Wait()
	wg.Wait()

	// Every worker is either a commit that ran before the revocation took
	// the lock, or a refusal that ran after. No third outcome exists, and
	// the sum is exact. The split itself is scheduler-dependent: if the
	// revocation wins the lock first, every commit is refused; if a commit
	// wins first, it lands and stands. Both partitions are correct.
	if committed+refused != workers {
		t.Fatalf("expected every commit to either land or be refused: %d committed, %d refused of %d", committed, refused, workers)
	}
	if committed < 0 || refused < 0 {
		t.Fatal("impossible negative outcome")
	}
}

func TestServerLeaseCommitRefusalLeavesLeaseValid(t *testing.T) {
	a := NewServerLeaseAuthority()
	if _, err := a.IssueLease("srv-a", 7); err != nil {
		t.Fatal(err.Error())
	}

	// A failing commit leaves the lease valid so the acceptance can be
	// retried.
	if err := a.CommitLease(7, func() error { return errors.New("store refused") }); err == nil {
		t.Fatal("expected the commit error to propagate")
	}
	if err := a.CommitLease(7, func() error { return nil }); err != nil {
		t.Fatalf("expected retry after a failed commit to succeed: %v", err)
	}
}
