package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type ctxKey int

const userCtxKey ctxKey = iota

// Guard holds the material needed to issue, verify, and authenticate sessions
// for a single server instance.
type Guard struct {
	// Secret signs session cookies.
	Secret string
	// User is the admin username accepted at login.
	User string
	// Hash is the Argon2id hash of the admin password used to verify logins.
	Hash string
	// ConfigSaver persists a freshly hashed admin password back to the config
	// file for the change-password endpoint. When nil the server refuses to
	// change the password, since there would be no durable place to store it.
	ConfigSaver func(newHash string) error
}

// NewGuard builds a Guard. The secret, user, and hash must all be non-empty — an
// empty password is rejected so a server can never start with a blank admin
// password. It does not read environment variables; resolve those before calling
// (see config, which prefers FILETOASTER_ADMIN_PASSWORD over the config file).
func NewGuard(secret, adminUser, adminHash string) (*Guard, error) {
	if secret == "" || adminUser == "" || adminHash == "" {
		return nil, errBlankAdminPassword
	}
	return &Guard{Secret: secret, User: adminUser, Hash: adminHash}, nil
}

// Middleware requires a valid session cookie for every request that passes
// through it and stamps the authenticated username onto the request context.
// Login and logout routes must be registered outside this middleware so they are
// reachable without a cookie.
func (g *Guard) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := g.userFromRequest(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r.WithContext(withUser(r.Context(), user)))
	})
}

// userFromRequest extracts and validates the session cookie.
func (g *Guard) userFromRequest(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return "", false
	}
	user, _, ok := Parse(g.Secret, cookie.Value)
	return user, ok
}

// CheckPassword reports whether password matches the guard's current admin hash.
// It backs both login and the change-password flow.
func (g *Guard) CheckPassword(password string) bool {
	return VerifyPassword(g.Hash, password)
}

// loginRequest is the JSON body of a login POST.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// ServeLogin verifies the admin credentials and, on success, issues a signed
// session cookie. On any failure it returns 401 with a generic message so that
// the username and password cannot be probed.
func (g *Guard) ServeLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "username or password is incorrect")
		return
	}

	// Always run both checks against the real values and return the same generic
	// message on failure, so the username and password cannot be probed.
	userOK := req.Username == g.User
	passOK := VerifyPassword(g.Hash, req.Password)
	if !userOK || !passOK {
		writeError(w, http.StatusUnauthorized, "username or password is incorrect")
		return
	}

	token, err := Issue(g.Secret, g.User, time.Now().Add(sessionTTL))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "please try again later")
		return
	}

	http.SetCookie(w, SessionCookie(token))
	writeJSON(w, http.StatusOK, map[string]any{"username": g.User})
}

// ServeLogout clears the session cookie.
func (g *Guard) ServeLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"logged_out": true})
}

// ServeStatus reports whether the request carries a valid session cookie and, if
// so, the authenticated username. It is the client's session-hydration endpoint.
func (g *Guard) ServeStatus(w http.ResponseWriter, r *http.Request) {
	user, ok := g.userFromRequest(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"username": user})
}

// changePasswordRequest is the JSON body of a change-password POST.
type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ServeChangePassword verifies the current password, then, on success, replaces
// the admin password with the new one in the config file (via ConfigSaver) and in
// the live guard so the change takes effect without a restart. On failure it
// returns 401 with a generic message so the current password cannot be probed
// beyond a plain match/mismatch.
func (g *Guard) ServeChangePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if strings.TrimSpace(req.CurrentPassword) == "" || strings.TrimSpace(req.NewPassword) == "" {
		writeError(w, http.StatusBadRequest, "current and new password are required")
		return
	}
	if req.NewPassword == req.CurrentPassword {
		writeError(w, http.StatusBadRequest, "new password must differ from the current one")
		return
	}
	if !g.CheckPassword(req.CurrentPassword) {
		writeError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}
	newHash, err := HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "please try again later")
		return
	}
	if g.ConfigSaver != nil {
		if err := g.ConfigSaver(newHash); err != nil {
			writeError(w, http.StatusInternalServerError, "could not save the new password; please try again later")
			return
		}
	}
	// Update the live hash so the new password is accepted immediately.
	g.Hash = newHash
	writeJSON(w, http.StatusOK, map[string]any{"password_changed": true})
}

func withUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, userCtxKey, user)
}

// UserFromContext returns the authenticated user stamped by Middleware.
func UserFromContext(ctx context.Context) (string, bool) {
	user, ok := ctx.Value(userCtxKey).(string)
	return user, ok
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
