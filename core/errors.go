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
)
