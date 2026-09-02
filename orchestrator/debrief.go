package orchestrator

import "github.com/paralin/fraglands/core"

// Result returns the private result for the principal and one attempt, or
// core.ErrNoResult. This is the debrief retrieval path: the account is the
// authenticated principal, never a caller-supplied value.
func (o *Orchestrator) Result(principal *core.Account, processGeneration, attemptGeneration uint64) (*core.AttemptResult, error) {
	if principal == nil {
		return nil, ErrUnauthenticated
	}
	return o.results.Lookup(principal.ID, processGeneration, attemptGeneration)
}
