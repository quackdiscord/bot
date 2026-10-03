package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"slices"
	"strings"
)

const csrfHeader = "X-CSRF-Token"

// csrf protects cookie-authenticated writes. A write that carries the session
// cookie must come from an allowed Origin and echo the CSRF cookie in the
// X-CSRF-Token header (double submit). Bearer-authenticated callers are not
// browsers and skip the check.
func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := cookieValue(r, s.cfg.Auth.SessionCookieName); !ok || !isWrite(r.Method) || hasBearerCredential(r) {
			next.ServeHTTP(w, r)
			return
		}
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" || !slices.Contains(s.cfg.API.CORSOrigins, origin) {
			writeError(w, r, http.StatusForbidden, codeCSRF, "CSRF validation failed")
			return
		}
		cookie, _ := cookieValue(r, s.cfg.Auth.CSRFCookieName)
		header := strings.TrimSpace(r.Header.Get(csrfHeader))
		if cookie == "" || header == "" || !secretsEqual(cookie, header) {
			writeError(w, r, http.StatusForbidden, codeCSRF, "CSRF validation failed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// newCSRFToken returns the random challenge for the double-submit check. It
// is not a secret from the browser; it only proves same-origin JavaScript.
func newCSRFToken() (string, error) {
	var body [32]byte
	if _, err := rand.Read(body[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(body[:]), nil
}

// secretsEqual compares shared secrets in constant time.
func secretsEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func isWrite(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func hasBearerCredential(r *http.Request) bool {
	parts := strings.Fields(r.Header.Get("Authorization"))
	return len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && parts[1] != ""
}
