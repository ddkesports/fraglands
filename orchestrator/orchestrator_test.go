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
