package server

import "time"

// ReadinessFact is the explicit evidence that a server process is ready. A
// process is never assumed ready: the worker records a fact with provenance
// and the supervisor exposes it. Without a fact, the process stays in the
// Running state and every readiness-gated operation is refused.
type ReadinessFact struct {
	// Evidence is the concrete observation that proves readiness, e.g. the
	// exact log line the worker matched.
	Evidence string
	// RecordedAt is the moment the worker observed the evidence.
	RecordedAt time.Time
}
