package core

import "github.com/aperturerobotics/util/broadcast"

// JoinIntent is a one-use intent to join one prepared scenario revision on one
// server process generation. It is bound to both: a revision mismatch or a
// stale generation is refused. Consumption is one-use; the second consume
// fails.
type JoinIntent struct {
	// ID is the intent identifier.
	ID string
	// AccountID is the account the intent was issued to.
	AccountID string
	// SteamID is the Steam identity that must present at the server.
	SteamID SteamID
	// RevisionID is the immutable revision the intent admits to.
	RevisionID string
	// Generation is the server process generation the intent admits to.
	Generation uint64
	// used records one-use consumption under the intent owner lock.
	used bool
	// bcast guards used.
	bcast broadcast.Broadcast
}

// NewJoinIntent constructs an unused intent bound to one revision, generation,
// account, and Steam identity.
func NewJoinIntent(id, accountID string, steamID SteamID, revisionID string, generation uint64) *JoinIntent {
	return &JoinIntent{
		ID:         id,
		AccountID:  accountID,
		SteamID:    steamID,
		RevisionID: revisionID,
		Generation: generation,
	}
}

// Consume marks the intent used and verifies it against the presented
// revision, generation, and Steam identity. A consumed, mismatched, or
// unbound intent is refused and never marked used by a refused consume.
func (j *JoinIntent) Consume(revisionID string, generation uint64, steamID SteamID) error {
	var err error
	j.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		switch {
		case j.used:
			err = ErrIntentAlreadyUsed
		case j.RevisionID != revisionID:
			err = ErrRevisionMismatch
		case j.Generation != generation:
			err = ErrGenerationMismatch
		case j.SteamID != steamID:
			err = ErrSteamIDAlreadyBound
		default:
			j.used = true
		}
	})
	return err
}

// Used reports whether the intent was already consumed.
func (j *JoinIntent) Used() bool {
	var used bool
	j.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		used = j.used
	})
	return used
}
