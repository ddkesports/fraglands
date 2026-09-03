// Package provider implements the Fraglands replay provider. It obtains
// replay bytes from an authorized ReplayStore, extracts RunbackFacts via
// s2replay at an explicit takeover tick, and hands the facts plus the
// ServerInfo-proven tick interval to core.PrepareScenario, which produces an
// honest Preview or Complete revision.
//
// The provider owns no game state. It never invents a tick interval, never
// fabricates a missing field, and never leaves a preparation in partial
// state: the outcome is exactly one of ready-with-revision or
// failed-with-typed-reason.
package provider

import (
	"context"
	"fmt"
	"io"

	"github.com/paralin/s2replay/analysis"

	"github.com/paralin/fraglands/core"
)

// MaxReplayBytes bounds one replay accepted for preparation. It protects the
// host from unbounded reads; a larger stream is a typed failure.
const MaxReplayBytes = 1 << 30

// ReplayStore is the authorized source of replay bytes. The provider never
// reads files, sockets, or Steam: the store is the only way bytes enter.
// Every request must carry a replay grant; the store (or its verifying
// decorator) refuses any request that does not present a valid one, so the
// provider can never fetch bytes without authorization.
type ReplayStore interface {
	// Replay returns a reader over the replay named by the request. A store
	// that cannot authorize the grant or find the replay returns an error;
	// the provider fails the preparation closed on any store error.
	Replay(ctx context.Context, req core.ReplayRequest) (io.ReadCloser, error)
}

// FactsExtractor extracts RunbackFacts from demo bytes at a fixed tick. The
// production implementation is analysis.ExtractRunbackFacts; tests inject
// fakes.
type FactsExtractor func(demo []byte, req analysis.RunbackRequest) (analysis.RunbackFacts, error)

// IntervalProber reports the exact seconds-per-tick proven from the demo
// bytes, or a typed error. The production implementation reads
// CSVCMsg_ServerInfo through the s2replay parser clock; tests inject fakes.
type IntervalProber func(demo []byte) (float64, error)

// Provider prepares scenarios from stored replays.
type Provider struct {
	grants  core.GrantAuthority
	store   ReplayStore
	facts   FactsExtractor
	prober  IntervalProber
	maxAge  uint32
	maxRead int64
}

// New constructs a Provider over the store. maxFreshnessTicks is the
// freshness budget committed to the compiler; zero means no budget was
// declared, which always yields Preview.
func New(grants core.GrantAuthority, store ReplayStore, facts FactsExtractor, maxFreshnessTicks uint32) (*Provider, error) {
	if grants == nil {
		// Authorization is mandatory: a provider without a grant
		// authority can never produce an authorized fetch.
		return nil, ErrGrantAuthorityRequired
	}
	if facts == nil {
		facts = analysis.ExtractRunbackFacts
	}
	return &Provider{
		grants:  grants,
		store:   store,
		facts:   facts,
		prober:  ProveTickInterval,
		maxAge:  maxFreshnessTicks,
		maxRead: MaxReplayBytes,
	}, nil
}

// Prepare implements the Preparer seam: it drives one preparation to ready
// or failed, exactly once, with no partial state. A preparation already in a
// terminal state is refused without consulting the store.
func (p *Provider) Prepare(ctx context.Context, prep *core.ScenarioPreparation) error {
	if prep != nil && prep.State().Terminal() {
		return ErrPreparationTerminal
	}
	if err := p.doPrepare(ctx, prep); err != nil {
		return p.fail(prep, err)
	}
	return nil
}

// doPrepare performs the actual work; Prepare turns any error into a typed
// preparation failure.
func (p *Provider) doPrepare(ctx context.Context, prep *core.ScenarioPreparation) error {
	if p == nil || p.store == nil {
		return ErrStoreRequired
	}
	if prep == nil {
		return ErrNilPreparation
	}
	if prep.TakeoverTick == 0 {
		return ErrTakeoverTickRequired
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// Authorization precedes every store call: the request carries the
	// preparation's private grant bound to the preparation ID and replay
	// ID. A preparation without a grant is refused before the store is
	// touched, and a store refusal of the grant fails the preparation
	// closed with the typed grant reason.
	req := prep.ReplayRequest()
	if req == nil {
		return ErrGrantRequired
	}
	rc, err := p.store.Replay(ctx, *req)
	if err != nil {
		if core.IsGrantRefusal(err) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrStoreDenied, err)
	}
	defer rc.Close()

	demo, err := p.readAll(rc)
	if err != nil {
		return err
	}

	// The tick interval and the facts must come from the same bytes: one
	// provenance path. Without a proven interval the compiler would have no
	// honest way to convert the lead-in window to ticks.
	interval, err := p.prober(demo)
	if err != nil {
		return err
	}

	facts, err := p.facts(demo, analysis.RunbackRequest{Tick: prep.TakeoverTick})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrExtractionFailed, err)
	}

	_, _, err = core.PrepareScenario(prep, facts, core.DefaultRunbackCapabilities(), p.maxAge, interval)
	return err
}

// fail marks the preparation failed with a typed reason. A preparation
// already terminal cannot be re-failed; the returned error reflects the
// original cause when marking is refused for that reason.
func (p *Provider) fail(prep *core.ScenarioPreparation, cause error) error {
	if prep == nil {
		return cause
	}
	if err := prep.MarkFailed(&core.FailureReason{
		Code:    FailureCode(cause),
		Message: cause.Error(),
	}); err != nil {
		return err
	}
	return cause
}

// readAll reads the replay bytes, refusing streams larger than the bound.
func (p *Provider) readAll(rc io.Reader) ([]byte, error) {
	limited := io.LimitReader(rc, p.maxRead+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrReplayUnreadable, err)
	}
	if int64(len(data)) > p.maxRead {
		return nil, ErrReplaySizeExceeded
	}
	return data, nil
}
