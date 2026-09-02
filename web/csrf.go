package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"sync"
)

// csrfField is the hidden form field carrying the synchronizer token.
const csrfField = "csrf_token"

// csrfStore holds one synchronizer token per session. Tokens are random,
// per-session, and compared in constant time.
type csrfStore struct {
	mtx    sync.Mutex
	tokens map[string]string
}

// newCSRFStore constructs an empty token store.
func newCSRFStore() *csrfStore {
	return &csrfStore{tokens: make(map[string]string)}
}

// newToken generates a 256-bit cryptographically random token.
func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ensure returns the token bound to the session, creating it on first use.
func (c *csrfStore) ensure(sessionID string) (string, error) {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	if tok, ok := c.tokens[sessionID]; ok {
		return tok, nil
	}
	tok, err := newToken()
	if err != nil {
		return "", err
	}
	c.tokens[sessionID] = tok
	return tok, nil
}

// verify reports whether the presented token matches the session token.
// The comparison is constant time; an unknown session never matches.
func (c *csrfStore) verify(sessionID, presented string) bool {
	c.mtx.Lock()
	tok, ok := c.tokens[sessionID]
	c.mtx.Unlock()
	if !ok || presented == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(tok), []byte(presented)) == 1
}

// drop removes the session token; called with the session on logout.
func (c *csrfStore) drop(sessionID string) {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	delete(c.tokens, sessionID)
}

// csrfTokenFor returns the CSRF token bound to the session, creating it on
// first use. The token is rendered into every state-changing form.
func (w *Web) csrfTokenFor(sessionID string) string {
	tok, err := w.csrf.ensure(sessionID)
	if err != nil {
		// Token generation failed: render without a usable form rather
		// than weakening the token requirement.
		return ""
	}
	return tok
}

// requireCSRF verifies the synchronizer token before running the handler.
// A missing, stale, or mismatched token is refused with 403 and the
// handler never runs.
func (w *Web) requireCSRF(next func(http.ResponseWriter, *http.Request, *principal)) func(http.ResponseWriter, *http.Request, *principal) {
	return func(rw http.ResponseWriter, r *http.Request, p *principal) {
		if err := r.ParseForm(); err != nil {
			w.renderError(rw, http.StatusBadRequest, "The form could not be read.")
			return
		}
		if !w.csrf.verify(p.sessionID, r.PostFormValue(csrfField)) {
			w.renderError(rw, http.StatusForbidden, "Your session has expired or the form was stale. Go back and retry.")
			return
		}
		next(rw, r, p)
	}
}
