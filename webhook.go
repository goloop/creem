package creem

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// DefaultMaxBody bounds the webhook body ParseRequest reads.
const DefaultMaxBody = 1 << 20

// SignatureHeader is the header that carries the webhook signature.
const SignatureHeader = "creem-signature"

// DefaultMaxAge is how old an event Parse accepts. Creem stops retrying a
// delivery after 24 hours; twice that leaves room for a late retry and a
// drifting clock, and still bounds how long a captured delivery can be
// replayed.
const DefaultMaxAge = 48 * time.Hour

var (
	// ErrSignature reports a webhook whose signature is missing or does not
	// match the body.
	ErrSignature = errors.New("creem: bad webhook signature")

	// ErrEventExpired reports a correctly signed event whose created_at is
	// further from the local clock than the allowed age: a replay of a
	// captured delivery, or a clock that drifted. It matches ErrSignature
	// too, so one check rejects both.
	ErrEventExpired = fmt.Errorf("%w: event outside the allowed age", ErrSignature)
)

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
	// more than once. The signature carries no timestamp, so the id - with
	// the age check on CreatedAt - is also the defence against a replayed
	// delivery.
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
	maxAge  time.Duration
	now     func() time.Time
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

// WithMaxAge sets how far an event's created_at may be from the local clock,
// either way. The default is DefaultMaxAge; zero turns the check off and
// leaves the stored event ids as the only replay guard.
func WithMaxAge(d time.Duration) WebhookOption {
	return func(w *Webhook) { w.maxAge = max(0, d) }
}

// WithWebhookClock replaces time.Now, for tests.
func WithWebhookClock(now func() time.Time) WebhookOption {
	return func(w *Webhook) { w.now = now }
}

// NewWebhook returns a Webhook for the webhook secret. An empty secret
// verifies nothing: every delivery fails with ErrSignature, which is the safe
// way for a misconfigured service to fail.
func NewWebhook(secret string, opts ...WebhookOption) *Webhook {
	w := &Webhook{maxBody: DefaultMaxBody, maxAge: DefaultMaxAge, now: time.Now}
	if secret != "" {
		w.secrets = append(w.secrets, []byte(secret))
	}
	for _, o := range opts {
		o(w)
	}
	return w
}

// String describes the webhook without its secrets.
func (w *Webhook) String() string {
	return fmt.Sprintf("creem.Webhook(%d secrets, max age %s)", len(w.secrets), w.maxAge)
}

// GoString is String, for %#v.
func (w *Webhook) GoString() string { return w.String() }

// LogValue implements slog.LogValuer, without the secrets.
func (w *Webhook) LogValue() slog.Value { return slog.StringValue(w.String()) }

// Verify checks a signature against the raw body: the hex HMAC-SHA256 of the
// body under the secret. The body must be the bytes as received. It checks
// the signature only; Parse also checks the event's age.
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

// Parse verifies a delivery, decodes it and checks its age: created_at is in
// the signed body, so it cannot be forged, and an event older than the
// allowed age fails with ErrEventExpired.
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
	if w.maxAge > 0 {
		if e.CreatedAt.IsZero() {
			return nil, fmt.Errorf("%w: no created_at", ErrEventExpired)
		}
		age := w.now().Sub(e.CreatedAt.Time)
		if age > w.maxAge || age < -w.maxAge {
			return nil, ErrEventExpired
		}
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
