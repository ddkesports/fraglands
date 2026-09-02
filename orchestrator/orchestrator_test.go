package orchestrator

import (
	"context"
	"errors"
	"time"

	"github.com/paralin/fraglands/core"
)

// mockPreparer is a test Preparer that simulates the provider work.
type mockPreparer struct {
	delay time.Duration
	fail  bool
}

// Prepare simulates provider work and moves the preparation to a terminal
// state.
func (m *mockPreparer) Prepare(ctx context.Context, prep *core.ScenarioPreparation) {
	if m.delay > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(m.delay):
		}
	}

	if err := prep.MarkRunning(); err != nil {
		return
	}

	// Fail closed with one typed reason when the provider refuses.
	if m.fail {
		prep.MarkFailed(&core.FailureReason{Code: "test_fail", Message: "simulated failure"})
		return
	}
	prep.MarkReady(&core.ScenarioRevision{ID: "rev-" + prep.ID, ReplayID: prep.ReplayID})
}

// mockAllocator is a test ProcessAllocator that simulates a worker.
type mockAllocator struct {
	fail bool
}

// Allocate simulates starting one server process.
func (m *mockAllocator) Allocate(ctx context.Context, rev *core.ScenarioRevision) (*AllocatedProcess, error) {
	if m.fail {
		return nil, errors.New("worker offline")
	}
	proc := &AllocatedProcess{Generation: 1, ConnectAddress: "127.0.0.1:7777"}
	proc.MarkReady("test: process bound to port")
	return proc, nil
}

// mockIdentityAuthority authenticates one fixed principal per credential.
type mockIdentityAuthority struct {
	accounts map[string]*core.Account
}

// Authenticate returns the account bound to the credential.
func (m *mockIdentityAuthority) Authenticate(ctx context.Context, credential string) (*core.Account, error) {
	acct, ok := m.accounts[credential]
	if !ok {
		return nil, ErrUnauthenticated
	}
	return acct, nil
}

// testAccounts returns two principals: the owner and another account.
func testAccounts() (owner, other *core.Account) {
	owner = &core.Account{ID: "acct-a", SteamID: 76561198000000001, DisplayName: "Owner"}
	other = &core.Account{ID: "acct-b", SteamID: 76561198000000002, DisplayName: "Other"}
	return owner, other
}

// testIdentityAuthority builds an authority over the two test principals.
func testIdentityAuthority() *mockIdentityAuthority {
	owner, other := testAccounts()
	return &mockIdentityAuthority{accounts: map[string]*core.Account{
		"cred-a": owner,
		"cred-b": other,
	}}
}
