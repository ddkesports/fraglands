package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/paralin/fraglands/core"
)

// The orchestrator refuses to construct without a grant authority:
// authorization cannot be optional for a deployment.
func TestNewOrchestratorRequiresGrantAuthority(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sources := []core.ReplaySource{{ID: "replay-1"}}
	if _, err := NewOrchestrator(ctx, sources, &mockPreparer{}, &mockAllocator{}, testIdentityAuthority(), testServerAuthority(), nil); err != ErrGrantAuthorityRequired {
		t.Fatalf("expected ErrGrantAuthorityRequired, got %v", err)
	}
}

// The grant is minted only after the authenticated catalog check, and it is
// atomically bound to the preparation ID, owner account, and replay ID.
func TestPrepareMintsGrantAfterCatalogAcceptance(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	owner, other := testAccounts()
	sources := []core.ReplaySource{{ID: "replay-1", DisplayName: "One", FileName: "one.dem"}}
	o, _ := NewOrchestrator(ctx, sources, &mockPreparer{}, &mockAllocator{}, testIdentityAuthority(), testServerAuthority(), testGrantAuthority())

	id, err := o.Prepare(owner, "replay-1", 0, 63280)
	if err != nil {
		t.Fatal(err.Error())
	}

	prep := o.preparation(id)
	if prep == nil {
		t.Fatal("expected the preparation to exist")
	}
	req := prep.ReplayRequest()
	if req == nil {
		t.Fatal("expected a replay request carrying the private grant")
	}
	if req.PreparationID != id || req.ReplayID != "replay-1" {
		t.Fatalf("request not bound to the preparation and replay: %+v", req)
	}
	if req.Grant == "" {
		t.Fatal("expected a grant token on the request")
	}

	// A different account's preparation carries its own binding.
	otherID, err := o.Prepare(other, "replay-1", 0, 63280)
	if err != nil {
		t.Fatal(err.Error())
	}
	otherReq := o.preparation(otherID).ReplayRequest()
	if otherReq.Grant == req.Grant {
		t.Fatal("two preparations must never share a grant token")
	}
}

// A mint failure must orphan nothing: no preparation record, no lobby, no
// owner entry.
func TestPrepareMintFailureOrphansNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	owner, _ := testAccounts()
	sources := []core.ReplaySource{{ID: "replay-1"}}
	grants := newMockGrantAuthority()
	o, _ := NewOrchestrator(ctx, sources, &mockPreparer{}, &mockAllocator{}, testIdentityAuthority(), testServerAuthority(), grants)

	// Simulate an authority failure.
	grants.inner = failingGrantAuthority{}
	_, err := o.Prepare(owner, "replay-1", 0, 63280)
	if err == nil {
		t.Fatal("expected the mint failure to surface")
	}
	if o.preparation("prep-1") != nil {
		t.Fatal("no preparation record may exist after a mint failure")
	}
}

// failingGrantAuthority refuses every operation.
type failingGrantAuthority struct{}

func (f failingGrantAuthority) Mint(preparationID, ownerAccountID, replayID string) (*core.ReplayGrant, error) {
	return nil, errors.New("authority unavailable")
}

func (f failingGrantAuthority) Verify(req core.ReplayRequest) error {
	return errors.New("authority unavailable")
}

func (f failingGrantAuthority) Revoke(preparationID string) error {
	return errors.New("authority unavailable")
}

// Cancelling a preparation revokes its grant: the request can no longer be
// verified, and the refusal is typed.
func TestCancelPreparationRevokesGrant(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	owner, other := testAccounts()
	sources := []core.ReplaySource{{ID: "replay-1"}}
	grants := newMockGrantAuthority()
	o, _ := NewOrchestrator(ctx, sources, &mockPreparer{}, &mockAllocator{}, testIdentityAuthority(), testServerAuthority(), grants)

	id, err := o.Prepare(owner, "replay-1", 0, 63280)
	if err != nil {
		t.Fatal(err.Error())
	}
	prep := o.preparation(id)
	req := *prep.ReplayRequest()

	// Only the owner may cancel.
	if err := o.CancelPreparation(other, id); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden for a non-owner cancel, got %v", err)
	}
	if err := o.CancelPreparation(nil, id); err != ErrUnauthenticated {
		t.Fatalf("expected ErrUnauthenticated, got %v", err)
	}
	if err := o.CancelPreparation(owner, "prep-unknown"); err != ErrUnknownPreparation {
		t.Fatalf("expected ErrUnknownPreparation, got %v", err)
	}

	if err := o.CancelPreparation(owner, id); err != nil {
		t.Fatal(err.Error())
	}
	if prep.State() != core.PreparationCancelled {
		t.Fatalf("expected cancelled, got %s", prep.State())
	}
	if err := grants.inner.Verify(req); err != core.ErrGrantRevoked {
		t.Fatalf("expected ErrGrantRevoked after cancel, got %v", err)
	}
}

// A failed preparation revokes its grant: the replay can never be fetched
// for a preparation that will never run.
func TestFailedPreparationRevokesGrant(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	owner, _ := testAccounts()
	sources := []core.ReplaySource{{ID: "replay-1"}}
	grants := newMockGrantAuthority()
	o, _ := NewOrchestrator(ctx, sources, &mockPreparer{fail: true}, &mockAllocator{}, testIdentityAuthority(), testServerAuthority(), grants)

	id, err := o.Prepare(owner, "replay-1", 0, 63280)
	if err != nil {
		t.Fatal(err.Error())
	}
	prep := o.preparation(id)
	req := *prep.ReplayRequest()

	state, err := prep.WaitReady(ctx)
	if err != nil {
		t.Fatal(err.Error())
	}
	if state != core.PreparationFailed {
		t.Fatalf("expected failed, got %s", state)
	}
	if err := grants.inner.Verify(req); err != core.ErrGrantRevoked {
		t.Fatalf("expected ErrGrantRevoked after failure, got %v", err)
	}
}
