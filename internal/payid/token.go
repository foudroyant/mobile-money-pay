// Package payid generates the random identifiers used throughout the
// platform: merchant API keys and public payment link tokens.
package payid

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// randomBase62 returns a cryptographically random base62 string of length n.
func randomBase62(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, n)
	for i, v := range b {
		out[i] = base62Alphabet[int(v)%len(base62Alphabet)]
	}
	return string(out), nil
}

// APIKey is a freshly generated merchant secret key. Secret is shown to the
// caller exactly once; only Hash is meant to be persisted.
type APIKey struct {
	Secret string
	Prefix string
	Hash   string
}

// GenerateAPIKey creates a new merchant secret key, formatted like
// "sk_live_<40 random chars>" or "sk_test_<40 random chars>".
func GenerateAPIKey(live bool) (*APIKey, error) {
	body, err := randomBase62(40)
	if err != nil {
		return nil, err
	}

	env := "test"
	if live {
		env = "live"
	}
	secret := "sk_" + env + "_" + body

	return &APIKey{
		Secret: secret,
		Prefix: secret[:12],
		Hash:   HashAPIKey(secret),
	}, nil
}

// HashAPIKey returns the stored/lookup form of a secret key. The key has
// high entropy (240 bits of randomness) so a fast unsalted hash used purely
// as an indexed lookup value is sufficient here; this is not a password.
func HashAPIKey(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// IsLiveKey reports whether a secret key belongs to the live (vs test) mode
// based on its "sk_live_"/"sk_test_" prefix.
func IsLiveKey(secret string) bool {
	return len(secret) >= 8 && secret[3:7] == "live"
}

// GeneratePaymentToken creates the public, unguessable slug used in the
// hosted payment page URL (/pay/{token}).
func GeneratePaymentToken() (string, error) {
	return randomBase62(28)
}

// GenerateCSRFToken creates a random value for the double-submit CSRF cookie.
func GenerateCSRFToken() (string, error) {
	return randomBase62(32)
}

// GenerateWebhookSecret creates the per-merchant HMAC key used to sign
// webhook deliveries (see internal/webhook).
func GenerateWebhookSecret() (string, error) {
	return randomBase62(32)
}
