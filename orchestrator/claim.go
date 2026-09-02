package orchestrator

import "github.com/paralin/fraglands/core"

// Claim reserves a slot in the preparation lobby for the authenticated
// principal. The operation delegates to the core lobby, which owns the
// slot state.
func (o *Orchestrator) Claim(principal *core.Account, prepID string) (int, error) {
	if principal == nil {
		return -1, ErrUnauthenticated
	}
	lobby := o.lobbyFor(prepID)
	if lobby == nil {
		return -1, ErrUnknownPreparation
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
