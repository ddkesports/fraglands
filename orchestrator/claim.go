package orchestrator

import "github.com/paralin/fraglands/core"

// Claim reserves a slot in the preparation lobby for the account. The
// operation delegates to the core lobby, which owns the slot state.
func (o *Orchestrator) Claim(prepID, accountID string) (int, error) {
	lobby := o.lobbyFor(prepID)
	if lobby == nil {
		return -1, ErrUnknownPreparation
	}
	return lobby.Claim(accountID)
}

// Release frees the slot held by the account in the preparation lobby.
func (o *Orchestrator) Release(prepID, accountID string) error {
	lobby := o.lobbyFor(prepID)
	if lobby == nil {
		return ErrUnknownPreparation
	}
	return lobby.Release(accountID)
}

// lobbyFor returns the lobby for one preparation, or nil.
func (o *Orchestrator) lobbyFor(prepID string) *core.Lobby {
	var lobby *core.Lobby
	o.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		lobby = o.lobbies[prepID]
	})
	return lobby
}
