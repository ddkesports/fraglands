package server

// ArtifactDelivery is the contract for delivering artifacts from a worker
// process to the supervisor. Implementations must be safe for concurrent use.
type ArtifactDelivery interface {
	// DeliverArtifact records one artifact delivered by the worker. It
	// returns an error if the process is not in a state that accepts
	// artifacts (Running or Ready).
	DeliverArtifact(processID string, artifact Artifact) error
}
