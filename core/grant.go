package core

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"sync"
	"time"
)

// This file defines the replay authorization grant contract: one opaque,
// single-use, cryptographically bound authorization to fetch one replay's
// bytes for exactly one preparation. The orchestrator mints the grant only
// after authenticated catalog acceptance; the provider presents it to the
// ReplayStore, whose verifying decorator refuses every request that does not
// bind the same preparation, replay, and grant. The grant is never
// serialized by any surface: MarshalJSON always refuses and String always
// redacts.

// Typed grant refusals. Each is a stable, comparable error: expiry and
// revocation are never conflated with mismatch or reuse.
var (
	// ErrGrantUnknown is returned when the presented grant token is empty or
	// was never minted.
	ErrGrantUnknown = errors.New("core: replay grant unknown")
	// ErrGrantMismatch is returned when the presented preparation or replay
	// identity does not match the grant's minted binding. Substituting a
	// grant across preparations or replays is always this refusal.
	ErrGrantMismatch = errors.New("core: replay grant does not bind the presented preparation and replay")
	// ErrGrantRevoked is returned when the grant was revoked before use.
	ErrGrantRevoked = errors.New("core: replay grant revoked")
	// ErrGrantAlreadyUsed is returned when a one-use grant is presented a
	// second time.
	ErrGrantAlreadyUsed = errors.New("core: replay grant already used")
	// ErrGrantExpired is returned when the grant is presented after its
	// expiry, measured by the injected clock.
	ErrGrantExpired = errors.New("core: replay grant expired")
	// ErrGrantInvalidBinding is returned when a mint is requested without a
	// complete binding.
	ErrGrantInvalidBinding = errors.New("core: replay grant binding incomplete")
	// ErrGrantAlreadyMinted is returned when a second grant is minted for
	// one preparation.
	ErrGrantAlreadyMinted = errors.New("core: replay grant already minted for preparation")
	// ErrGrantClockRequired is returned when a grant authority is
	// constructed without an injected clock.
	ErrGrantClockRequired = errors.New("core: grant authority requires an injected clock")
	// ErrGrantTTLRequired is returned when a grant authority is constructed
	// without a configured positive TTL. No default is ever guessed.
	ErrGrantTTLRequired = errors.New("core: grant authority requires a configured positive ttl")
	// errGrantNotSerializable is returned by ReplayGrant.MarshalJSON: the
	// grant must never cross a serialization boundary.
	errGrantNotSerializable = errors.New("core: replay grant is never serialized")
)

// IsGrantRefusal reports whether the error is one of the typed grant
// refusals. Callers use it to keep grant refusals typed through wrapping.
func IsGrantRefusal(err error) bool {
	switch {
	case errors.Is(err, ErrGrantUnknown),
		errors.Is(err, ErrGrantMismatch),
		errors.Is(err, ErrGrantRevoked),
		errors.Is(err, ErrGrantAlreadyUsed),
		errors.Is(err, ErrGrantExpired):
		return true
	default:
		return false
	}
}

// ReplayGrant is one minted authorization. It is immutable after minting:
// every field is fixed by the authority at mint time and bound into the
// token's cryptographic MAC. The token itself is unexported so the grant
// cannot be constructed or retokened outside the authority.
type ReplayGrant struct {
	// preparationID is the only preparation the grant admits.
	preparationID string
	// ownerAccountID is the authenticated account the grant was minted for.
	ownerAccountID string
	// replayID is the only replay the grant admits.
	replayID string
	// expires is the absolute expiry instant.
	expires time.Time
	// token is the opaque cryptographic grant presented to the store.
	token string
}

// PreparationID returns the preparation the grant is bound to.
func (g *ReplayGrant) PreparationID() string {
	if g == nil {
		return ""
	}
	return g.preparationID
}

// OwnerAccountID returns the account the grant was minted for.
func (g *ReplayGrant) OwnerAccountID() string {
	if g == nil {
		return ""
	}
	return g.ownerAccountID
}

// ReplayID returns the replay the grant is bound to.
func (g *ReplayGrant) ReplayID() string {
	if g == nil {
		return ""
	}
	return g.replayID
}

// ExpiresAt returns the absolute expiry instant.
func (g *ReplayGrant) ExpiresAt() time.Time {
	if g == nil {
		return time.Time{}
	}
	return g.expires
}

// Token returns the opaque grant token presented to the ReplayStore. The
// only intended consumer is the provider's authorized store request.
func (g *ReplayGrant) Token() string {
	if g == nil {
		return ""
	}
	return g.token
}

// MarshalJSON always refuses: the grant is never serialized by any surface.
func (g *ReplayGrant) MarshalJSON() ([]byte, error) {
	return nil, errGrantNotSerializable
}

// String redacts the grant: log lines can never carry the token.
func (g *ReplayGrant) String() string {
	return "replay-grant(redacted)"
}

// ReplayRequest is the authorized request the provider presents to the
// ReplayStore. Verification binds all three fields: the same grant cannot be
// substituted across preparations or replays.
type ReplayRequest struct {
	// PreparationID is the preparation the bytes are fetched for.
	PreparationID string
	// ReplayID is the replay identity requested.
	ReplayID string
	// Grant is the opaque grant token.
	Grant string
}

// GrantAuthority mints, verifies, and revokes replay grants. The
// orchestrator depends on Mint and Revoke; the provider's verifying store
// depends on Verify. One deployment wires one authority into both sides.
type GrantAuthority interface {
	// Mint mints one grant bound to the preparation, owner account, and
	// replay, expiring by the authority's configured TTL.
	Mint(preparationID, ownerAccountID, replayID string) (*ReplayGrant, error)
	// Verify consumes the presented grant atomically. A refused verify
	// never consumes.
	Verify(req ReplayRequest) error
	// Revoke revokes the grant minted for the preparation. Revoking an
	// unknown or already-consumed preparation changes nothing.
	Revoke(preparationID string) error
}

// GrantAuthorityConfig is the required configuration of the HMAC grant
// authority. The clock is injected and the TTL is configured: neither is
// ever defaulted.
type GrantAuthorityConfig struct {
	// Clock returns the current instant. Required.
	Clock func() time.Time
	// TTL is the positive lifetime of a minted grant. Required.
	TTL time.Duration
	// Key is the HMAC key. When nil, a random key is generated at
	// construction.
	Key []byte
}

// GrantAuthority is the concrete HMAC-backed authority. Grants are bound
// with HMAC-SHA256 over the full binding, so a token is opaque and
// unforgeable, and verification consumes one use under one lock.
type HMACGrantAuthority struct {
	clock func() time.Time
	ttl   time.Duration
	key   []byte

	// mtx guards the maps and the one-use consumption flag below.
	mtx sync.Mutex
	// grants maps token to its minted record.
	grants map[string]*grantRecord
	// byPreparation maps preparation ID to its grant token.
	byPreparation map[string]string
}

// grantRecord is the minted state of one grant.
type grantRecord struct {
	preparationID  string
	ownerAccountID string
	replayID       string
	expires        time.Time
	used           bool
	revoked        bool
}

// NewHMACGrantAuthority constructs the authority. Construction fails without an
// injected clock or without a configured positive TTL: there is no guessed
// default.
func NewHMACGrantAuthority(cfg GrantAuthorityConfig) (*HMACGrantAuthority, error) {
	if cfg.Clock == nil {
		return nil, ErrGrantClockRequired
	}
	if cfg.TTL <= 0 {
		return nil, ErrGrantTTLRequired
	}
	key := cfg.Key
	if len(key) == 0 {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, errors.New("core: grant authority key generation failed")
		}
	}
	return &HMACGrantAuthority{
		clock:         cfg.Clock,
		ttl:           cfg.TTL,
		key:           key,
		grants:        make(map[string]*grantRecord),
		byPreparation: make(map[string]string),
	}, nil
}

// Mint mints one grant bound atomically to the preparation, owner account,
// replay, and expiry. The token is base64(nonce) || "." ||
// base64(HMAC-SHA256(key, binding)); the MAC covers the full binding so a
// token cannot be split from its preparation, owner, replay, or expiry.
func (a *HMACGrantAuthority) Mint(preparationID, ownerAccountID, replayID string) (*ReplayGrant, error) {
	if preparationID == "" || ownerAccountID == "" || replayID == "" {
		return nil, ErrGrantInvalidBinding
	}
	a.mtx.Lock()
	defer a.mtx.Unlock()
	if _, exists := a.byPreparation[preparationID]; exists {
		return nil, ErrGrantAlreadyMinted
	}

	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, errors.New("core: grant nonce generation failed")
	}
	expires := a.clock().Add(a.ttl)
	var expiry [8]byte
	binary.LittleEndian.PutUint64(expiry[:], uint64(expires.UnixNano()))
	mac := hmac.New(sha256.New, a.key)
	mac.Write([]byte("fraglands-replay-grant\x00"))
	mac.Write([]byte(preparationID))
	mac.Write([]byte("\x00"))
	mac.Write([]byte(ownerAccountID))
	mac.Write([]byte("\x00"))
	mac.Write([]byte(replayID))
	mac.Write([]byte("\x00"))
	mac.Write(expiry[:])

	token := base64.RawURLEncoding.EncodeToString(nonce) +
		"." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	a.grants[token] = &grantRecord{
		preparationID:  preparationID,
		ownerAccountID: ownerAccountID,
		replayID:       replayID,
		expires:        expires,
	}
	a.byPreparation[preparationID] = token

	return &ReplayGrant{
		preparationID:  preparationID,
		ownerAccountID: ownerAccountID,
		replayID:       replayID,
		expires:        expires,
		token:          token,
	}, nil
}

// Verify consumes the presented grant atomically: the binding check, the
// revocation and reuse checks, the expiry check against the injected clock,
// and the one-use consumption happen under one lock. A refused verify never
// consumes. Refusals are typed and ordered: unknown, mismatch, revoked,
// already used, expired.
func (a *HMACGrantAuthority) Verify(req ReplayRequest) error {
	a.mtx.Lock()
	defer a.mtx.Unlock()
	if req.Grant == "" {
		return ErrGrantUnknown
	}
	rec, ok := a.grants[req.Grant]
	if !ok {
		return ErrGrantUnknown
	}
	if req.PreparationID != rec.preparationID || req.ReplayID != rec.replayID {
		return ErrGrantMismatch
	}
	if rec.revoked {
		return ErrGrantRevoked
	}
	if rec.used {
		return ErrGrantAlreadyUsed
	}
	if !a.clock().Before(rec.expires) {
		return ErrGrantExpired
	}
	rec.used = true
	return nil
}

// Revoke revokes the grant minted for the preparation. Revoking an unknown
// preparation or an already-consumed grant changes nothing: the grant is
// already unusable.
func (a *HMACGrantAuthority) Revoke(preparationID string) error {
	a.mtx.Lock()
	defer a.mtx.Unlock()
	token, ok := a.byPreparation[preparationID]
	if !ok {
		return nil
	}
	rec := a.grants[token]
	if rec.used {
		return nil
	}
	rec.revoked = true
	return nil
}
