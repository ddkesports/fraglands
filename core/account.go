// Package core implements the minimal Fraglands path: account and Steam
// identity types, ReplaySource and MatchRecord stubs, ScenarioPreparation
// lifecycle, JoinIntent fencing, and private results.
//
// This slice deliberately excludes social, party, and public board features.
// It defines the contract types that the Runback reconstruction provider
// (modlock runback-world) fills in; it never interprets game state itself.
package core

// SteamID is a Steam community identity as a 64-bit account ID.
type SteamID uint64

// Account is one durable Fraglands account. It owns at most one bound Steam
// identity; a binding never moves between accounts.
type Account struct {
	// ID is the durable account identifier.
	ID string
	// SteamID is the bound Steam identity; zero when unbound.
	SteamID SteamID
	// DisplayName is the human-readable account name.
	DisplayName string
}

// BindSteamID binds one Steam identity to the account. An existing binding is
// immutable: rebinding to a different identity is refused.
func (a *Account) BindSteamID(id SteamID) error {
	if a.SteamID != 0 {
		if a.SteamID == id {
			return nil
		}
		return ErrSteamIDAlreadyBound
	}
	if id == 0 {
		return ErrInvalidSteamID
	}
	a.SteamID = id
	return nil
}
