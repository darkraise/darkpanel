package send

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"
)

// SignatureHeader carries the HMAC-SHA256 of a webhook body.
const SignatureHeader = "X-Darkpanel-Signature"

type webhookBody struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Severity  string `json:"severity"`
	HostID    string `json:"hostId"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
}

// webhook posts ev as JSON. Delivery is at least once, so the body carries
// the event id; with a key, the body's HMAC-SHA256 is sent as
// X-Darkpanel-Signature: sha256=<hex>.
func (s *sender) webhook(ctx context.Context, ch Channel, ev Event) (string, error) {
	body, err := json.Marshal(webhookBody{ID: ev.ID, Kind: ev.Kind, Severity: ev.Severity, HostID: ev.HostID,
		Title: ev.Title, Body: ev.Body, CreatedAt: ev.CreatedAt.UTC().Format(time.RFC3339)})
	if err != nil {
		return CodeInvalid, err
	}
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	if ch.Secret != "" {
		mac := hmac.New(sha256.New, []byte(ch.Secret))
		mac.Write(body)
		h.Set(SignatureHeader, "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	return s.post(ctx, "the webhook", ch.URL, h, body)
}
