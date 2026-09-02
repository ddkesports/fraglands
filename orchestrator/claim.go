package orchestrator

import (
	"crypto/rand"
	"encoding/base64"
	"time"

	"github.com/paralin/fraglands/core"
)

// Claim reserves a slot in the preparation lobby for the authenticated
// principal. Only the preparation owner or a principal explicitly invited
// onto the preparation may claim: any other principal is refused with
// ErrForbidden and lobby state is untouched. This keeps the connect
// address, which claiming unlocks through canViewLocked, behind the same
// authorization boundary as every other read.
func (o *Orchestrator) Claim(principal *core.Account, prepID string) (int, error) {
	if principal == nil {
		return -1, ErrUnauthenticated
	}

	var (
		lobby      *core.Lobby
		authorized bool
	)
	o.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		l, ok := o.lobbies[prepID]
		if !ok {
			return
		}
		lobby = l
		authorized = o.canViewLocked(principal.ID, prepID)
	})
	if lobby == nil {
		return -1, ErrUnknownPreparation
	}
	if !authorized {
		return -1, ErrForbidden
	}
	return lobby.Claim(principal.ID)
}

// Release frees the slot held by the principal in the preparation lobby.
// Only the preparation owner or a current participant may release: any
// other principal is refused and lobby state is untouched.
func (o *Orchestrator) Release(principal *core.Account, prepID string) error {
	if principal == nil {
		return ErrUnauthenticated
	}

	var (
		lobby      *core.Lobby
		authorized bool
	)
	o.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		l, ok := o.lobbies[prepID]
		if !ok {
			return
		}
		lobby = l
		authorized = o.canViewLocked(principal.ID, prepID)
	})
	if lobby == nil {
		return ErrUnknownPreparation
	}
	if !authorized {
		return ErrForbidden
	}
	return lobby.Release(principal.ID)
}

// lobbyFor returns the lobby for one preparation, or nil.
func (o *Orchestrator) lobbyFor(prepID string) *core.Lobby {
	var lobby *core.Lobby
	o.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		lobby = o.lobbies[prepID]
	})
	return lobby
}

// inviteTTL is how long an unused invitation stays redeemable.
const inviteTTL = 1 * time.Hour

// Invitation is one opaque, single-use authorization for one account to
// claim a slot in one preparation's lobby. The token is cryptographically
// random: it grants nothing until the invited principal presents it, and a
// consumed or expired token authorizes nothing.
type Invitation struct {
	// Token is the opaque authorization token presented at claim time.
	Token string
	// PrepID is the preparation this invitation admits to.
	PrepID string
	// AccountID is the invited account; no other account may redeem it.
	AccountID string
	// expires is the redemption deadline.
	expires time.Time
	// used records single-use consumption.
	used bool
}

// Invite creates one opaque, single-use invitation for the named account to
// claim into the preparation's lobby. Only the preparation owner may invite.
// The token travels to the invitee out of band; the orchestrator never
// delivers it.
func (o *Orchestrator) Invite(principal *core.Account, prepID, accountID string) (*Invitation, error) {
	if principal == nil {
		return nil, ErrUnauthenticated
	}
	if accountID == "" {
		return nil, core.ErrInvalidAccount
	}

	var authorized bool
	o.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		authorized = o.owners[prepID] == principal.ID
	})
	if !authorized {
		return nil, ErrForbidden
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	invite := &Invitation{
		Token:     base64.RawURLEncoding.EncodeToString(buf),
		PrepID:    prepID,
		AccountID: accountID,
		expires:   time.Now().Add(inviteTTL),
	}
	o.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		o.invites[invite.Token] = invite
	})
	return invite, nil
}

// ClaimAuthorized reserves a lobby slot for the principal when the principal
// is the preparation owner or presents a valid, unexpired, single-use
// invitation bound to its account. A redeemed invitation is consumed even
// when the lobby claim itself fails, so a token can never be replayed.
func (o *Orchestrator) ClaimAuthorized(principal *core.Account, prepID, token string) (int, error) {
	if principal == nil {
		return -1, ErrUnauthenticated
	}

	// Owner path: no token needed.
	var (
		lobby      *core.Lobby
		authorized bool
	)
	o.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		l, ok := o.lobbies[prepID]
		if !ok {
			return
		}
		lobby = l
		authorized = o.owners[prepID] == principal.ID
	})
	if lobby == nil {
		return -1, ErrUnknownPreparation
	}
	if authorized {
		return lobby.Claim(principal.ID)
	}

	// Invited path: consume one matching invitation, then claim.
	invited := false
	o.bcast.HoldLock(func(broadcast func(), _ func() <-chan struct{}) {
		invite, ok := o.invites[token]
		if !ok || invite.used || invite.PrepID != prepID || invite.AccountID != principal.ID {
			return
		}
		if time.Now().After(invite.expires) {
			return
		}
		invite.used = true
		invited = true
		broadcast()
	})
	if !invited {
		return -1, ErrForbidden
	}
	return lobby.Claim(principal.ID)
}

// IsOwner reports whether the principal owns the preparation. The web
// surface uses this to expose owner-only actions such as inviting.
func (o *Orchestrator) IsOwner(principal *core.Account, prepID string) bool {
	if principal == nil {
		return false
	}
	var owner bool
	o.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		owner = o.owners[prepID] == principal.ID
	})
	return owner
}
