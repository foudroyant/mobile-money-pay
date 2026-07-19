// Package webhook signs and delivers transaction status notifications to
// merchant-provided callback URLs, Stripe-style.
package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// Payload is the JSON body POSTed to the merchant's webhook_url.
type Payload struct {
	ID             string          `json:"id"`
	Status         string          `json:"status"`
	Amount         float64         `json:"amount"`
	Currency       string          `json:"currency"`
	Description    string          `json:"description,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	ResolutionNote string          `json:"resolution_note,omitempty"`
	OccurredAt     string          `json:"occurred_at"`
}

var retryDelays = []time.Duration{0, 5 * time.Second, 30 * time.Second}

// Dispatch signs and POSTs payload to targetURL, retrying a few times on
// failure. It performs its own I/O and sleeps between retries, so callers
// should run it in a goroutine (best-effort, no durable queue).
func Dispatch(app core.App, transactionID, targetURL, secret string, payload Payload) {
	if targetURL == "" {
		return
	}

	if err := checkPublicURL(targetURL); err != nil {
		log.Printf("webhook: refusing to dispatch for transaction %s: %v", transactionID, err)
		return
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("webhook: marshal failed for transaction %s: %v", transactionID, err)
		return
	}

	var lastErr error
	for attempt, delay := range retryDelays {
		if delay > 0 {
			time.Sleep(delay)
		}

		lastErr = send(targetURL, secret, body)
		recordAttempt(app, transactionID, attempt+1, lastErr == nil)

		if lastErr == nil {
			return
		}
	}

	log.Printf("webhook: giving up on transaction %s after %d attempts: %v", transactionID, len(retryDelays), lastErr)
}

func send(targetURL, secret string, body []byte) error {
	ts := time.Now().Unix()
	signed := fmt.Sprintf("%d.%s", ts, body)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signed))
	sig := hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Momopay-Signature", fmt.Sprintf("t=%d,v1=%s", ts, sig))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook endpoint returned status %d", resp.StatusCode)
	}

	return nil
}

func recordAttempt(app core.App, transactionID string, attempts int, delivered bool) {
	record, err := app.FindRecordById("transactions", transactionID)
	if err != nil {
		return
	}
	record.Set("webhook_attempts", attempts)
	if delivered {
		record.Set("webhook_delivered", true)
	}
	if err := app.Save(record); err != nil {
		log.Printf("webhook: failed to record delivery state for transaction %s: %v", transactionID, err)
	}
}

// checkPublicURL is a best-effort SSRF guard: it rejects webhook URLs that
// resolve to loopback, private, link-local or otherwise non-public
// addresses so a merchant can't point us at our own internal network.
func checkPublicURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("missing host")
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("dns lookup failed: %w", err)
	}

	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("host %s resolves to a non-public address (%s)", host, ip)
		}
	}

	return nil
}
