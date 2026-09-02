// Package web serves the minimal private Fraglands web console over stdlib
// net/http and html/template. It is a thin session adapter in front of the
// authenticated orchestrator API surface: every rule lives in the
// orchestrator and core packages, and every identity is derived from the
// presented credential by the injected IdentityAuthority, never from a form
// value or query string.
//
// The console is private and server-rendered. It covers replay selection with
// timecode input, preparation status with honest Reconstruction Preview
// omission display, lobby slot claim and release, and a private debrief shown
// side-by-side with explicit source labels and exactly one next action.
package web

import (
	"context"
	"embed"
	"html/template"
	"net/http"

	"github.com/paralin/fraglands/core"
	"github.com/paralin/fraglands/orchestrator"
)

// templateFS embeds the server-rendered page templates.
//
//go:embed templates/*.html
var templateFS embed.FS

// sessionCookie is the browser cookie carrying the presented credential.
const sessionCookie = "fraglands_session"

// Web serves the private Fraglands web console.
type Web struct {
	// orch is the orchestrator the console operates through.
	orch *orchestrator.Orchestrator
	// templates are the parsed server-rendered pages.
	templates *template.Template
}

// NewWeb constructs the console over one orchestrator and parses the
// embedded templates once at startup.
func NewWeb(orch *orchestrator.Orchestrator) (*Web, error) {
	templates, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Web{orch: orch, templates: templates}, nil
}

// Handler returns the console routes. Every route requires a session; the
// middleware derives the principal from the session credential before any
// handler runs.
func (w *Web) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", w.requirePrincipal(w.handleHome))
	mux.HandleFunc("POST /login", w.handleLogin)
	mux.HandleFunc("POST /logout", w.handleLogout)
	mux.HandleFunc("POST /preparations", w.requirePrincipal(w.handlePrepare))
	mux.HandleFunc("GET /preparations/{id}", w.requirePrincipal(w.handlePreparation))
	mux.HandleFunc("POST /preparations/{id}/slots", w.requirePrincipal(w.handleClaim))
	mux.HandleFunc("POST /preparations/{id}/slots/release", w.requirePrincipal(w.handleRelease))
	mux.HandleFunc("GET /debrief", w.requirePrincipal(w.handleDebrief))
	return mux
}

// requirePrincipal resolves the session cookie into the orchestrator
// principal and stores it on the request context, or redirects to the login
// form. The credential never leaves the server: the cookie is HttpOnly and
// the account is derived server-side by the injected authority.
func (w *Web) requirePrincipal(next func(http.ResponseWriter, *http.Request, *principal)) http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil || cookie.Value == "" {
			w.renderLogin(rw, "")
			return
		}
		principal, err := w.authenticate(r.Context(), cookie.Value)
		if err != nil {
			w.renderLogin(rw, "Sign-in failed: the credential was refused.")
			return
		}
		next(rw, r, principal)
	}
}

// authenticate derives the principal for one session credential.
func (w *Web) authenticate(ctx context.Context, credential string) (*principal, error) {
	account, err := w.orch.Authenticate(ctx, credential)
	if err != nil {
		return nil, err
	}
	return &principal{account: account, credential: credential}, nil
}

// principal is the session-bound authenticated account.
type principal struct {
	account    *core.Account
	credential string
}
