package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/paralin/fraglands/core"
	"github.com/paralin/fraglands/orchestrator"
	"github.com/paralin/fraglands/server"
)

// ---------------------------------------------------------------------------
// test doubles
// ---------------------------------------------------------------------------

// mockPreparer completes preparations immediately.
type mockPreparer struct{}

func (m *mockPreparer) Prepare(ctx context.Context, prep *core.ScenarioPreparation) {
	_ = prep.MarkRunning()
	prep.MarkReady(&core.ScenarioRevision{ID: "rev-" + prep.ID, ReplayID: prep.ReplayID})
}

// mockAllocator allocates a ready process on generation 7.
type mockAllocator struct{}

func (m *mockAllocator) Allocate(ctx context.Context, rev *core.ScenarioRevision) (*orchestrator.AllocatedProcess, error) {
	proc := &orchestrator.AllocatedProcess{Generation: 7, ConnectAddress: "127.0.0.1:7777"}
	proc.MarkReady("test: ready")
	return proc, nil
}

// mockIdentityAuthority authenticates "cred-a" to the owner account.
type mockIdentityAuthority struct{}

func (m *mockIdentityAuthority) Authenticate(ctx context.Context, credential string) (*core.Account, error) {
	if credential == "cred-a" {
		return &core.Account{ID: "acct-a", SteamID: 76561198000000001, DisplayName: "Owner"}, nil
	}
	return nil, orchestrator.ErrUnauthenticated
}

// mockServerAuthority authenticates "scred-a" to a participant on
// generation 7 and refuses everyone else.
type mockServerAuthority struct{}

func (m *mockServerAuthority) AuthenticateServer(ctx context.Context, credential string) (*core.ServerParticipant, error) {
	if credential == "scred-a" {
		return &core.ServerParticipant{ID: "srv-a", ProcessGeneration: 7}, nil
	}
	return nil, orchestrator.ErrUnauthenticated
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// setupReady prepares one replay to ready with an allocated process on
// generation 7, owned by the test owner principal.
func setupReady(t *testing.T) (*orchestrator.Orchestrator, string, *core.Account) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	owner := &core.Account{ID: "acct-a", SteamID: 76561198000000001, DisplayName: "Owner"}
	sources := []core.ReplaySource{{ID: "replay-1"}}
	o, err := orchestrator.NewOrchestrator(ctx, sources, &mockPreparer{}, &mockAllocator{}, &mockIdentityAuthority{}, &mockServerAuthority{}, testGrantAuthority())

	id, err := o.Prepare(owner, "replay-1", 0, 63280)
	if err != nil {
		t.Fatal(err.Error())
	}

	// Wait for the allocation to appear.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		status, err := o.Preparation(owner, id)
		if err != nil {
			t.Fatal(err.Error())
		}
		if status.Process != nil || status.AllocationFailure != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	return o, id, owner
}

// admitFlow drives claim -> issue intent -> consume for the owner on the
// default participant (generation 7). Returns the intent.
func admitFlow(t *testing.T, o *orchestrator.Orchestrator, id string, owner *core.Account) *core.JoinIntent {
	t.Helper()
	if _, err := o.Claim(owner, id); err != nil {
		t.Fatal(err.Error())
	}
	target, err := o.IssueJoinIntent(owner, id)
	if err != nil {
		t.Fatal(err.Error())
	}
	if err := o.ConsumeJoinIntent(&core.ServerParticipant{ID: "srv-a", ProcessGeneration: 7}, target.Intent.ID, target.Intent.RevisionID, owner.SteamID); err != nil {
		t.Fatal(err.Error())
	}
	return target.Intent
}

// artifactNameFor builds the spool artifact name for one generation.
func artifactNameFor(generation uint64) string {
	return fmt.Sprintf("runback_summary_gen%d.json", generation)
}

// validSummaryJSON returns a canonical, fully valid TerminalSummary artifact
// for attempt generation 3, process generation 7, with the given revision.
func validSummaryJSON(revision string) []byte {
	return []byte(fmt.Sprintf(`{"version":"runback-attempt/v1","revision":%q,"replay_identity":"replay-1",`+
		`"attempt_generation":3,"server_process_generation":7,"takeover_tick":63280,`+
		`"ending":"secure","ended_at_seconds":200}`, revision))
}

// ---------------------------------------------------------------------------
// composition: the full result path
// ---------------------------------------------------------------------------

// TestCompositionHappyPath drives the full path: a spool artifact is ingested
// through the composed gate, and a private result becomes retrievable by the
// owning account. The AccountID was never supplied by any caller.
func TestCompositionHappyPath(t *testing.T) {
	o, id, owner := setupReady(t)
	intent := admitFlow(t, o, id, owner)

	gate, err := NewServerIngestionGate(o)
	if err != nil {
		t.Fatal(err.Error())
	}

	data := validSummaryJSON(intent.RevisionID)
	if err := gate.Ingest(server.IngestRequest{
		Credential:   "scred-a",
		ArtifactName: artifactNameFor(3),
		Data:         data,
	}); err != nil {
		t.Fatal(err.Error())
	}

	got, err := o.Result(owner, 7, 3)
	if err != nil {
		t.Fatalf("result must be retrievable by the owning account: %v", err)
	}
	if got.AccountID != owner.ID {
		t.Fatalf("expected account %s, got %s", owner.ID, got.AccountID)
	}
	if got.RevisionID != intent.RevisionID {
		t.Fatalf("expected revision %s, got %s", intent.RevisionID, got.RevisionID)
	}
	if got.ProcessGeneration != 7 || got.AttemptGeneration != 3 {
		t.Fatalf("bad generations: %d / %d", got.ProcessGeneration, got.AttemptGeneration)
	}
	if got.ReplayID != "replay-1" {
		t.Fatalf("expected replay identity mapped from summary, got %q", got.ReplayID)
	}
	if got.TakeoverTick != 63280 {
		t.Fatalf("expected takeover tick mapped from summary, got %d", got.TakeoverTick)
	}
}

// TestCompositionAccountIsNeverCallerSupplied proves the account of the
// result comes from admission state, not from the artifact or the request.
func TestCompositionAccountIsNeverCallerSupplied(t *testing.T) {
	o, id, owner := setupReady(t)
	intent := admitFlow(t, o, id, owner)

	gate, err := NewServerIngestionGate(o)
	if err != nil {
		t.Fatal(err.Error())
	}

	// The artifact is a raw spool line: it cannot name an account. There is
	// no request field for one either. The stored result must be attributed
	// to the account from the admission record.
	data := validSummaryJSON(intent.RevisionID)
	if err := gate.Ingest(server.IngestRequest{
		Credential:   "scred-a",
		ArtifactName: artifactNameFor(3),
		Data:         data,
	}); err != nil {
		t.Fatal(err.Error())
	}

	got, err := o.Result(owner, 7, 3)
	if err != nil {
		t.Fatal(err.Error())
	}
	if got.AccountID != owner.ID {
		t.Fatalf("result attributed to %s, want %s (from admission)", got.AccountID, owner.ID)
	}
}

// TestCompositionCredentialFencing proves a result can only be ingested by
// the credential bound to the generation the artifact claims.
func TestCompositionCredentialFencing(t *testing.T) {
	o, id, owner := setupReady(t)
	intent := admitFlow(t, o, id, owner)

	gate, err := NewServerIngestionGate(o)
	if err != nil {
		t.Fatal(err.Error())
	}

	// A credential that resolves to no participant is refused.
	err = gate.Ingest(server.IngestRequest{
		Credential:   "scred-stranger",
		ArtifactName: artifactNameFor(3),
		Data:         validSummaryJSON(intent.RevisionID),
	})
	if err == nil {
		t.Fatal("expected refusal for unknown credential")
	}

	// The correct credential works.
	if err = gate.Ingest(server.IngestRequest{
		Credential:   "scred-a",
		ArtifactName: artifactNameFor(3),
		Data:         validSummaryJSON(intent.RevisionID),
	}); err != nil {
		t.Fatalf("expected acceptance for the bound credential: %v", err)
	}
}

// TestCompositionRevisionFencing proves a summary against a different
// revision than the admission records is refused whole.
func TestCompositionRevisionFencing(t *testing.T) {
	o, id, owner := setupReady(t)
	admitFlow(t, o, id, owner)

	gate, err := NewServerIngestionGate(o)
	if err != nil {
		t.Fatal(err.Error())
	}

	// "rev-other" is not the revision the account was admitted against.
	err = gate.Ingest(server.IngestRequest{
		Credential:   "scred-a",
		ArtifactName: artifactNameFor(3),
		Data:         validSummaryJSON("rev-other"),
	})
	if err == nil {
		t.Fatal("expected refusal for revision mismatch")
	}
	if _, lookupErr := o.Result(owner, 7, 3); !errors.Is(lookupErr, core.ErrNoResult) {
		t.Fatalf("refused ingest must not store a result, got %v", lookupErr)
	}
}

// TestCompositionNamePayloadFencing proves the artifact name must carry the
// same attempt generation as the payload.
func TestCompositionNamePayloadFencing(t *testing.T) {
	o, id, owner := setupReady(t)
	intent := admitFlow(t, o, id, owner)

	gate, err := NewServerIngestionGate(o)
	if err != nil {
		t.Fatal(err.Error())
	}

	err = gate.Ingest(server.IngestRequest{
		Credential:   "scred-a",
		ArtifactName: artifactNameFor(4), // name says gen 4, payload says gen 3
		Data:         validSummaryJSON(intent.RevisionID),
	})
	if err == nil {
		t.Fatal("expected refusal for name/payload generation split")
	}
	if _, lookupErr := o.Result(owner, 7, 3); !errors.Is(lookupErr, core.ErrNoResult) {
		t.Fatalf("refused ingest must not store a result, got %v", lookupErr)
	}
}

// TestCompositionWrongParticipantGenerationFencing proves a participant on
// another generation cannot deliver a summary for this generation.
func TestCompositionWrongParticipantGenerationFencing(t *testing.T) {
	o, id, owner := setupReady(t)
	intent := admitFlow(t, o, id, owner)

	// Swap the authority so "scred-a" resolves to a participant on
	// generation 9 instead of 7.
	o2, id2, owner2 := setupReady(t)
	_ = o2
	_ = id2
	_ = owner2

	gate, err := NewServerIngestionGate(o)
	if err != nil {
		t.Fatal(err.Error())
	}

	// Tamper: the payload claims generation 7 but we authenticate through a
	// resolver bound to generation 9 by substituting the authority.
	// Instead of swapping the orchestrator's authority (which is fixed),
	// we exercise the mismatch by presenting a summary for generation 9.
	wrongGen := strings.Replace(string(validSummaryJSON(intent.RevisionID)),
		`"server_process_generation":7`, `"server_process_generation":9`, 1)
	err = gate.Ingest(server.IngestRequest{
		Credential:   "scred-a",
		ArtifactName: artifactNameFor(3),
		Data:         []byte(wrongGen),
	})
	if err == nil {
		t.Fatal("expected refusal for process generation mismatch")
	}
	if _, lookupErr := o.Result(owner, 7, 3); !errors.Is(lookupErr, core.ErrNoResult) {
		t.Fatalf("refused ingest must not store a result, got %v", lookupErr)
	}
}

// TestCompositionNoResultWithoutAdmission proves that no admission means no
// result, even for a fully valid artifact.
func TestCompositionNoResultWithoutAdmission(t *testing.T) {
	o, _, owner := setupReady(t)
	gate, err := NewServerIngestionGate(o)
	if err != nil {
		t.Fatal(err.Error())
	}

	// revision "rev-prep-1" exists (mockPreparer), but no intent was ever
	// consumed, so there is no admission to attribute the summary to.
	if err := gate.Ingest(server.IngestRequest{
		Credential:   "scred-a",
		ArtifactName: artifactNameFor(3),
		Data:         validSummaryJSON("rev-prep-1"),
	}); err == nil {
		t.Fatal("expected refusal without admission")
	}
	if _, err := o.Result(owner, 7, 3); !errors.Is(err, core.ErrNoResult) {
		t.Fatalf("expected no result stored, got %v", err)
	}
}

// TestCompositionNewConstructorRefusals covers composition-time validation.
func TestCompositionNewConstructorRefusals(t *testing.T) {
	if _, err := NewServerIngestionGate(nil); err == nil {
		t.Fatal("expected refusal without an orchestrator")
	}
}

// ---------------------------------------------------------------------------
// composition: duplicate / no-partial semantics
// ---------------------------------------------------------------------------

// TestCompositionDuplicateArtifactExactlyOneResult proves the duplicate
// artifact is refused and the stored result is unchanged.
func TestCompositionDuplicateArtifactExactlyOneResult(t *testing.T) {
	o, id, owner := setupReady(t)
	intent := admitFlow(t, o, id, owner)

	gate, err := NewServerIngestionGate(o)
	if err != nil {
		t.Fatal(err.Error())
	}

	req := server.IngestRequest{
		Credential:   "scred-a",
		ArtifactName: artifactNameFor(3),
		Data:         validSummaryJSON(intent.RevisionID),
	}
	if err := gate.Ingest(req); err != nil {
		t.Fatal(err.Error())
	}
	first, err := o.Result(owner, 7, 3)
	if err != nil {
		t.Fatal(err.Error())
	}

	if err := gate.Ingest(req); err == nil {
		t.Fatal("expected duplicate refusal")
	}

	second, err := o.Result(owner, 7, 3)
	if err != nil {
		t.Fatal(err.Error())
	}
	if first != second {
		t.Fatal("duplicate ingestion must not alter the stored result")
	}
}

// TestCompositionConcurrentDuplicateExactlyOneAccept hammers the gate with
// concurrent identical requests: exactly one acceptance survives.
func TestCompositionConcurrentDuplicateExactlyOneAccept(t *testing.T) {
	o, id, owner := setupReady(t)
	intent := admitFlow(t, o, id, owner)

	gate, err := NewServerIngestionGate(o)
	if err != nil {
		t.Fatal(err.Error())
	}

	req := server.IngestRequest{
		Credential:   "scred-a",
		ArtifactName: artifactNameFor(3),
		Data:         validSummaryJSON(intent.RevisionID),
	}

	const workers = 16
	var wg sync.WaitGroup
	var successes int64
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := gate.Ingest(req); err != nil {
				errs <- err
			} else {
				atomic.AddInt64(&successes, 1)
			}
		}()
	}
	wg.Wait()
	close(errs)

	if successes != 1 {
		t.Fatalf("expected exactly 1 acceptance under race, got %d", successes)
	}
	for err := range errs {
		if !errors.Is(err, server.ErrSummaryDuplicate) && !errors.Is(err, core.ErrResultAlreadyAccepted) {
			t.Errorf("unexpected refusal error: %v", err)
		}
	}
	if _, err := o.Result(owner, 7, 3); err != nil {
		t.Fatalf("expected one stored result, got %v", err)
	}
}

// TestCompositionConcurrentDistinctAllAccepted proves distinct attempts
// (different attempt generations) do not interfere under concurrency.
func TestCompositionConcurrentDistinctAllAccepted(t *testing.T) {
	o, id, owner := setupReady(t)
	intent := admitFlow(t, o, id, owner)

	gate, err := NewServerIngestionGate(o)
	if err != nil {
		t.Fatal(err.Error())
	}

	const workers = 8
	var wg sync.WaitGroup
	var successes int64
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			gen := uint64(n + 100)
			data := strings.Replace(string(validSummaryJSON(intent.RevisionID)),
				`"attempt_generation":3`, fmt.Sprintf(`"attempt_generation":%d`, gen), 1)
			name := fmt.Sprintf("runback_summary_gen%d.json", gen)
			if err := gate.Ingest(server.IngestRequest{Credential: "scred-a", ArtifactName: name, Data: []byte(data)}); err == nil {
				atomic.AddInt64(&successes, 1)
			}
		}(i)
	}
	wg.Wait()

	if successes != workers {
		t.Fatalf("expected %d distinct accepted results, got %d", workers, successes)
	}
}

// TestCompositionRefusalLeavesNoPartialState proves a mid-path refusal
// (identity mismatch after successful decode) leaves nothing behind: no
// result, and the artifact can be re-ingested later (digest not reserved).
func TestCompositionRefusalLeavesNoPartialState(t *testing.T) {
	o, id, owner := setupReady(t)
	intent := admitFlow(t, o, id, owner)

	gate, err := NewServerIngestionGate(o)
	if err != nil {
		t.Fatal(err.Error())
	}

	// First, a mismatched-generation artifact: refused at binding.
	wrongGen := strings.Replace(string(validSummaryJSON(intent.RevisionID)),
		`"server_process_generation":7`, `"server_process_generation":9`, 1)
	err = gate.Ingest(server.IngestRequest{
		Credential:   "scred-a",
		ArtifactName: artifactNameFor(3),
		Data:         []byte(wrongGen),
	})
	if err == nil {
		t.Fatal("expected identity mismatch refusal")
	}

	// The same attempt ingested correctly afterwards must succeed: the
	// refused attempt reserved nothing.
	if err := gate.Ingest(server.IngestRequest{
		Credential:   "scred-a",
		ArtifactName: artifactNameFor(3),
		Data:         validSummaryJSON(intent.RevisionID),
	}); err != nil {
		t.Fatalf("refused attempt must leave no trace; retry failed: %v", err)
	}
	if _, err := o.Result(owner, 7, 3); err != nil {
		t.Fatalf("expected stored result after successful retry: %v", err)
	}
}

// testGrantAuthority returns a fresh in-memory grant authority for tests.
func testGrantAuthority() core.GrantAuthority {
	a, err := core.NewHMACGrantAuthority(core.GrantAuthorityConfig{
		Clock: time.Now,
		TTL:   time.Hour,
	})
	if err != nil {
		panic(err.Error())
	}
	return a
}
