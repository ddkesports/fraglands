// Command fraglands-web serves the minimal private Fraglands web console.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/paralin/fraglands/core"
	"github.com/paralin/fraglands/orchestrator"
	"github.com/paralin/fraglands/web"
)

// staticIdentityAuthority is the development identity authority: it binds a
// fixed set of bearer credentials to accounts. A deployment replaces this
// with its own authority; identities are always derived server-side.
type staticIdentityAuthority struct {
	accounts map[string]*core.Account
}

// Authenticate returns the account bound to the credential.
func (s *staticIdentityAuthority) Authenticate(ctx context.Context, credential string) (*core.Account, error) {
	acct, ok := s.accounts[credential]
	if !ok {
		return nil, orchestrator.ErrUnauthenticated
	}
	return acct, nil
}

// noopPreparer refuses every preparation: the reconstruction provider is a
// separate deployment and is not wired into the development binary.
type noopPreparer struct{}

// Prepare fails the preparation with a typed reason.
func (n *noopPreparer) Prepare(ctx context.Context, prep *core.ScenarioPreparation) {
	_ = prep.MarkRunning()
	prep.MarkFailed(&core.FailureReason{
		Code:    "provider_unwired",
		Message: "the reconstruction provider is not wired into this binary",
	})
}

// errAllocatorUnwired is the typed refusal of the development allocator.
var errAllocatorUnwired = errors.New("no worker allocator wired into this binary")

// noopAllocator refuses every allocation for the same reason.
type noopAllocator struct{}

// Allocate refuses with a typed failure.
func (n *noopAllocator) Allocate(ctx context.Context, rev *core.ScenarioRevision) (*orchestrator.AllocatedProcess, error) {
	return nil, errAllocatorUnwired
}

func main() {
	addr := os.Getenv("FRAGLANDS_WEB_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	orch := orchestrator.NewOrchestrator(ctx, nil, &noopPreparer{}, &noopAllocator{}, &staticIdentityAuthority{
		accounts: map[string]*core.Account{},
	})
	console, err := web.NewWeb(orch)
	if err != nil {
		log.Fatal(err.Error())
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           console.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("fraglands-web listening on %s", addr)
	log.Fatal(server.ListenAndServe())
}
