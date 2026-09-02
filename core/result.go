package core

import (
	"sync"

	"github.com/pkg/errors"
)

// AttemptResult is one private structured attempt result. It is bound to the
// revision, server process generation, attempt generation, replay identity,
// and takeover timecode. Results are private Fraglands-core records: never a
// public board entry, and a failed upload never becomes a result.
type AttemptResult struct {
	// ID is the result identifier.
	ID string
	// AccountID is the account the result belongs to.
	AccountID string
	// RevisionID is the immutable revision the attempt ran against.
	RevisionID string
	// ProcessGeneration is the server process generation that hosted it.
	ProcessGeneration uint64
	// AttemptGeneration fences duplicate results for the same attempt.
	AttemptGeneration uint64
	// ReplayID is the replay identity the revision was built from.
	ReplayID string
	// TakeoverTick is the timecode measurement started at.
	TakeoverTick uint32
}

// ResultStore keeps private results indexed by attempt. Duplicate acceptance
// for the same attempt generation is refused.
type ResultStore struct {
	// results maps attempt identity to its accepted result.
	results map[resultKey]*AttemptResult
	// mtx guards results.
	mtx sync.Mutex
}

// resultKey fences one result per attempt generation per process generation.
type resultKey struct {
	accountID         string
	processGeneration uint64
	attemptGeneration uint64
}

// NewResultStore constructs an empty private result store.
func NewResultStore() *ResultStore {
	return &ResultStore{results: make(map[resultKey]*AttemptResult)}
}

// Accept stores one private result. A second result for the same account,
// process generation, and attempt generation is refused.
func (s *ResultStore) Accept(result *AttemptResult) error {
	if result == nil {
		return errors.New("core: nil result")
	}
	key := resultKey{
		accountID:         result.AccountID,
		processGeneration: result.ProcessGeneration,
		attemptGeneration: result.AttemptGeneration,
	}
	s.mtx.Lock()
	defer s.mtx.Unlock()
	if _, exists := s.results[key]; exists {
		return ErrResultAlreadyAccepted
	}
	s.results[key] = result
	return nil
}

// Lookup returns the private result for one account and attempt, or
// ErrNoResult.
func (s *ResultStore) Lookup(accountID string, processGeneration, attemptGeneration uint64) (*AttemptResult, error) {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	result, exists := s.results[resultKey{
		accountID:         accountID,
		processGeneration: processGeneration,
		attemptGeneration: attemptGeneration,
	}]
	if !exists {
		return nil, ErrNoResult
	}
	return result, nil
}
