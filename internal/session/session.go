// Package session implements a small cookie-based login layer on top of
// PocketBase auth collections, used for the two HTML back offices
// (/admin and /app). It deliberately reuses PocketBase's own auth token
// issuing/validation instead of inventing a parallel signing scheme.
package session

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"momopay/internal/payid"
)

var ErrInvalidCredentials = errors.New("session: invalid email or password")

const cookieMaxAge = int((7 * 24 * time.Hour) / time.Second)

// secureCookies controls the Secure flag on session/CSRF cookies. Disabled
// by default so local development over plain HTTP works; set
// COOKIE_SECURE=true when deploying behind HTTPS.
func secureCookies() bool {
	return os.Getenv("COOKIE_SECURE") == "true"
}

// Store manages login/logout/session-resolution for a single auth
// collection (e.g. "admins" or "merchants").
type Store struct {
	App            core.App
	CollectionName string
	CookieName     string
	CSRFCookieName string
}

// Login validates credentials and, on success, sets the session + CSRF
// cookies on the response and returns the authenticated record.
func (s *Store) Login(w http.ResponseWriter, email, password string) (*core.Record, error) {
	record, err := s.App.FindAuthRecordByEmail(s.CollectionName, email)
	if err != nil || !record.ValidatePassword(password) {
		return nil, ErrInvalidCredentials
	}

	token, err := record.NewAuthToken()
	if err != nil {
		return nil, err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     s.CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   cookieMaxAge,
	})

	csrfToken, err := payid.GenerateCSRFToken()
	if err != nil {
		return nil, err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     s.CSRFCookieName,
		Value:    csrfToken,
		Path:     "/",
		HttpOnly: false,
		Secure:   secureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   cookieMaxAge,
	})

	return record, nil
}

// Logout clears the session + CSRF cookies.
func (s *Store) Logout(w http.ResponseWriter) {
	for _, name := range []string{s.CookieName, s.CSRFCookieName} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: name == s.CookieName,
			Secure:   secureCookies(),
			SameSite: http.SameSiteLaxMode,
			MaxAge:   -1,
		})
	}
}

// Resolve returns the authenticated record for the current request, or nil
// if there is no valid session cookie.
func (s *Store) Resolve(r *http.Request) *core.Record {
	cookie, err := r.Cookie(s.CookieName)
	if err != nil || cookie.Value == "" {
		return nil
	}

	record, err := s.App.FindAuthRecordByToken(cookie.Value, core.TokenTypeAuth)
	if err != nil {
		return nil
	}

	return record
}

// LoadAuth is a router middleware that resolves the session cookie into
// e.Auth so that apis.RequireAuth(s.CollectionName) works downstream.
func (s *Store) LoadAuth() func(e *core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if record := s.Resolve(e.Request); record != nil {
			e.Auth = record
		}
		return e.Next()
	}
}

// ValidateCSRF implements the double-submit cookie pattern. The page layout
// sets `hx-headers='{"X-CSRF-Token": "..."}'` once on <body> for logged-in
// pages, which htmx attaches to every request triggered from a descendant
// element — including rows/forms inserted later via SSE OOB swaps, since
// hx-headers is resolved by walking up the DOM at request time. This avoids
// having to bake a viewer-specific CSRF value into every broadcast partial.
// A plain form-field fallback is kept for non-htmx POSTs.
func (s *Store) ValidateCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(s.CSRFCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}

	posted := r.Header.Get("X-CSRF-Token")
	if posted == "" {
		posted = r.FormValue("csrf_token")
	}
	if posted == "" {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(posted)) == 1
}

// CSRFToken returns the current CSRF cookie value, for embedding in forms.
func (s *Store) CSRFToken(r *http.Request) string {
	cookie, err := r.Cookie(s.CSRFCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}
