package orchestrator

import (
	"context"

	"github.com/paralin/fraglands/core"
)

// ServerParticipant is one authenticated server process instance. The
// authoritative definition lives in core so the server ingestion gate binds
// decoded artifacts to the same identity the orchestrator authenticates:
// this alias keeps one type, not a copy.
type ServerParticipant = core.ServerParticipant

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
