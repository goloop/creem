package creem

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// DefaultMaxBody bounds the webhook body ParseRequest reads.
const DefaultMaxBody = 1 << 20

// SignatureHeader is the header that carries the webhook signature.
const SignatureHeader = "creem-signature"

// ErrSignature reports a webhook whose signature is missing or does not
// match the body.
var ErrSignature = errors.New("creem: bad webhook signature")

// Event types this package names. Any other type still parses; compare
// Event.Type with the string.
const (
	EventCheckoutCompleted = "checkout.completed"

	EventSubscriptionActive          = "subscription.active"
	EventSubscriptionPaid            = "subscription.paid"
	EventSubscriptionCanceled        = "subscription.canceled"
	EventSubscriptionScheduledCancel = "subscription.scheduled_cancel"
	EventSubscriptionPastDue         = "subscription.past_due"
	EventSubscriptionUnpaid          = "subscription.unpaid"
	EventSubscriptionExpired         = "subscription.expired"
	EventSubscriptionUpdate          = "subscription.update"
	EventSubscriptionTrialing        = "subscription.trialing"
	EventSubscriptionPaused          = "subscription.paused"

	EventRefundCreated  = "refund.created"
	EventDisputeCreated = "dispute.created"
)

// Event is one verified webhook notification.
type Event struct {
	// ID identifies the event. Deduplicate on it: a delivery can arrive
	// more than once, and the signature carries no timestamp, so the id is
	// also the only defence against a replayed delivery.
	ID        string `json:"id"`
	Type      string `json:"eventType"`
	CreatedAt Time   `json:"created_at"`

	// Object is the entity the event is about, as sent.
	Object json.RawMessage `json:"object"`
}

// Decode unmarshals the event's object into v.
func (e *Event) Decode(v any) error {
	if err := json.Unmarshal(e.Object, v); err != nil {
		return fmt.Errorf("creem: decode %s: %w", e.Type, err)
	}
	return nil
}

// Checkout decodes the object of a checkout.completed event.
func (e *Event) Checkout() (*Checkout, error) {
	var v Checkout
	return &v, e.Decode(&v)
}

// Subscription decodes the object of a subscription.* event.
func (e *Event) Subscription() (*Subscription, error) {
	var v Subscription
	return &v, e.Decode(&v)
}

// Refund decodes the object of a refund.created event.
func (e *Event) Refund() (*Refund, error) {
	var v Refund
	return &v, e.Decode(&v)
}

// Dispute decodes the object of a dispute.created event.
func (e *Event) Dispute() (*Dispute, error) {
	var v Dispute
	return &v, e.Decode(&v)
}

// Webhook verifies and parses notifications.
//
// A Webhook is safe for concurrent use.
type Webhook struct {
	secrets [][]byte
	maxBody int64
}

// WebhookOption configures a Webhook in NewWebhook.
type WebhookOption func(*Webhook)

// WithPreviousSecret also accepts signatures made with an older secret, for
// the time a rotated secret is still in use somewhere.
func WithPreviousSecret(secret string) WebhookOption {
	return func(w *Webhook) {
		if secret != "" {
			w.secrets = append(w.secrets, []byte(secret))
		}
	}
}

// WithMaxBody sets how many bytes ParseRequest reads. The default is
// DefaultMaxBody.
func WithMaxBody(n int64) WebhookOption {
	return func(w *Webhook) { w.maxBody = n }
}

// NewWebhook returns a Webhook for the webhook secret. An empty secret
// verifies nothing: every delivery fails with ErrSignature, which is the safe
// way for a misconfigured service to fail.
func NewWebhook(secret string, opts ...WebhookOption) *Webhook {
	w := &Webhook{maxBody: DefaultMaxBody}
	if secret != "" {
		w.secrets = append(w.secrets, []byte(secret))
	}
	for _, o := range opts {
		o(w)
	}
	return w
}

// Verify checks a signature against the raw body: the hex HMAC-SHA256 of the
// body under the secret. The body must be the bytes as received.
func (w *Webhook) Verify(signature string, body []byte) error {
	got, err := hex.DecodeString(strings.TrimSpace(signature))
	if err != nil || len(got) == 0 || len(w.secrets) == 0 {
		return ErrSignature
	}
	for _, secret := range w.secrets {
		mac := hmac.New(sha256.New, secret)
		mac.Write(body)
		if hmac.Equal(got, mac.Sum(nil)) {
			return nil
		}
	}
	return ErrSignature
}

// Parse verifies a delivery and decodes it.
func (w *Webhook) Parse(signature string, body []byte) (*Event, error) {
	if err := w.Verify(signature, body); err != nil {
		return nil, err
	}
	var e Event
	if err := json.Unmarshal(body, &e); err != nil {
		return nil, fmt.Errorf("creem: decode event: %w", err)
	}
	if e.ID == "" || e.Type == "" {
		return nil, errors.New("creem: decode event: no id or eventType")
	}
	return &e, nil
}

// ParseRequest reads, verifies and decodes a delivery. Answer 200 once the
// event is stored; anything else makes Creem send it again.
func (w *Webhook) ParseRequest(r *http.Request) (*Event, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, w.maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("creem: read webhook: %w", err)
	}
	if int64(len(body)) > w.maxBody {
		return nil, fmt.Errorf("creem: webhook body larger than %d bytes", w.maxBody)
	}
	return w.Parse(r.Header.Get(SignatureHeader), body)
}

// Sign returns the signature of the body. It exists for tests of the code
// that receives webhooks.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
