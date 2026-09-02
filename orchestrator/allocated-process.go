package orchestrator

import (
	"context"

	"github.com/aperturerobotics/util/broadcast"
)

// AllocatedProcess is one server process generation allocated for a ready
// revision. Readiness is explicit: the process is not usable until the
// allocator records the readiness evidence.
type AllocatedProcess struct {
	// Generation is the server process generation.
	Generation uint64
	// ConnectAddress is the address clients join.
	ConnectAddress string

	// bcast guards ready and evidence below.
	bcast broadcast.Broadcast
	// ready records that the allocator proved the process ready.
	ready bool
	// evidence is the readiness fact the allocator recorded.
	evidence string
}

// MarkReady records the readiness evidence and wakes waiters. It returns
// false when readiness was already recorded.
func (p *AllocatedProcess) MarkReady(evidence string) bool {
	var marked bool
	p.bcast.HoldLock(func(broadcast func(), _ func() <-chan struct{}) {
		if p.ready {
			return
		}
		p.ready = true
		p.evidence = evidence
		marked = true
		broadcast()
	})
	return marked
}

// Ready reports whether readiness was proven.
func (p *AllocatedProcess) Ready() bool {
	var ready bool
	p.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		ready = p.ready
	})
	return ready
}

// Evidence returns the readiness fact the allocator recorded, or an empty
// string before readiness is proven.
func (p *AllocatedProcess) Evidence() string {
	var evidence string
	p.bcast.HoldLock(func(_ func(), _ func() <-chan struct{}) {
		evidence = p.evidence
	})
	return evidence
}

// WaitReady waits until readiness is proven. It cannot miss the transition:
// the readiness read and the wait channel are obtained in the same HoldLock.
func (p *AllocatedProcess) WaitReady(ctx context.Context) error {
	for {
		var ready bool
		var waitCh <-chan struct{}
		p.bcast.HoldLock(func(_ func(), getWaitCh func() <-chan struct{}) {
			ready = p.ready
			waitCh = getWaitCh()
		})
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-waitCh:
		}
	}
}
