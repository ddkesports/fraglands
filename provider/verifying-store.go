package provider

import (
	"context"
	"io"

	"github.com/paralin/fraglands/core"
)

// VerifyingStore is a decorator over a ReplayStore that makes grant
// verification mandatory and blocks it from being bypassed. No request
// reaches the underlying store unless the authority accepts it, and the
// authority's Verify is one-use: the same grant can never authorize a
// second fetch. A deployment cannot opt out of authorization, because the
// provider itself refuses to construct without an authority and refuses to
// run without a verifying store.
type VerifyingStore struct {
	inner  ReplayStore
	grants core.GrantAuthority
}

// NewVerifyingStore wraps a store with mandatory grant verification.
func NewVerifyingStore(grants core.GrantAuthority, inner ReplayStore) (*VerifyingStore, error) {
	if grants == nil {
		return nil, ErrGrantAuthorityRequired
	}
	if inner == nil {
		return nil, ErrStoreRequired
	}
	return &VerifyingStore{inner: inner, grants: grants}, nil
}

// Replay verifies the presented grant first and only then consults the
// underlying store. The verification binds preparation, replay, and grant
// token: a valid grant presented for the wrong preparation or replay is
// refused, and a refused request never reaches the store.
func (s *VerifyingStore) Replay(ctx context.Context, req core.ReplayRequest) (io.ReadCloser, error) {
	if err := s.grants.Verify(req); err != nil {
		return nil, err
	}
	return s.inner.Replay(ctx, req)
}
