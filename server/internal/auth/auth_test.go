package auth

import (
  "bytes"
  "encoding/json"
  "errors"
  "net/http"
  "net/http/httptest"
  "strings"
  "testing"
  "time"
)

// newTestGuard returns a Guard whose admin password is "admin" so tests can log
// in with the default credentials.
func newTestGuard(t *testing.T) *Guard {
  t.Helper()
  hash, err := HashPassword(DefaultPassword)
  if err != nil {
    t.Fatalf("HashPassword: %v", err)
  }
  g, err := NewGuard("test-secret", DefaultUser, hash)
  if err != nil {
    t.Fatalf("NewGuard: %v", err)
  }
  return g
}

// readErr extracts the {"error":...} body from a response recorder.
func readErr(t *testing.T, rec *httptest.ResponseRecorder) string {
  t.Helper()
  var body struct {
    Error string `json:"error"`
  }
  if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
    t.Fatalf("decode error body: %v (body=%q)", err, rec.Body.String())
  }
  return body.Error
}

// setCookie returns the session cookie value set on the response, if any.
func setCookie(rec *httptest.ResponseRecorder) string {
  for _, c := range rec.Header().Values("Set-Cookie") {
    if strings.HasPrefix(c, CookieName+"=") {
      // Keep only the "name=value" part, before the first ";" separator.
      if i := strings.IndexByte(c, ';'); i >= 0 {
        c = c[:i]
      }
      return strings.TrimPrefix(c, CookieName+"=")
    }
  }
  return ""
}

func TestHashPasswordVerifyRoundtrip(t *testing.T) {
  hash, err := HashPassword(DefaultPassword)
  if err != nil {
    t.Fatalf("HashPassword: %v", err)
  }
  if !VerifyPassword(hash, DefaultPassword) {
    t.Fatal("correct password should verify")
  }
  if VerifyPassword(hash, "wrong-password") {
    t.Fatal("wrong password should not verify")
  }
}

func TestHashPasswordIsRandomised(t *testing.T) {
  h1, err := HashPassword(DefaultPassword)
  if err != nil {
    t.Fatal(err)
  }
  h2, err := HashPassword(DefaultPassword)
  if err != nil {
    t.Fatal(err)
  }
  if h1 == h2 {
    t.Fatal("two hashes of the same password must differ (random salt)")
  }
  // Both must still verify.
  if !VerifyPassword(h1, DefaultPassword) || !VerifyPassword(h2, DefaultPassword) {
    t.Fatal("both hashes should verify")
  }
}

func TestVerifyPasswordRejectsMalformed(t *testing.T) {
  for _, bad := range []string{"", "not-a-hash", "$argon2i$v=19$m=1$", "$$$$"} {
    if VerifyPassword(bad, DefaultPassword) {
      t.Fatalf("VerifyPassword(%q) should be false", bad)
    }
  }
}

func TestIssueParseRoundtrip(t *testing.T) {
  exp := time.Now().Add(sessionTTL)
  token, err := Issue("secret", DefaultUser, exp)
  if err != nil {
    t.Fatalf("Issue: %v", err)
  }
  user, gotExp, ok := Parse("secret", token)
  if !ok {
    t.Fatal("fresh token should parse as valid")
  }
  if user != DefaultUser {
    t.Fatalf("user = %q, want %q", user, DefaultUser)
  }
  if gotExp.Unix() != exp.Unix() {
    t.Fatalf("exp = %v, want %v", gotExp, exp)
  }
}

func TestParseRejectsTamperAndBadSecret(t *testing.T) {
  token, err := Issue("secret", DefaultUser, time.Now().Add(time.Hour))
  if err != nil {
    t.Fatal(err)
  }
  // Tamper with the signed payload.
  tampered := strings.Replace(token, ".", "_", 1)
  if _, _, ok := Parse("secret", tampered); ok {
    t.Fatal("tampered token must not verify")
  }
  // Correct token, wrong secret.
  if _, _, ok := Parse("other-secret", token); ok {
    t.Fatal("token signed with a different secret must not verify")
  }
}

func TestParseRejectsExpired(t *testing.T) {
  token, err := Issue("secret", DefaultUser, time.Now().Add(-time.Hour))
  if err != nil {
    t.Fatal(err)
  }
  if _, _, ok := Parse("secret", token); ok {
    t.Fatal("expired token must not verify")
  }
}

func TestNewGuardRejectsBlank(t *testing.T) {
  hash, err := HashPassword(DefaultPassword)
  if err != nil {
    t.Fatal(err)
  }
  cases := []struct {
    name, secret, user, adminHash string
  }{
    {"blank secret", "", DefaultUser, hash},
    {"blank user", "secret", "", hash},
    {"blank hash", "secret", DefaultUser, ""},
  }
  for _, tc := range cases {
    t.Run(tc.name, func(t *testing.T) {
      _, err := NewGuard(tc.secret, tc.user, tc.adminHash)
      if !errors.Is(err, errBlankAdminPassword) {
        t.Fatalf("NewGuard err = %v, want errBlankAdminPassword", err)
      }
    })
  }
}

func TestServeLoginSuccess(t *testing.T) {
  g := newTestGuard(t)
  body := `{"username":"admin","password":"admin"}`
  rec := httptest.NewRecorder()
  req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(body))
  g.ServeLogin(rec, req)

  if rec.Code != http.StatusOK {
    t.Fatalf("status = %d, want 200", rec.Code)
  }
  var out struct {
    Username string `json:"username"`
  }
  if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
    t.Fatalf("decode body: %v", err)
  }
  if out.Username != DefaultUser {
    t.Fatalf("username = %q, want %q", out.Username, DefaultUser)
  }
  token := setCookie(rec)
  if token == "" {
    t.Fatal("expected a Set-Cookie header with the session token")
  }
  if _, _, ok := Parse("test-secret", token); !ok {
    t.Fatal("issued cookie must be a valid signed token")
  }
}

func TestServeLoginRejectsWrongPassword(t *testing.T) {
  g := newTestGuard(t)
  req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
    bytes.NewBufferString(`{"username":"admin","password":"nope"}`))
  rec := httptest.NewRecorder()
  g.ServeLogin(rec, req)

  if rec.Code != http.StatusUnauthorized {
    t.Fatalf("status = %d, want 401", rec.Code)
  }
  if got := readErr(t, rec); got != "username or password is incorrect" {
    t.Fatalf("error = %q, want generic message", got)
  }
  if setCookie(rec) != "" {
    t.Fatal("failed login must not set a session cookie")
  }
}

// TestServeLoginDoesNotProbeCredentials ensures wrong-username and wrong-password
// fail identically so an attacker cannot tell which field was wrong.
func TestServeLoginDoesNotProbeCredentials(t *testing.T) {
  g := newTestGuard(t)
  hash, _ := HashPassword(DefaultPassword)
  _ = hash

  run := func(user, pass string) *httptest.ResponseRecorder {
    req := httptest.NewRequest(http.MethodPost, "/api/auth/login",
      bytes.NewBufferString(`{"username":`+jsonString(user)+`,"password":`+jsonString(pass)+`}`))
    rec := httptest.NewRecorder()
    g.ServeLogin(rec, req)
    return rec
  }

  wrongUser := run("attacker", DefaultPassword)     // correct password, wrong user
  wrongPass := run(DefaultUser, "definitely-wrong") // wrong password, correct user

  if wrongUser.Code != http.StatusUnauthorized || wrongPass.Code != http.StatusUnauthorized {
    t.Fatalf("both should be 401 (got %d and %d)", wrongUser.Code, wrongPass.Code)
  }
  if u, p := readErr(t, wrongUser), readErr(t, wrongPass); u != p {
    t.Fatalf("login errors differ (%q vs %q); credentials can be probed", u, p)
  }
}

func TestServeLoginMalformedBody(t *testing.T) {
  g := newTestGuard(t)
  req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader("not json"))
  rec := httptest.NewRecorder()
  g.ServeLogin(rec, req)

  if rec.Code != http.StatusBadRequest {
    t.Fatalf("status = %d, want 400", rec.Code)
  }
}

func TestServeLogoutClearsCookie(t *testing.T) {
  g := newTestGuard(t)
  rec := httptest.NewRecorder()
  g.ServeLogout(rec, httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil))

  if rec.Code != http.StatusOK {
    t.Fatalf("status = %d, want 200", rec.Code)
  }
  var out struct {
    LoggedOut bool `json:"logged_out"`
  }
  if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
    t.Fatalf("decode body: %v", err)
  }
  if !out.LoggedOut {
    t.Fatal("body should report logged_out=true")
  }
  // The cookie must be cleared: empty value with Max-Age=0 (Go serialises a
  // negative MaxAge as Max-Age=0, i.e. "delete now").
  cleared := false
  for _, c := range rec.Header().Values("Set-Cookie") {
    if strings.HasPrefix(c, CookieName+"=") {
      value := c[len(CookieName)+1:]
      if i := strings.IndexByte(value, ';'); i >= 0 {
        value = value[:i]
      }
      if value == "" && strings.Contains(c, "Max-Age=0") {
        cleared = true
      }
    }
  }
  if !cleared {
    t.Fatalf("logout must clear the cookie, got Set-Cookie=%v", rec.Header().Values("Set-Cookie"))
  }
}

func TestServeStatus(t *testing.T) {
  g := newTestGuard(t)
  token, _ := Issue("test-secret", DefaultUser, time.Now().Add(time.Hour))

  t.Run("valid cookie", func(t *testing.T) {
    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
    req.AddCookie(&http.Cookie{Name: CookieName, Value: token})
    g.ServeStatus(rec, req)

    if rec.Code != http.StatusOK {
      t.Fatalf("status = %d, want 200", rec.Code)
    }
    var out struct {
      Username string `json:"username"`
    }
    if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
      t.Fatalf("decode body: %v", err)
    }
    if out.Username != DefaultUser {
      t.Fatalf("username = %q, want %q", out.Username, DefaultUser)
    }
  })

  t.Run("missing cookie", func(t *testing.T) {
    rec := httptest.NewRecorder()
    g.ServeStatus(rec, httptest.NewRequest(http.MethodGet, "/api/auth/status", nil))
    if rec.Code != http.StatusUnauthorized {
      t.Fatalf("status = %d, want 401", rec.Code)
    }
  })
}

func TestMiddleware(t *testing.T) {
  g := newTestGuard(t)
  inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    u, ok := UserFromContext(r.Context())
    if !ok {
      w.WriteHeader(http.StatusInternalServerError)
      return
    }
    w.WriteHeader(http.StatusOK)
    _, _ = w.Write([]byte(u))
  })
  mw := g.Middleware(inner)

  t.Run("no cookie", func(t *testing.T) {
    rec := httptest.NewRecorder()
    mw.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
    if rec.Code != http.StatusUnauthorized {
      t.Fatalf("status = %d, want 401", rec.Code)
    }
  })

  t.Run("valid cookie stamps user", func(t *testing.T) {
    token, _ := Issue("test-secret", DefaultUser, time.Now().Add(time.Hour))
    rec := httptest.NewRecorder()
    req := httptest.NewRequest(http.MethodGet, "/", nil)
    req.AddCookie(&http.Cookie{Name: CookieName, Value: token})
    mw.ServeHTTP(rec, req)
    if rec.Code != http.StatusOK {
      t.Fatalf("status = %d, want 200", rec.Code)
    }
    if rec.Body.String() != DefaultUser {
      t.Fatalf("body = %q, want %q", rec.Body.String(), DefaultUser)
    }
  })
}

// jsonString quotes s as a JSON string literal for building login bodies.
func jsonString(s string) string {
  b, _ := json.Marshal(s)
  return string(b)
}

// newChangePasswordGuard returns a Guard whose admin password is "old-password"
// and whose ConfigSaver records the hash it is asked to persist.
func newChangePasswordGuard(t *testing.T) (*Guard, *string) {
  t.Helper()
  hash, err := HashPassword("old-password")
  if err != nil {
    t.Fatalf("HashPassword: %v", err)
  }
  g, err := NewGuard("test-secret", DefaultUser, hash)
  if err != nil {
    t.Fatalf("NewGuard: %v", err)
  }
  var saved string
  g.ConfigSaver = func(newHash string) error { saved = newHash; return nil }
  return g, &saved
}

func TestCheckPassword(t *testing.T) {
  g, _ := newChangePasswordGuard(t)
  if !g.CheckPassword("old-password") {
    t.Fatal("CheckPassword should accept the correct password")
  }
  if g.CheckPassword("wrong") {
    t.Fatal("CheckPassword should reject a wrong password")
  }
}

func TestServeChangePasswordSuccess(t *testing.T) {
  g, saved := newChangePasswordGuard(t)
  rec := httptest.NewRecorder()
  req := httptest.NewRequest(http.MethodPost, "/api/auth/change-password",
    bytes.NewBufferString(`{"current_password":"old-password","new_password":"new-password"}`))
  g.ServeChangePassword(rec, req)

  if rec.Code != http.StatusOK {
    t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
  }
  var out struct {
    PasswordChanged bool `json:"password_changed"`
  }
  if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
    t.Fatalf("decode body: %v", err)
  }
  if !out.PasswordChanged {
    t.Fatal("body should report password_changed=true")
  }
  if *saved == "" {
    t.Fatal("ConfigSaver must be called with the new hash")
  }
  if !VerifyPassword(g.Hash, "new-password") {
    t.Fatal("live hash must verify the new password")
  }
  if VerifyPassword(g.Hash, "old-password") {
    t.Fatal("old password must no longer verify after a change")
  }
}

func TestServeChangePasswordRejectsWrongCurrent(t *testing.T) {
  g, saved := newChangePasswordGuard(t)
  rec := httptest.NewRecorder()
  req := httptest.NewRequest(http.MethodPost, "/api/auth/change-password",
    bytes.NewBufferString(`{"current_password":"nope","new_password":"new-password"}`))
  g.ServeChangePassword(rec, req)

  if rec.Code != http.StatusUnauthorized {
    t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
  }
  if readErr(t, rec) != "current password is incorrect" {
    t.Fatalf("unexpected error body: %s", rec.Body.String())
  }
  if *saved != "" {
    t.Fatal("ConfigSaver must not run when the current password is wrong")
  }
  if VerifyPassword(g.Hash, "new-password") {
    t.Fatal("hash must be unchanged after a rejected change")
  }
}

func TestServeChangePasswordValidation(t *testing.T) {
  g, saved := newChangePasswordGuard(t)
  cases := []struct {
    name, body string
    wantStatus int
  }{
    {"empty current", `{"current_password":"","new_password":"x"}`, http.StatusBadRequest},
    {"empty new", `{"current_password":"old-password","new_password":""}`, http.StatusBadRequest},
    {"same as current", `{"current_password":"old-password","new_password":"old-password"}`, http.StatusBadRequest},
    {"malformed body", `not-json`, http.StatusBadRequest},
  }
  for _, tc := range cases {
    t.Run(tc.name, func(t *testing.T) {
      rec := httptest.NewRecorder()
      g.ServeChangePassword(rec, httptest.NewRequest(http.MethodPost, "/api/auth/change-password", strings.NewReader(tc.body)))
      if rec.Code != tc.wantStatus {
        t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
      }
      if *saved != "" {
        t.Fatal("ConfigSaver must not run on a validation failure")
      }
    })
  }
}

func TestServeChangePasswordWithoutSaverStillUpdates(t *testing.T) {
  hash, err := HashPassword("old-password")
  if err != nil {
    t.Fatal(err)
  }
  g, err := NewGuard("test-secret", DefaultUser, hash)
  if err != nil {
    t.Fatal(err)
  }
  // ConfigSaver is nil: the change must still update the live hash.
  rec := httptest.NewRecorder()
  g.ServeChangePassword(rec, httptest.NewRequest(http.MethodPost, "/api/auth/change-password",
    bytes.NewBufferString(`{"current_password":"old-password","new_password":"new-password"}`)))
  if rec.Code != http.StatusOK {
    t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
  }
  if !VerifyPassword(g.Hash, "new-password") {
    t.Fatal("hash should update in memory even without a saver")
  }
}
