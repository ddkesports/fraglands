package core

// ServerParticipant is one authenticated server process instance. It is
// derived from the process credential by the server authority and carries
// the process generation the instance serves. This is the authoritative
// identity type for server participants: the orchestrator authenticates
// against it and the server ingestion gate binds decoded artifacts to it,
// so no package copies the identity into a local shape.
type ServerParticipant struct {
	// ID is the durable server participant identifier.
	ID string
	// ProcessGeneration is the generation this instance serves.
	ProcessGeneration uint64
}
