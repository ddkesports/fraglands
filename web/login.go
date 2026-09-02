package web

import (
	"net/http"
)

// handleLogin accepts one presented credential in the login form, derives
// the account through the orchestrator identity authority, and exchanges
// the credential for a fresh opaque session ID. The upstream credential is
// never stored in the browser: the cookie carries only the opaque session
// ID, whose mapping lives server-side. Every successful login rotates the
// session: a new opaque ID is issued and any prior session is destroyed,
// so a replayed or fixed cookie value cannot outlive the exchange.
func (w *Web) handleLogin(rw http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		w.renderLogin(rw, "Sign-in failed: the form could not be read.")
		return
	}
	credential := r.PostFormValue("credential")
	if credential == "" {
		w.renderLogin(rw, "Enter your credential.")
		return
	}
	if !w.checkOrigin(r) {
		w.renderError(rw, http.StatusForbidden, "Sign-in refused: cross-origin request.")
		return
	}
	account, err := w.orch.Authenticate(r.Context(), credential)
	if err != nil {
		w.renderLogin(rw, "Sign-in failed: the credential was refused.")
		return
	}

	// Rotate: destroy any session the request already presented (fixation
	// defense) before issuing the fresh opaque ID.
	if prior, err := r.Cookie(sessionCookie); err == nil && prior.Value != "" {
		w.destroySession(prior.Value)
	}
	sessionID, err := w.sessions.create(account.ID, credential)
	if err != nil {
		w.renderError(rw, http.StatusInternalServerError, "Sign-in failed. Try again.")
		return
	}

	http.SetCookie(rw, w.sessionCookieFor(sessionID, r))
	http.Redirect(rw, r, "/", http.StatusSeeOther)
}

// handleLogout destroys the server-side session and its CSRF token, clears
// the cookie, and returns to the login form. The opaque ID is unusable
// afterwards even if the cookie value is replayed.
func (w *Web) handleLogout(rw http.ResponseWriter, r *http.Request, p *principal) {
	w.destroySession(p.sessionID)
	w.clearSessionCookie(rw)
	http.Redirect(rw, r, "/", http.StatusSeeOther)
}

// renderLogin renders the login form with an optional message.
func (w *Web) renderLogin(rw http.ResponseWriter, message string) {
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.WriteHeader(http.StatusUnauthorized)
	_ = w.templates.ExecuteTemplate(rw, "login.html", map[string]any{
		"Message": message,
	})
}
