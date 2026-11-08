// Package auth implements FileToaster's login: an Argon2id admin password hash,
// an HMAC-signed httpOnly session cookie, and an HTTP middleware plus login and
// logout handlers. Auth is stateless — the cookie is signed, not stored — so the
// server keeps no session state and any cookie signed with the correct secret is
// valid until it expires.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

// Defaults and tuning constants.
const (
	// DefaultUser and DefaultPassword are the built-in admin credentials used
	// when a config file does not pin a different value.
	DefaultUser     = "admin"
	DefaultPassword = "admin"

	argon2Time    = 1
	argon2Memory  = 64 * 1024 // KiB
	argon2Threads = 4
	argon2KeyLen  = 32
	argon2SaltLen = 16

	// sessionTTL is how long an issued session cookie stays valid.
	sessionTTL = 30 * 24 * time.Hour

	// CookieName is the httpOnly cookie used for the session.
	CookieName = "filetoaster_session"
)

// errBlankAdminPassword is returned when no admin password can be resolved.
var errBlankAdminPassword = errors.New("admin password must not be blank (set FILETOASTER_ADMIN_PASSWORD or admin_password_hash in the config)")

// HashPassword returns an encoded Argon2id hash of password with a random salt.
// The returned string is self-describing (parameters + salt embedded) and can be
// stored directly in the config.
func HashPassword(password string) (string, error) {
	salt, err := randomBytes(argon2SaltLen)
	if err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)
	return encodeArgon2(argon2Time, argon2Memory, argon2Threads, salt, key), nil
}

// VerifyPassword reports whether password matches the encoded Argon2id hash. It
// runs in constant time relative to the derived key.
func VerifyPassword(encodedHash, password string) bool {
	t, m, p, salt, key, err := decodeArgon2(encodedHash)
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(key)))
	return subtle.ConstantTimeCompare(key, got) == 1
}

// encodeArgon2 serialises an Argon2id derivation into the PHC string format
// ($argon2id$v=19$m=<mem>,t=<time>,p=<threads>$<b64 salt>$<b64 key>). This
// package's x/crypto/argon2 only exposes IDKey, so encoding is done by hand.
func encodeArgon2(t, m uint32, p uint8, salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		m, t, p,
		base64.StdEncoding.EncodeToString(salt),
		base64.StdEncoding.EncodeToString(key))
}

// decodeArgon2 parses a PHC Argon2id string back into its parameters, salt, and
// derived key. It returns an error for anything that is not well formed.
func decodeArgon2(encoded string) (t, m uint32, p uint8, salt, key []byte, err error) {
	parts := strings.Split(encoded, "$")
	// Leading $ yields an empty first element: "", "argon2id", "v=19", "m..,t..,p..", salt, key.
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return 0, 0, 0, nil, nil, errors.New("invalid argon2 hash format")
	}
	m, t, p, err = parseArgon2Params(parts[3])
	if err != nil {
		return 0, 0, 0, nil, nil, err
	}
	salt, err = base64.StdEncoding.DecodeString(parts[4])
	if err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("decode salt: %w", err)
	}
	key, err = base64.StdEncoding.DecodeString(parts[5])
	if err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("decode key: %w", err)
	}
	return t, m, p, salt, key, nil
}

// parseArgon2Params parses the "m=<mem>,t=<time>,p=<threads>" params field.
func parseArgon2Params(s string) (uint32, uint32, uint8, error) {
	fields := strings.Split(s, ",")
	if len(fields) != 3 {
		return 0, 0, 0, errors.New("invalid argon2 params")
	}
	mRaw, err := parseArgon2Field(fields[0], "m")
	if err != nil {
		return 0, 0, 0, err
	}
	tRaw, err := parseArgon2Field(fields[1], "t")
	if err != nil {
		return 0, 0, 0, err
	}
	pRaw, err := parseArgon2Field(fields[2], "p")
	if err != nil {
		return 0, 0, 0, err
	}
	return uint32(mRaw), uint32(tRaw), uint8(pRaw), nil
}

// parseArgon2Field parses a "k=<n>" argon2 param into a uint64.
func parseArgon2Field(field, prefix string) (uint64, error) {
	if !strings.HasPrefix(field, prefix+"=") {
		return 0, fmt.Errorf("invalid argon2 param %q", field)
	}
	return strconv.ParseUint(field[len(prefix)+1:], 10, 64)
}

// Issue returns a signed session token for username expiring at exp.
func Issue(secret, username string, exp time.Time) (string, error) {
	payload := sessionPayload{User: username, Exp: exp.Unix()}
	raw, err := mustJSON(payload)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	sig, err := sign(secret, encoded)
	if err != nil {
		return "", err
	}
	return encoded + "." + sig, nil
}

// Parse verifies the signature of token and returns the username, its expiry,
// and whether it is still unexpired. A tampered or expired token returns ok=false.
func Parse(secret, token string) (user string, exp time.Time, ok bool) {
	i := strings.LastIndex(token, ".")
	if i < 0 {
		return "", time.Time{}, false
	}
	encoded, sig := token[:i], token[i+1:]
	want, err := sign(secret, encoded)
	if err != nil || subtle.ConstantTimeCompare([]byte(want), []byte(sig)) != 1 {
		return "", time.Time{}, false
	}
	var p sessionPayload
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", time.Time{}, false
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", time.Time{}, false
	}
	exp = time.Unix(p.Exp, 0)
	return p.User, exp, time.Now().Before(exp)
}

// SessionCookie builds the httpOnly session cookie for a signed token.
func SessionCookie(token string) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
		// Intentionally false: FileToaster runs over localhost HTTP. Serve behind
		// TLS (set Secure: true) in any production deployment.
		Secure: false,
	}
}

// sessionPayload is the signed contents of a session token.
type sessionPayload struct {
	User string `json:"user"`
	Exp  int64  `json:"exp"`
}

func sign(secret, payload string) (string, error) {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func mustJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}
