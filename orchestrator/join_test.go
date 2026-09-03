package orchestrator

import (
	"context"
	"testing"

	"github.com/paralin/fraglands/core"
)

func TestIssueJoinIntent(t *testing.T) {
	o, id, owner, _ := setupReady(t)

	if _, err := o.Claim(owner, id); err != nil {
		t.Fatal(err.Error())
	}

	target, err := o.IssueJoinIntent(owner, id)
	if err != nil {
		t.Fatal(err.Error())
	}
	if target.Intent.AccountID != owner.ID {
		t.Fatalf("expected %s, got %s", owner.ID, target.Intent.AccountID)
	}
	if target.Intent.SteamID != owner.SteamID {
		t.Fatal("expected intent bound to the principal steam identity")
	}
	if target.Intent.RevisionID == "" {
		t.Fatal("expected intent bound to a revision")
	}
	if target.Intent.Generation == 0 {
		t.Fatal("expected intent bound to a process generation")
	}
	if target.Process != nil && target.Process.Generation != target.Intent.Generation {
		t.Fatal("expected intent bound to the target process generation")
	}
}

func TestIssueJoinIntentWithoutSlot(t *testing.T) {
	o, id, owner, _ := setupReady(t)

	if _, err := o.IssueJoinIntent(owner, id); err != core.ErrNoSlotClaimed {
		t.Fatalf("expected ErrNoSlotClaimed, got %v", err)
	}
}

func TestIssueJoinIntentUnknownPreparation(t *testing.T) {
	o, _, owner, _ := setupReady(t)

	if _, err := o.IssueJoinIntent(owner, "nonexistent"); err != ErrUnknownPreparation {
		t.Fatalf("expected ErrUnknownPreparation, got %v", err)
	}
}

func TestIssueJoinIntentAllocationFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	owner, _ := testAccounts()
	sources := []core.ReplaySource{{ID: "replay-1"}}
	o, err := NewOrchestrator(ctx, sources, &mockPreparer{}, &mockAllocator{fail: true}, testIdentityAuthority(), testServerAuthority(), testGrantAuthority())
	if err != nil {
		t.Fatal(err.Error())
	}

	id, err := o.Prepare(owner, "replay-1", 0, 63280)
	if err != nil {
		t.Fatal(err.Error())
	}
	if _, err := o.Claim(owner, id); err != nil {
		t.Fatal(err.Error())
	}

	waitAllocated(t, o, id)
	_, err = o.IssueJoinIntent(owner, id)
	var allocErr *AllocationError
	if !errorsAs(err, &allocErr) {
		t.Fatalf("expected AllocationError, got %v", err)
	}
	if allocErr.Reason.Code != AllocationFailureCode {
		t.Fatalf("expected %s, got %s", AllocationFailureCode, allocErr.Reason.Code)
	}
}

func TestConsumeJoinIntent(t *testing.T) {
	o, id, owner, _ := setupReady(t)
	participant := testServerAuthority().participants["scred-a"]

	if _, err := o.Claim(owner, id); err != nil {
		t.Fatal(err.Error())
	}
	target, err := o.IssueJoinIntent(owner, id)
	if err != nil {
		t.Fatal(err.Error())
	}

	// Consume with the bound revision, generation, and Steam identity.
	if err := o.ConsumeJoinIntent(participant, target.Intent.ID, target.Intent.RevisionID, owner.SteamID); err != nil {
		t.Fatal(err.Error())
	}

	// One-use: the second consume is refused.
	if err := o.ConsumeJoinIntent(participant, target.Intent.ID, target.Intent.RevisionID, owner.SteamID); err != core.ErrIntentAlreadyUsed {
		t.Fatalf("expected ErrIntentAlreadyUsed, got %v", err)
	}
}

func TestConsumeJoinIntentMismatch(t *testing.T) {
	o, id, owner, _ := setupReady(t)
	participant := testServerAuthority().participants["scred-a"]

	if _, err := o.Claim(owner, id); err != nil {
		t.Fatal(err.Error())
	}
	target, err := o.IssueJoinIntent(owner, id)
	if err != nil {
		t.Fatal(err.Error())
	}

	// Refused consumes never burn the intent.
	if err := o.ConsumeJoinIntent(participant, target.Intent.ID, "rev-other", owner.SteamID); err != core.ErrRevisionMismatch {
		t.Fatalf("expected ErrRevisionMismatch, got %v", err)
	}
	if err := o.ConsumeJoinIntent(participant, target.Intent.ID, target.Intent.RevisionID, core.SteamID(999)); err != core.ErrSteamIDAlreadyBound {
		t.Fatalf("expected ErrSteamIDAlreadyBound, got %v", err)
	}
	if target.Intent.Used() {
		t.Fatal("refused consumes must not burn the intent")
	}
	if err := o.ConsumeJoinIntent(participant, "nonexistent", "rev", owner.SteamID); err != ErrUnknownIntent {
		t.Fatalf("expected ErrUnknownIntent, got %v", err)
	}
}

func TestConsumeJoinIntentWrongGeneration(t *testing.T) {
	o, id, owner, _ := setupReady(t)
	_, wrongParticipant := testServerParticipants()

	if _, err := o.Claim(owner, id); err != nil {
		t.Fatal(err.Error())
	}
	target, err := o.IssueJoinIntent(owner, id)
	if err != nil {
		t.Fatal(err.Error())
	}

	// A participant bound to another process generation cannot consume the
	// intent: it was issued for a different process.
	if err := o.ConsumeJoinIntent(wrongParticipant, target.Intent.ID, target.Intent.RevisionID, owner.SteamID); err != ErrWrongProcessGeneration {
		t.Fatalf("expected ErrWrongProcessGeneration, got %v", err)
	}
	// The intent was not burned by the refused consume.
	if target.Intent.Used() {
		t.Fatal("refused consume must not burn the intent")
	}
	// No admission was recorded.
	if adm := o.admissionFor(owner.ID, wrongParticipant.ProcessGeneration); adm != nil {
		t.Fatal("no admission must be recorded for a refused consume")
	}
}

func TestAcceptResultRequiresParticipant(t *testing.T) {
	o, id, owner, _ := setupReady(t)
	participant, otherParticipant := testServerParticipants()

	if _, err := o.Claim(owner, id); err != nil {
		t.Fatal(err.Error())
	}
	target, err := o.IssueJoinIntent(owner, id)
	if err != nil {
		t.Fatal(err.Error())
	}

	// A nil participant is refused.
	if err := o.AcceptResult(nil, &core.AttemptResult{}); err != ErrUnauthenticated {
		t.Fatalf("expected ErrUnauthenticated, got %v", err)
	}

	// A result for another process generation is refused: the participant
	// cannot invent process identity.
	wrongGen := &core.AttemptResult{
		ID:                "res-0",
		AccountID:         owner.ID,
		RevisionID:        target.Intent.RevisionID,
		ProcessGeneration: otherParticipant.ProcessGeneration,
		AttemptGeneration: 1,
		ReplayID:          "replay-1",
	}
	if err := o.AcceptResult(participant, wrongGen); err != ErrWrongProcessGeneration {
		t.Fatalf("expected ErrWrongProcessGeneration, got %v", err)
	}

	// A result for an account that never consumed an intent on this
	// process generation is refused.
	_, stranger := testAccounts()
	unadmitted := &core.AttemptResult{
		ID:                "res-x",
		AccountID:         stranger.ID,
		RevisionID:        target.Intent.RevisionID,
		ProcessGeneration: participant.ProcessGeneration,
		AttemptGeneration: 1,
		ReplayID:          "replay-1",
	}
	if err := o.AcceptResult(participant, unadmitted); err != ErrUnadmittedAccount {
		t.Fatalf("expected ErrUnadmittedAccount, got %v", err)
	}

	// A result with a revision other than the admitted one is refused.
	if err := o.ConsumeJoinIntent(participant, target.Intent.ID, target.Intent.RevisionID, owner.SteamID); err != nil {
		t.Fatal(err.Error())
	}
	forgedRevision := &core.AttemptResult{
		ID:                "res-2",
		AccountID:         owner.ID,
		RevisionID:        "rev-forged",
		ProcessGeneration: participant.ProcessGeneration,
		AttemptGeneration: 1,
		ReplayID:          "replay-1",
	}
	if err := o.AcceptResult(participant, forgedRevision); err != core.ErrRevisionMismatch {
		t.Fatalf("expected ErrRevisionMismatch, got %v", err)
	}

	// A result with an empty account identifier is refused.
	emptyAccount := &core.AttemptResult{
		ID:                "res-3",
		AccountID:         "",
		RevisionID:        target.Intent.RevisionID,
		ProcessGeneration: participant.ProcessGeneration,
		AttemptGeneration: 1,
		ReplayID:          "replay-1",
	}
	if err := o.AcceptResult(participant, emptyAccount); err != core.ErrInvalidAccount {
		t.Fatalf("expected ErrInvalidAccount, got %v", err)
	}

	// Nothing was stored by the refused submissions.
	if _, err := o.Result(owner, participant.ProcessGeneration, 1); err != core.ErrNoResult {
		t.Fatalf("expected ErrNoResult, got %v", err)
	}
}

func TestAcceptResultRequiresAdmission(t *testing.T) {
	o, id, owner, _ := setupReady(t)
	participant, _ := testServerParticipants()

	if _, err := o.Claim(owner, id); err != nil {
		t.Fatal(err.Error())
	}
	target, err := o.IssueJoinIntent(owner, id)
	if err != nil {
		t.Fatal(err.Error())
	}

	// Before the intent is consumed, no result can be accepted for the
	// account on this process generation.
	result := &core.AttemptResult{
		ID:                "res-1",
		AccountID:         owner.ID,
		RevisionID:        target.Intent.RevisionID,
		ProcessGeneration: participant.ProcessGeneration,
		AttemptGeneration: 1,
		ReplayID:          "replay-1",
	}
	if err := o.AcceptResult(participant, result); err != ErrUnadmittedAccount {
		t.Fatalf("expected ErrUnadmittedAccount, got %v", err)
	}

	// After the consume, the result is accepted.
	if err := o.ConsumeJoinIntent(participant, target.Intent.ID, target.Intent.RevisionID, owner.SteamID); err != nil {
		t.Fatal(err.Error())
	}
	if err := o.AcceptResult(participant, result); err != nil {
		t.Fatal(err.Error())
	}
}

func TestServerParticipantRace(t *testing.T) {
	o, id, owner, _ := setupReady(t)
	participant, _ := testServerParticipants()

	if _, err := o.Claim(owner, id); err != nil {
		t.Fatal(err.Error())
	}
	target, err := o.IssueJoinIntent(owner, id)
	if err != nil {
		t.Fatal(err.Error())
	}

	// Many concurrent consumers race for the one-use intent: exactly one
	// consume succeeds and exactly one admission is recorded.
	const consumers = 16
	consumeErrs := make(chan error, consumers)
	for i := 0; i < consumers; i++ {
		go func() {
			consumeErrs <- o.ConsumeJoinIntent(participant, target.Intent.ID, target.Intent.RevisionID, owner.SteamID)
		}()
	}
	accepted := 0
	for i := 0; i < consumers; i++ {
		if err := <-consumeErrs; err == nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("expected exactly 1 successful consume, got %d", accepted)
	}
	if admissions := len(o.admissions); admissions != 1 {
		t.Fatalf("expected exactly 1 admission, got %d", admissions)
	}

	// Many concurrent submissions of the same attempt race: exactly one
	// result is stored, the rest are refused as duplicates.
	const submissions = 16
	submitErrs := make(chan error, submissions)
	for i := 0; i < submissions; i++ {
		go func() {
			submitErrs <- o.AcceptResult(participant, &core.AttemptResult{
				ID:                "res-race",
				AccountID:         owner.ID,
				RevisionID:        target.Intent.RevisionID,
				ProcessGeneration: participant.ProcessGeneration,
				AttemptGeneration: 7,
				ReplayID:          "replay-1",
				TakeoverTick:      63280,
			})
		}()
	}
	stored := 0
	for i := 0; i < submissions; i++ {
		if err := <-submitErrs; err == nil {
			stored++
		}
	}
	if stored != 1 {
		t.Fatalf("expected exactly 1 stored result, got %d", stored)
	}
}
