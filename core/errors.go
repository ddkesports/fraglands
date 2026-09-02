package core

import "errors"

var (
	// ErrSteamIDAlreadyBound is returned when a bound Steam identity would move
	// to another account.
	ErrSteamIDAlreadyBound = errors.New("core: steam identity already bound")
	// ErrInvalidSteamID is returned for a zero Steam identity.
	ErrInvalidSteamID = errors.New("core: invalid steam identity")
	// ErrRevisionMismatch is returned when a JoinIntent revision does not match
	// the prepared revision.
	ErrRevisionMismatch = errors.New("core: join intent revision mismatch")
	// ErrGenerationMismatch is returned when a JoinIntent generation does not
	// match the running server process generation.
	ErrGenerationMismatch = errors.New("core: join intent generation mismatch")
	// ErrIntentAlreadyUsed is returned when a one-use JoinIntent is consumed
	// twice.
	ErrIntentAlreadyUsed = errors.New("core: join intent already consumed")
	// ErrPreparationNotReady is returned when a join is attempted before the
	// preparation reaches the ready state.
	ErrPreparationNotReady = errors.New("core: preparation not ready")
	// ErrNoResult is returned when no private result exists for the request.
	ErrNoResult = errors.New("core: no private result")
	// ErrResultAlreadyAccepted is returned when a second result arrives for one
	// attempt generation.
	ErrResultAlreadyAccepted = errors.New("core: result already accepted for attempt")

	// ErrInvalidLobbyCapacity is returned when a lobby is constructed with a
	// non-positive slot capacity.
	ErrInvalidLobbyCapacity = errors.New("core: invalid lobby capacity")
	// ErrInvalidAccount is returned when an account identifier is empty.
	ErrInvalidAccount = errors.New("core: invalid account")
	// ErrLobbyFull is returned when a slot claim would exceed lobby capacity.
	ErrLobbyFull = errors.New("core: lobby full")
	// ErrNoSlotClaimed is returned when releasing a slot no account holds.
	ErrNoSlotClaimed = errors.New("core: no slot claimed by account")
)
