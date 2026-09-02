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

// mockServerAuthority authenticates fixed credentials to fixed server
// participants.
type mockServerAuthority struct {
	participants map[string]*ServerParticipant
}

// AuthenticateServer returns the server participant bound to the credential.
func (m *mockServerAuthority) AuthenticateServer(ctx context.Context, credential string) (*ServerParticipant, error) {
	p, ok := m.participants[credential]
	if !ok {
		return nil, ErrUnauthenticated
	}
	return p, nil
}

// testServerParticipants returns two server participants: one bound to
// process generation 1 (the default test generation) and one bound to
// another generation.
func testServerParticipants() (p1, p2 *ServerParticipant) {
	p1 = &ServerParticipant{ID: "srv-a", ProcessGeneration: 1}
	p2 = &ServerParticipant{ID: "srv-b", ProcessGeneration: 2}
	return p1, p2
}

// testServerAuthority builds a server authority over the test server
// participants. The default participant is bound to process generation 1.
func testServerAuthority() *mockServerAuthority {
	p1, p2 := testServerParticipants()
	return &mockServerAuthority{participants: map[string]*ServerParticipant{
		"scred-a": p1,
		"scred-b": p2,
	}}
}
