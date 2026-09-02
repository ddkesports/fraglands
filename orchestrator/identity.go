package orchestrator

import (
	"context"

	"github.com/paralin/fraglands/core"
)

// IdentityAuthority authenticates presented credentials and derives the
// immutable account and its bound Steam identity. It is injected and owned by
// the server: identities are never accepted from request payloads or query
// strings.
type IdentityAuthority interface {
	// Authenticate derives the account for one presented credential, or
	// returns an authentication error.
	Authenticate(ctx context.Context, credential string) (*core.Account, error)
}

// Authenticate derives the principal for one presented credential. The API
// layer calls this once per request; every operation then acts as the
// derived principal.
func (o *Orchestrator) Authenticate(ctx context.Context, credential string) (*core.Account, error) {
	return o.identities.Authenticate(ctx, credential)
}
