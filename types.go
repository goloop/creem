package creem

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Time is a timestamp as Creem sends it: an ISO 8601 string on most
// entities, milliseconds since the epoch on transactions and webhook
// envelopes. Both decode into the embedded time.Time; null and absent are
// the zero time.
type Time struct {
	time.Time
}

// UnmarshalJSON implements json.Unmarshaler.
func (t *Time) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case len(b) == 0 || string(b) == "null":
		t.Time = time.Time{}
		return nil
	case b[0] == '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		if s == "" {
			t.Time = time.Time{}
			return nil
		}
		v, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return fmt.Errorf("creem: time %q: %w", s, err)
		}
		t.Time = v
		return nil
	default:
		ms, err := strconv.ParseInt(string(b), 10, 64)
		if err != nil {
			f, ferr := strconv.ParseFloat(string(b), 64)
			if ferr != nil {
				return fmt.Errorf("creem: time %s: %w", b, err)
			}
			ms = int64(f)
		}
		t.Time = time.UnixMilli(ms).UTC()
		return nil
	}
}

// MarshalJSON implements json.Marshaler, as RFC 3339.
func (t Time) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(t.Time.UTC().Format(time.RFC3339Nano))
}

// Expandable is a reference to another entity: an id, or the entity itself
// when the API expanded it. ID is set either way; Object only when it was
// expanded.
type Expandable[T any] struct {
	ID     string
	Object *T
}

// UnmarshalJSON implements json.Unmarshaler.
func (e *Expandable[T]) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	*e = Expandable[T]{}
	switch {
	case len(b) == 0 || string(b) == "null":
		return nil
	case b[0] == '"':
		return json.Unmarshal(b, &e.ID)
	}
	var head struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return err
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	e.ID, e.Object = head.ID, &v
	return nil
}

// MarshalJSON implements json.Marshaler: the object when there is one,
// otherwise the id.
func (e Expandable[T]) MarshalJSON() ([]byte, error) {
	if e.Object != nil {
		return json.Marshal(e.Object)
	}
	if e.ID == "" {
		return []byte("null"), nil
	}
	return json.Marshal(e.ID)
}

// Product is something sold through Creem. Price is in cents.
type Product struct {
	ID                string         `json:"id"`
	Mode              string         `json:"mode"`
	Name              string         `json:"name"`
	Description       string         `json:"description"`
	Price             int64          `json:"price"`
	Currency          string         `json:"currency"`
	BillingType       string         `json:"billing_type"`
	BillingPeriod     string         `json:"billing_period"`
	Status            string         `json:"status"`
	TaxMode           string         `json:"tax_mode"`
	TaxCategory       string         `json:"tax_category"`
	DefaultSuccessURL string         `json:"default_success_url"`
	Metadata          map[string]any `json:"metadata"`
	CreatedAt         Time           `json:"created_at"`
	UpdatedAt         Time           `json:"updated_at"`
}

// Customer is a Creem customer.
type Customer struct {
	ID         string         `json:"id"`
	Mode       string         `json:"mode"`
	Email      string         `json:"email"`
	Name       string         `json:"name"`
	Country    string         `json:"country"`
	ExternalID string         `json:"external_id"`
	Metadata   map[string]any `json:"metadata"`
	CreatedAt  Time           `json:"created_at"`
	UpdatedAt  Time           `json:"updated_at"`
}

// Checkout statuses.
const (
	CheckoutPending    = "pending"
	CheckoutProcessing = "processing"
	CheckoutCompleted  = "completed"
	CheckoutExpired    = "expired"
)

// Checkout is a checkout session.
type Checkout struct {
	ID           string                   `json:"id"`
	Mode         string                   `json:"mode"`
	Status       string                   `json:"status"`
	RequestID    string                   `json:"request_id"`
	Units        int                      `json:"units"`
	CustomPrice  int64                    `json:"custom_price"`
	CheckoutURL  string                   `json:"checkout_url"`
	SuccessURL   string                   `json:"success_url"`
	Metadata     map[string]any           `json:"metadata"`
	Product      Expandable[Product]      `json:"product"`
	Customer     Expandable[Customer]     `json:"customer"`
	Subscription Expandable[Subscription] `json:"subscription"`
	Order        *Order                   `json:"order"`
}

// Order is what a completed checkout charged. Amounts are in cents.
type Order struct {
	ID             string               `json:"id"`
	Mode           string               `json:"mode"`
	Customer       Expandable[Customer] `json:"customer"`
	Product        Expandable[Product]  `json:"product"`
	Transaction    string               `json:"transaction"`
	Amount         int64                `json:"amount"`
	SubTotal       int64                `json:"sub_total"`
	TaxAmount      int64                `json:"tax_amount"`
	DiscountAmount int64                `json:"discount_amount"`
	AmountDue      int64                `json:"amount_due"`
	AmountPaid     int64                `json:"amount_paid"`
	Currency       string               `json:"currency"`
	Status         string               `json:"status"`
	// Type is "onetime" or "recurring".
	Type      string `json:"type"`
	CreatedAt Time   `json:"created_at"`
	UpdatedAt Time   `json:"updated_at"`
}

// Subscription statuses.
const (
	SubscriptionActive          = "active"
	SubscriptionTrialing        = "trialing"
	SubscriptionPastDue         = "past_due"
	SubscriptionUnpaid          = "unpaid"
	SubscriptionPaused          = "paused"
	SubscriptionScheduledCancel = "scheduled_cancel"
	SubscriptionCanceled        = "canceled"
)

// Subscription is a recurring billing agreement.
type Subscription struct {
	ID                     string               `json:"id"`
	Mode                   string               `json:"mode"`
	Status                 string               `json:"status"`
	Product                Expandable[Product]  `json:"product"`
	Customer               Expandable[Customer] `json:"customer"`
	Items                  []SubscriptionItem   `json:"items"`
	CollectionMethod       string               `json:"collection_method"`
	LastTransactionID      string               `json:"last_transaction_id"`
	LastTransactionDate    Time                 `json:"last_transaction_date"`
	NextTransactionDate    Time                 `json:"next_transaction_date"`
	CurrentPeriodStartDate Time                 `json:"current_period_start_date"`
	CurrentPeriodEndDate   Time                 `json:"current_period_end_date"`
	CanceledAt             Time                 `json:"canceled_at"`
	Metadata               map[string]any       `json:"metadata"`
	CreatedAt              Time                 `json:"created_at"`
	UpdatedAt              Time                 `json:"updated_at"`
}

// SubscriptionItem is one product a subscription bills.
type SubscriptionItem struct {
	ID        string `json:"id"`
	ProductID string `json:"product_id"`
	PriceID   string `json:"price_id"`
	Units     int    `json:"units"`
}

// Transaction statuses.
const (
	TransactionPending       = "pending"
	TransactionPaid          = "paid"
	TransactionRefunded      = "refunded"
	TransactionPartialRefund = "partialRefund"
	TransactionChargedBack   = "chargedBack"
	TransactionUncollectible = "uncollectible"
	TransactionDeclined      = "declined"
	TransactionCanceled      = "canceled"
	TransactionVoid          = "void"
)

// Transaction is one payment: a one-time order or a subscription invoice.
// Amounts are in cents.
type Transaction struct {
	ID             string                   `json:"id"`
	Mode           string                   `json:"mode"`
	Amount         int64                    `json:"amount"`
	AmountPaid     int64                    `json:"amount_paid"`
	DiscountAmount int64                    `json:"discount_amount"`
	TaxAmount      int64                    `json:"tax_amount"`
	RefundedAmount int64                    `json:"refunded_amount"`
	Currency       string                   `json:"currency"`
	TaxCountry     string                   `json:"tax_country"`
	Description    string                   `json:"description"`
	Type           string                   `json:"type"`
	Status         string                   `json:"status"`
	Order          Expandable[Order]        `json:"order"`
	Subscription   Expandable[Subscription] `json:"subscription"`
	Customer       Expandable[Customer]     `json:"customer"`
	PeriodStart    Time                     `json:"period_start"`
	PeriodEnd      Time                     `json:"period_end"`
	CreatedAt      Time                     `json:"created_at"`
}

// Refund is the object of a refund.created event. RefundAmount is in cents.
type Refund struct {
	ID             string                   `json:"id"`
	Mode           string                   `json:"mode"`
	Status         string                   `json:"status"`
	RefundAmount   int64                    `json:"refund_amount"`
	RefundCurrency string                   `json:"refund_currency"`
	Reason         string                   `json:"reason"`
	Transaction    Expandable[Transaction]  `json:"transaction"`
	Subscription   Expandable[Subscription] `json:"subscription"`
	Checkout       Expandable[Checkout]     `json:"checkout"`
	Order          Expandable[Order]        `json:"order"`
	Customer       Expandable[Customer]     `json:"customer"`
	CreatedAt      Time                     `json:"created_at"`
}

// Dispute is the object of a dispute.created event: a chargeback. Amount is
// in cents.
type Dispute struct {
	ID           string                   `json:"id"`
	Mode         string                   `json:"mode"`
	Amount       int64                    `json:"amount"`
	Currency     string                   `json:"currency"`
	Transaction  Expandable[Transaction]  `json:"transaction"`
	Subscription Expandable[Subscription] `json:"subscription"`
	Checkout     Expandable[Checkout]     `json:"checkout"`
	Order        Expandable[Order]        `json:"order"`
	Customer     Expandable[Customer]     `json:"customer"`
	CreatedAt    Time                     `json:"created_at"`
}
