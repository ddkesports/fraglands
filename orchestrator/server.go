package orchestrator

import (
	"context"
)

// ServerParticipant is one authenticated server process instance. It is
// derived from the process credential by the injected ServerAuthority and
// carries the process generation the instance serves.
type ServerParticipant struct {
	// ID is the durable server participant identifier.
	ID string
	// ProcessGeneration is the generation this instance serves.
	ProcessGeneration uint64
}

// ServerAuthority authenticates server process credentials and derives the
// bound process generation. It is injected and owned by the server: the
// generation is never accepted from request payloads.
type ServerAuthority interface {
	// AuthenticateServer derives the server participant for one presented
	// process credential, or returns an authentication error.
	AuthenticateServer(ctx context.Context, credential string) (*ServerParticipant, error)
}

// AuthenticateServer derives the server participant for one presented
// process credential.
func (o *Orchestrator) AuthenticateServer(ctx context.Context, credential string) (*ServerParticipant, error) {
	return o.servers.AuthenticateServer(ctx, credential)
}

// admission records that one account consumed a join intent on one server
// process generation against one revision. Result acceptance is fenced to
// admitted accounts.
type admission struct {
	accountID         string
	revisionID        string
	processGeneration uint64
}

// admissionKey fences one admission per account per process generation.
type admissionKey struct {
	accountID         string
	processGeneration uint64
}
