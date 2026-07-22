package httpserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const csrfCookieName = "rionlyu_csrf"

func (s *Server) ensureCSRF(w http.ResponseWriter, r *http.Request) (string, error) {
	if cookie, ok := s.validCSRFCookie(r); ok {
		return cookie.Value, nil
	}
	return s.rotateCSRF(w)
}

func (s *Server) validCSRFCookie(r *http.Request) (*http.Cookie, bool) {
	cookie, err := r.Cookie(csrfCookieName)
	return cookie, err == nil && s.validCSRFToken(cookie.Value)
}

func (s *Server) rotateCSRF(w http.ResponseWriter) (string, error) {
	nonce, err := s.randomString(32)
	if err != nil {
		return "", fmt.Errorf("create CSRF token: %w", err)
	}
	signature := s.signCSRF(nonce)
	token := nonce + "." + signature
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/contact",
		MaxAge:   int((2 * time.Hour).Seconds()),
		Expires:  s.now().Add(2 * time.Hour),
		HttpOnly: true,
		Secure:   s.config.IsProduction(),
		SameSite: http.SameSiteStrictMode,
	})
	return token, nil
}

func (s *Server) validateCSRF(r *http.Request, submitted string) bool {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || !s.validCSRFToken(cookie.Value) || !s.validCSRFToken(submitted) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(submitted)) == 1
}

func (s *Server) validCSRFToken(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return false
	}
	nonce, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(nonce) != 32 {
		return false
	}
	want := s.signCSRF(parts[0])
	return subtle.ConstantTimeCompare([]byte(parts[1]), []byte(want)) == 1
}

func (s *Server) signCSRF(nonce string) string {
	mac := hmac.New(sha256.New, s.config.CSRFSecret)
	_, _ = mac.Write([]byte(nonce))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) randomString(byteCount int) (string, error) {
	data := make([]byte, byteCount)
	if _, err := io.ReadFull(s.random, data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
