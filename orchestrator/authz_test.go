package orchestrator

import (
	"testing"

	"github.com/paralin/fraglands/core"
)

func TestPreparationViewAuthorization(t *testing.T) {
	o, id, owner, other := setupReady(t)

	// The owner views status.
	if _, err := o.Preparation(owner, id); err != nil {
		t.Fatal(err.Error())
	}

	// A stranger is refused before any process fact is exposed.
	if _, err := o.Preparation(other, id); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}

	// A stranger cannot claim: no invitation, no slot, no view.
	if _, err := o.Claim(other, id); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden for stranger claim, got %v", err)
	}
	if _, err := o.Preparation(other, id); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}

	// The owner invites the other account with an opaque authorization.
	invite, err := o.Invite(owner, id, other.ID)
	if err != nil {
		t.Fatal(err.Error())
	}
	if invite.Token == "" {
		t.Fatal("expected a non-empty opaque invite token")
	}

	// The invited participant claims with the opaque token.
	if _, err := o.ClaimAuthorized(other, id, invite.Token); err != nil {
		t.Fatal(err.Error())
	}
	if _, err := o.Preparation(other, id); err != nil {
		t.Fatal(err.Error())
	}
}

func TestReleaseAuthorization(t *testing.T) {
	o, id, owner, other := setupReady(t)

	if _, err := o.Claim(owner, id); err != nil {
		t.Fatal(err.Error())
	}

	// A stranger cannot release the victim slot: refused, and the slot
	// stays reserved.
	if err := o.Release(other, id); err != ErrForbidden {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
	status, err := o.Preparation(owner, id)
	if err != nil {
		t.Fatal(err.Error())
	}
	if status.Lobby.Occupied() != 1 {
		t.Fatalf("expected victim slot to survive, got %d occupied", status.Lobby.Occupied())
	}

	// The owner releases their own slot.
	if err := o.Release(owner, id); err != nil {
		t.Fatal(err.Error())
	}

	// The owner with no slot: authorized, nothing to release.
	if err := o.Release(owner, id); err != core.ErrNoSlotClaimed {
		t.Fatalf("expected ErrNoSlotClaimed, got %v", err)
	}
}

func TestUnauthenticatedRefused(t *testing.T) {
	o, id, _, _ := setupReady(t)

	if _, err := o.Prepare(nil, "replay-1", 0, 63280); err != ErrUnauthenticated {
		t.Fatalf("expected ErrUnauthenticated, got %v", err)
	}
	if _, err := o.Preparation(nil, id); err != ErrUnauthenticated {
		t.Fatalf("expected ErrUnauthenticated, got %v", err)
	}
	if _, err := o.Claim(nil, id); err != ErrUnauthenticated {
		t.Fatalf("expected ErrUnauthenticated, got %v", err)
	}
	if err := o.Release(nil, id); err != ErrUnauthenticated {
		t.Fatalf("expected ErrUnauthenticated, got %v", err)
	}
	if _, err := o.IssueJoinIntent(nil, id); err != ErrUnauthenticated {
		t.Fatalf("expected ErrUnauthenticated, got %v", err)
	}
	if _, err := o.Result(nil, 1, 1); err != ErrUnauthenticated {
		t.Fatalf("expected ErrUnauthenticated, got %v", err)
	}
}
