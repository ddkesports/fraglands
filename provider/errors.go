package provider

import "errors"

// Typed failures. Each maps to one stable reason code on the preparation and
// never carries partial state.
var (
	// ErrNilPreparation is returned when Prepare receives a nil preparation.
	ErrNilPreparation = errors.New("provider: nil preparation")
	// ErrStoreRequired is returned when the provider has no ReplayStore.
	ErrStoreRequired = errors.New("provider: no replay store configured")
	// ErrStoreDenied wraps a ReplayStore lookup failure. The store's own
	// error carries the authorization detail.
	ErrStoreDenied = errors.New("provider: replay store lookup failed")
	// ErrReplayNotFound is returned when the store cannot find the named
	// replay.
	ErrReplayNotFound = errors.New("provider: replay not found in store")
	// ErrReplayUnreadable wraps a read failure on the replay stream.
	ErrReplayUnreadable = errors.New("provider: replay stream failed")
	// ErrReplaySizeExceeded is returned when the replay exceeds the
	// declared size bound.
	ErrReplaySizeExceeded = errors.New("provider: replay exceeds maximum size")
	// ErrTickIntervalNotProven is returned when ServerInfo never provided
	// the exact tick interval.
	ErrTickIntervalNotProven = errors.New("provider: tick interval not proven from replay ServerInfo")
	// ErrTakeoverTickRequired is returned when the preparation carries no
	// takeover tick.
	ErrTakeoverTickRequired = errors.New("provider: takeover tick is zero")
	// ErrExtractionFailed wraps an s2replay extraction failure.
	ErrExtractionFailed = errors.New("provider: s2replay extraction failed")
	// ErrPreparationTerminal is returned when Prepare is called on a
	// preparation already in a terminal state.
	ErrPreparationTerminal = errors.New("provider: preparation already terminal")
)

// FailureCode maps a typed cause to the stable reason code carried on the
// preparation failure.
func FailureCode(err error) string {
	switch {
	case errors.Is(err, ErrStoreDenied):
		return "store_denied"
	case errors.Is(err, ErrReplayNotFound):
		return "replay_not_found"
	case errors.Is(err, ErrReplaySizeExceeded), errors.Is(err, ErrReplayUnreadable):
		return "replay_invalid"
	case errors.Is(err, ErrTickIntervalNotProven):
		return "tick_interval_not_proven"
	case errors.Is(err, ErrTakeoverTickRequired):
		return "takeover_tick_required"
	case errors.Is(err, ErrExtractionFailed):
		return "extraction_failed"
	case errors.Is(err, ErrStoreRequired):
		return "store_required"
	case errors.Is(err, ErrPreparationTerminal):
		return "preparation_terminal"
	default:
		return "provider_failed"
	}
}
