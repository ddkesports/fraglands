package core

import (
	"context"
	"testing"
	"time"
)

func TestScenarioPreparationHappyPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	p := NewScenarioPreparation("prep-1", "replay-1", 0, 63280, nil)
	if p.State() != PreparationQueued {
		t.Fatalf("expected queued, got %s", p.State())
	}

	stateDone := make(chan PreparationState, 1)
	go func() {
		state, err := p.WaitReady(ctx)
		if err != nil {
			t.Error(err.Error())
			return
		}
		stateDone <- state
	}()

	if err := p.MarkRunning(); err != nil {
		t.Fatal(err.Error())
	}

	// The wait must observe the ready transition without polling.
	revision := &ScenarioRevision{ID: "rev-1", ReplayID: "replay-1", TakeoverTick: 63280, Fidelity: FidelityPreview}
	if err := p.MarkReady(revision); err != nil {
		t.Fatal(err.Error())
	}

	select {
	case state := <-stateDone:
		if state != PreparationReady {
			t.Fatalf("expected ready, got %s", state)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for ready")
	}

	if p.Revision() != revision {
		t.Fatal("expected revision to be set")
	}
}

func TestScenarioPreparationFailures(t *testing.T) {
	p := NewScenarioPreparation("prep-2", "replay-1", 0, 63280, nil)
	if err := p.MarkFailed(&FailureReason{Code: "replay_unsupported", Message: "field kHealth unsupported"}); err != nil {
		t.Fatal(err.Error())
	}
	if p.State() != PreparationFailed {
		t.Fatalf("expected failed, got %s", p.State())
	}
	if p.Failure() == nil || p.Failure().Code != "replay_unsupported" {
		t.Fatal("expected typed failure reason")
	}

	// Terminal states never transition again.
	if err := p.MarkRunning(); err == nil {
		t.Fatal("expected terminal transition refusal")
	}
	// A failed preparation never carries a revision.
	if p.Revision() != nil {
		t.Fatal("failed preparation must not carry a revision")
	}
}

func TestScenarioPreparationCancel(t *testing.T) {
	p := NewScenarioPreparation("prep-3", "replay-1", 0, 63280, nil)
	if err := p.MarkCancelled(); err != nil {
		t.Fatal(err.Error())
	}
	if p.State() != PreparationCancelled {
		t.Fatalf("expected cancelled, got %s", p.State())
	}
	if !p.State().Terminal() {
		t.Fatal("cancelled must be terminal")
	}
}

func TestScenarioPreparationInvalidTransitions(t *testing.T) {
	p := NewScenarioPreparation("prep-4", "replay-1", 0, 63280, nil)
	if err := p.MarkReady(&ScenarioRevision{ID: "rev-early"}); err == nil {
		t.Fatal("expected queued to ready refusal")
	}
	if err := p.MarkRunning(); err != nil {
		t.Fatal(err.Error())
	}
	if err := p.MarkCancelled(); err != nil {
		t.Fatal(err.Error())
	}
	if err := p.MarkRunning(); err == nil {
		t.Fatal("expected cancelled to running refusal")
	}
	if err := p.MarkFailed(nil); err == nil {
		t.Fatal("expected typed reason requirement")
	}
}
