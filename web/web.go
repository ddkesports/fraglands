// Package web serves the minimal private Fraglands web console over stdlib
// net/http and html/template. It is a thin session adapter in front of the
// authenticated orchestrator API surface: every rule lives in the
// orchestrator and core packages, and every identity is derived from the
// presented credential by the injected IdentityAuthority, never from a form
// value or query string.
//
// The browser never holds the upstream credential. Login exchanges the
// presented credential for a cryptographically random opaque session ID
// stored server-side; the cookie carries only that opaque ID. Every POST
// form carries a per-session synchronizer CSRF token verified in constant
// time, and Origin is validated on every state-changing request. The cookie
// is HttpOnly, SameSite=Lax, and Secure (configurable for loopback dev).
package web

import (
	"embed"
	"html/template"
	"net"
	"net/http"
	"strings"

	"github.com/paralin/fraglands/core"
	"github.com/paralin/fraglands/orchestrator"
)

// templateFS embeds the server-rendered page templates.
//
//go:embed templates/*.html
var templateFS embed.FS

// sessionCookie is the browser cookie carrying the opaque session ID.
const sessionCookie = "fraglands_session"

// Web serves the private Fraglands web console.
type Web struct {
	// orch is the orchestrator the console operates through.
	orch *orchestrator.Orchestrator
	// templates are the parsed server-rendered pages.
	templates *template.Template
	// sessions maps opaque session IDs to credentials, server-side only.
	sessions *sessionStore
	// csrf holds one synchronizer token per session.
	csrf *csrfStore
	// secureCookies forces the Secure attribute on every session cookie.
	// When nil, Secure is derived per request: on unless the request
	// arrived from a loopback address. Production deployments set it
	// explicitly.
	secureCookies *bool
}

// NewWeb constructs the console over one orchestrator and parses the
// embedded templates once at startup.
func NewWeb(orch *orchestrator.Orchestrator) (*Web, error) {
	templates, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Web{
		orch:      orch,
		templates: templates,
		sessions:  newSessionStore(),
		csrf:      newCSRFStore(),
	}, nil
}

// SetSecureCookies forces the Secure attribute on every session cookie.
// Production deployments serving non-loopback traffic must call this (or
// terminate TLS in front) before serving.
func (w *Web) SetSecureCookies(secure bool) {
	w.secureCookies = &secure
}

// sessionCookieFor builds the session cookie with the given opaque ID.
// Secure is set when explicitly configured, or when the remote address is
// not a loopback listener (development convenience).
func (w *Web) sessionCookieFor(sessionID string, r *http.Request) *http.Cookie {
	secure := false
	if w.secureCookies != nil {
		secure = *w.secureCookies
	} else {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			secure = true
		} else {
			ip := net.ParseIP(host)
			secure = ip == nil || !ip.IsLoopback()
		}
	}
	return &http.Cookie{
		Name:     sessionCookie,
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// clearSessionCookie removes the session cookie from the browser.
func (w *Web) clearSessionCookie(rw http.ResponseWriter) {
	http.SetCookie(rw, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// checkOrigin validates the Origin (or Referer as fallback) header for
// state-changing requests. Same-origin requests pass. Requests with no
// Origin header are allowed (direct navigation, non-browser clients).
// Cross-origin requests are refused.
func (w *Web) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// No Origin header: could be same-origin form post from older
		// browsers. Fall back to Referer.
		referer := r.Header.Get("Referer")
		if referer == "" {
			return true
		}
		origin = referer
	}
	// Extract scheme://host from Origin and compare against the request's
	// own host.
	reqHost := r.Host
	if idx := strings.Index(origin, "://"); idx >= 0 {
		rest := origin[idx+3:]
		if slash := strings.Index(rest, "/"); slash >= 0 {
			rest = rest[:slash]
		}
		return rest == reqHost
	}
	return false
}

// Handler returns the console routes. Every state-changing POST verifies
// Origin and the session's CSRF token; every authenticated route derives
// the principal from the opaque session ID before any handler runs.
func (w *Web) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", w.requirePrincipal(w.handleHome))
	mux.HandleFunc("POST /login", w.handleLogin)
	mux.HandleFunc("POST /logout", w.requireSessionCSRF(w.handleLogout))
	mux.HandleFunc("POST /preparations", w.requireSessionCSRF(w.handlePrepare))
	mux.HandleFunc("GET /preparations/{id}", w.requirePrincipal(w.handlePreparation))
	mux.HandleFunc("POST /preparations/{id}/slots", w.requireSessionCSRF(w.handleClaim))
	mux.HandleFunc("POST /preparations/{id}/invite", w.requireSessionCSRF(w.handleInvite))
	mux.HandleFunc("POST /preparations/{id}/slots/release", w.requireSessionCSRF(w.handleRelease))
	mux.HandleFunc("GET /debrief", w.requirePrincipal(w.handleDebrief))
	return mux
}

// requireSessionCSRF wraps a handler with session resolution, Origin
// validation, and CSRF token verification. This is the wrapper for every
// state-changing POST route.
func (w *Web) requireSessionCSRF(next func(http.ResponseWriter, *http.Request, *principal)) http.HandlerFunc {
	return w.requirePrincipal(func(rw http.ResponseWriter, r *http.Request, p *principal) {
		if !w.checkOrigin(r) {
			w.renderError(rw, http.StatusForbidden, "Request refused: cross-origin request.")
			return
		}
		w.requireCSRF(next)(rw, r, p)
	})
}

// requirePrincipal resolves the opaque session cookie into the session
// principal and passes it to the handler, or renders the login form. The
// credential never leaves the server: the cookie carries only the opaque
// session ID, and the account is derived server-side by the injected
// authority.
func (w *Web) requirePrincipal(next func(http.ResponseWriter, *http.Request, *principal)) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil || cookie.Value == "" {
			w.renderLogin(rw, "")
			return
		}
		sess := w.sessions.get(cookie.Value)
		if sess == nil {
			// Unknown or expired session: clear the stale cookie.
			w.clearSessionCookie(rw)
			w.renderLogin(rw, "Your session has expired. Please sign in again.")
			return
		}
		account, err := w.orch.Authenticate(r.Context(), sess.credential)
		if err != nil {
			// The upstream credential is no longer valid; destroy the session.
			w.destroySession(cookie.Value)
			w.clearSessionCookie(rw)
			w.renderLogin(rw, "Sign-in failed: the credential was refused.")
			return
		}
		next(rw, r, &principal{
			account:    account,
			credential: sess.credential,
			sessionID:  cookie.Value,
		})
	}
}

// destroySession removes the session mapping and its CSRF token.
func (w *Web) destroySession(sessionID string) {
	w.sessions.delete(sessionID)
	w.csrf.drop(sessionID)
}

// principal is the session-bound authenticated account.
type principal struct {
	account    *core.Account
	credential string
	sessionID  string
}
