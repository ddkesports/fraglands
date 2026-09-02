package web

import (
	"net/http"
)

// handleLogin accepts one presented credential in the login form, derives
// the account through the orchestrator identity authority, and sets the
// session cookie. A refused credential re-renders the login form with a
// typed message and never sets a cookie.
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
	if _, err := w.authenticate(r.Context(), credential); err != nil {
		w.renderLogin(rw, "Sign-in failed: the credential was refused.")
		return
	}
	http.SetCookie(rw, &http.Cookie{
		Name:     sessionCookie,
		Value:    credential,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(rw, r, "/", http.StatusSeeOther)
}

// handleLogout clears the session cookie and returns to the login form.
func (w *Web) handleLogout(rw http.ResponseWriter, r *http.Request) {
	http.SetCookie(rw, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
		SameSite: http.SameSiteLaxMode,
	})
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
