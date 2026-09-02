package web

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// sessionTTL is the lifetime of a session since last activity.
const sessionTTL = 24 * time.Hour

// session is one server-side login session. The browser never sees the
// credential; it presents only the opaque random ID below.
type session struct {
	// accountID is the authenticated account this session belongs to.
	accountID string
	// credential is the upstream bearer credential, held server-side only
	// so the orchestrator can derive the principal on every request.
	credential string
	// expires is the wall-clock deadline for this session.
	expires time.Time
}

// sessionStore maps opaque session IDs to sessions. It is safe for
// concurrent use.
type sessionStore struct {
	mtx      sync.Mutex
	sessions map[string]*session
}

// newSessionStore constructs an empty session store.
func newSessionStore() *sessionStore {
	return &sessionStore{sessions: make(map[string]*session)}
}

// newSessionID generates a 256-bit cryptographically random session ID.
// The ID is opaque: it encodes nothing about the account or credential.
func newSessionID() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// create issues a fresh session for the credential and returns its opaque
// ID. The credential is stored server-side only.
func (s *sessionStore) create(accountID, credential string) (string, error) {
	id, err := newSessionID()
	if err != nil {
		return "", err
	}
	s.mtx.Lock()
	defer s.mtx.Unlock()
	s.sessions[id] = &session{
		accountID:  accountID,
		credential: credential,
		expires:    time.Now().Add(sessionTTL),
	}
	return id, nil
}

// get returns the live session for one opaque ID, or nil when the ID is
// unknown or expired. Expired sessions are removed lazily.
func (s *sessionStore) get(id string) *session {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return nil
	}
	if time.Now().After(sess.expires) {
		delete(s.sessions, id)
		return nil
	}
	return sess
}

// delete removes one session. The opaque ID becomes unusable immediately.
func (s *sessionStore) delete(id string) {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	delete(s.sessions, id)
}
