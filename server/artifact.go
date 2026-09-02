package server

import "time"

// Artifact is one file or data blob delivered by the worker from a server
// process. Artifacts are always delivered explicitly by the worker; the
// supervisor never fabricates, infers, or auto-generates an artifact.
type Artifact struct {
	// Name is the artifact identifier within the process (e.g. "log.txt").
	Name string
	// Data is the artifact payload.
	Data []byte
	// DeliveredAt is the moment the worker delivered the artifact.
	DeliveredAt time.Time
}
