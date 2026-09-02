// Package orchestrator coordinates the minimal Fraglands path: scenario
// preparation, server process allocation, lobby slot claims, one-use join
// intents, and private results. It owns no game state and never interprets
// replay content: the injected Preparer builds revisions, and the injected
// ProcessAllocator starts server processes and proves their readiness.
package orchestrator

import (
	"context"

	"github.com/aperturerobotics/util/broadcast"
	"github.com/paralin/fraglands/core"
)

// Orchestrator coordinates one region's minimal Fraglands path. It is safe
// for concurrent use.
type Orchestrator struct {
	// preparer runs each accepted preparation to a terminal state.
	preparer Preparer
	// allocator starts one server process per ready revision.
	allocator ProcessAllocator
	// ctx is the orchestrator lifetime: preparation and allocation watchers
	// stop when it is cancelled.
	ctx context.Context

	// bcast guards the counters and maps below.
	bcast broadcast.Broadcast
	// prepSeq numbers preparation identifiers.
	prepSeq int
	// intentSeq numbers join intent identifiers.
	intentSeq int
	// preparations maps preparation ID to its lifecycle record.
	preparations map[string]*core.ScenarioPreparation
	// lobbies maps preparation ID to its lobby.
	lobbies map[string]*core.Lobby
	// processes maps preparation ID to its allocated server process.
	processes map[string]*AllocatedProcess
	// allocFailures maps preparation ID to the typed reason its process
	// could not be allocated.
	allocFailures map[string]*AllocationFailure
	// intents maps join intent ID to the intent.
	intents map[string]*core.JoinIntent
	// results keeps the private attempt results.
	results *core.ResultStore
	// sources is the private replay selection catalog.
	sources []core.ReplaySource
}

// NewOrchestrator constructs an orchestrator over the given replay
// selection catalog. The context is the orchestrator lifetime: preparation
// and allocation watchers stop when it is cancelled.
func NewOrchestrator(
	ctx context.Context,
	sources []core.ReplaySource,
	preparer Preparer,
	allocator ProcessAllocator,
) *Orchestrator {
	return &Orchestrator{
		ctx:           ctx,
		preparer:      preparer,
		allocator:     allocator,
		preparations:  make(map[string]*core.ScenarioPreparation),
		lobbies:       make(map[string]*core.Lobby),
		processes:     make(map[string]*AllocatedProcess),
		allocFailures: make(map[string]*AllocationFailure),
		intents:       make(map[string]*core.JoinIntent),
		results:       core.NewResultStore(),
		sources:       sources,
	}
}
