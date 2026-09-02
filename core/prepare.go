package core

import (
	"fmt"

	"github.com/paralin/s2replay/analysis"
	"github.com/pkg/errors"
)

// This file is the only stateful seam between the pure RunbackFacts compiler
// and the ScenarioPreparation lifecycle. It owns no game state: it moves the
// caller's preparation exactly once, to ready with an immutable revision or to
// failed with one typed reason and no partial state.

// prepareFailureCode is the typed reason code used when compilation refuses.
const prepareFailureCode = "compile_refused"

// RevisionID derives the deterministic revision identifier for one facts
// record: the same replay bytes and tick always yield the same ID.
func RevisionID(facts analysis.RunbackFacts) string {
	sha := facts.Source.SHA256
	if len(sha) > 16 {
		sha = sha[:16]
	}
	if sha == "" && facts.Source.MatchID != 0 {
		sha = fmt.Sprintf("match-%d", facts.Source.MatchID)
	}
	if sha == "" {
		sha = "unknown-replay"
	}
	return fmt.Sprintf("rev-%s-%d", sha, facts.Tick)
}

// PrepareScenario compiles the RunbackFacts into an immutable
// ScenarioRevision, attaches it to the preparation, and moves the preparation
// to ready; or fails the preparation closed with one typed reason and no
// partial state.
//
// A returned error means the preparation was transitioned to failed; the
// caller must not retry or reuse the preparation.
func PrepareScenario(
	prep *ScenarioPreparation,
	facts analysis.RunbackFacts,
	caps map[string]CapabilityRequirement,
	maxFreshnessTicks uint32,
) (*ScenarioRevision, []Omission, error) {
	if err := prep.MarkRunning(); err != nil {
		return nil, nil, errors.Wrap(err, "core: mark preparation running")
	}

	out := Compile(CompileRequest{
		Facts:             facts,
		Capabilities:      caps,
		MaxFreshnessTicks: maxFreshnessTicks,
		RevisionID:        RevisionID(facts),
	})

	if out.Refusal != nil {
		if err := prep.MarkFailed(&FailureReason{
			Code:    prepareFailureCode,
			Message: out.Refusal.Reason,
		}); err != nil {
			return nil, nil, errors.Wrap(err, "core: mark preparation failed")
		}
		return nil, nil, out.Refusal
	}

	if err := prep.MarkReady(out.Revision); err != nil {
		return nil, nil, errors.Wrap(err, "core: mark preparation ready")
	}
	return out.Revision, out.Omissions, nil
}
