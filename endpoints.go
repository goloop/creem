package creem

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// Custom price limits, in cents per unit.
const (
	MinCustomPrice = 100
	MaxCustomPrice = 99_999_999
)

// CheckoutCustomer prefills the customer of a checkout: an existing
// customer by ID, or an Email. Not both.
type CheckoutCustomer struct {
	ID    string `json:"id,omitempty"`
	Email string `json:"email,omitempty"`
	Name  string `json:"name,omitempty"`
}

// CheckoutRequest creates a checkout session.
type CheckoutRequest struct {
	// ProductID is the Creem product sold; it is required. Creem sells its
	// own catalog products only.
	ProductID string `json:"product_id"`

	// RequestID is your reference, returned on the checkout and its events.
	// Put the id of your own purchase row here.
	RequestID string `json:"request_id,omitempty"`

	// Units defaults to 1 on the API side.
	Units int `json:"units,omitempty"`

	// CustomPrice overrides the product's price, in cents per unit, between
	// MinCustomPrice and MaxCustomPrice. It works for one-time products
	// only, and it is how an amount decided on the server is charged under
	// one product.
	CustomPrice int64 `json:"custom_price,omitempty"`

	DiscountCode string            `json:"discount_code,omitempty"`
	Customer     *CheckoutCustomer `json:"customer,omitempty"`
	SuccessURL   string            `json:"success_url,omitempty"`

	// Metadata is returned on the checkout and its events.
	Metadata map[string]any `json:"metadata,omitempty"`
}

// CreateCheckout creates a checkout session. Send the customer to its
// CheckoutURL.
//
// It is not retried on a 5xx: see the package documentation.
func (c *Client) CreateCheckout(ctx context.Context, req *CheckoutRequest) (*Checkout, error) {
	switch {
	case req == nil || req.ProductID == "":
		return nil, fmt.Errorf("%w: a checkout needs a product id", ErrInvalid)
	case req.CustomPrice != 0 && (req.CustomPrice < MinCustomPrice || req.CustomPrice > MaxCustomPrice):
		return nil, fmt.Errorf("%w: custom price %d is outside %d..%d cents", ErrInvalid, req.CustomPrice, MinCustomPrice, MaxCustomPrice)
	case req.Customer != nil && req.Customer.ID != "" && req.Customer.Email != "":
		return nil, fmt.Errorf("%w: a checkout customer is an id or an email, not both", ErrInvalid)
	case req.Units < 0:
		return nil, fmt.Errorf("%w: negative units", ErrInvalid)
	}
	var out Checkout
	if err := c.do(ctx, http.MethodPost, "/v1/checkouts", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetCheckout returns a checkout session.
func (c *Client) GetCheckout(ctx context.Context, id string) (*Checkout, error) {
	if err := checkID("checkout", id); err != nil {
		return nil, err
	}
	var out Checkout
	if err := c.do(ctx, http.MethodGet, "/v1/checkouts", url.Values{"checkout_id": {id}}, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetSubscription returns a subscription.
func (c *Client) GetSubscription(ctx context.Context, id string) (*Subscription, error) {
	if err := checkID("subscription", id); err != nil {
		return nil, err
	}
	var out Subscription
	if err := c.do(ctx, http.MethodGet, "/v1/subscriptions", url.Values{"subscription_id": {id}}, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Cancellation modes.
const (
	CancelImmediate = "immediate"
	CancelScheduled = "scheduled"
)

// CancelRequest cancels a subscription.
type CancelRequest struct {
	// Mode is CancelImmediate or CancelScheduled (at the end of the
	// period); empty uses the store's setting.
	Mode string `json:"mode,omitempty"`

	// OnExecute is "cancel" or "pause", for a scheduled cancellation.
	OnExecute string `json:"onExecute,omitempty"`
}

// CancelSubscription cancels a subscription. req may be nil. A scheduled
// cancellation leaves the subscription in SubscriptionScheduledCancel, and
// ResumeSubscription takes it back.
func (c *Client) CancelSubscription(ctx context.Context, id string, req *CancelRequest) (*Subscription, error) {
	if err := checkID("subscription", id); err != nil {
		return nil, err
	}
	if req == nil {
		req = &CancelRequest{}
	}
	var out Subscription
	if err := c.do(ctx, http.MethodPost, "/v1/subscriptions/"+id+"/cancel", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PauseSubscription pauses a subscription.
func (c *Client) PauseSubscription(ctx context.Context, id string) (*Subscription, error) {
	if err := checkID("subscription", id); err != nil {
		return nil, err
	}
	var out Subscription
	if err := c.do(ctx, http.MethodPost, "/v1/subscriptions/"+id+"/pause", nil, struct{}{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ResumeSubscription resumes a paused subscription or takes back a
// scheduled cancellation.
func (c *Client) ResumeSubscription(ctx context.Context, id string) (*Subscription, error) {
	if err := checkID("subscription", id); err != nil {
		return nil, err
	}
	var out Subscription
	if err := c.do(ctx, http.MethodPost, "/v1/subscriptions/"+id+"/resume", nil, struct{}{}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Upgrade behaviours.
const (
	ProrationChargeImmediately = "proration-charge-immediately"
	ProrationCharge            = "proration-charge"
	ProrationNone              = "proration-none"
)

// UpgradeSubscription moves a subscription to another product.
// updateBehavior is one of the Proration constants; empty means the API's
// default, ProrationChargeImmediately.
func (c *Client) UpgradeSubscription(ctx context.Context, id, productID, updateBehavior string) (*Subscription, error) {
	if err := checkID("subscription", id); err != nil {
		return nil, err
	}
	if productID == "" {
		return nil, fmt.Errorf("%w: empty product id", ErrInvalid)
	}
	body := map[string]string{"product_id": productID}
	if updateBehavior != "" {
		body["update_behavior"] = updateBehavior
	}
	var out Subscription
	if err := c.do(ctx, http.MethodPost, "/v1/subscriptions/"+id+"/upgrade", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SubscriptionItemUpdate changes one item of a subscription, usually its
// number of units (seats). Name the item by ID, or by ProductID or PriceID.
type SubscriptionItemUpdate struct {
	ID        string `json:"id,omitempty"`
	ProductID string `json:"product_id,omitempty"`
	PriceID   string `json:"price_id,omitempty"`
	Units     int    `json:"units"`
}

// UpdateSubscriptionItems changes the items of a subscription - the number of
// seats on a team plan. updateBehavior is one of the Proration constants;
// empty means the API's default, ProrationCharge.
func (c *Client) UpdateSubscriptionItems(ctx context.Context, id string, items []SubscriptionItemUpdate, updateBehavior string) (*Subscription, error) {
	if err := checkID("subscription", id); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("%w: no items to update", ErrInvalid)
	}
	for i, it := range items {
		if it.ID == "" && it.ProductID == "" && it.PriceID == "" {
			return nil, fmt.Errorf("%w: item %d names no item, product or price", ErrInvalid, i)
		}
		if it.Units < 1 {
			return nil, fmt.Errorf("%w: item %d needs at least one unit", ErrInvalid, i)
		}
	}
	body := map[string]any{"items": items}
	if updateBehavior != "" {
		body["update_behavior"] = updateBehavior
	}
	var out Subscription
	if err := c.do(ctx, http.MethodPost, "/v1/subscriptions/"+id, nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetCustomer returns a customer.
func (c *Client) GetCustomer(ctx context.Context, id string) (*Customer, error) {
	if err := checkID("customer", id); err != nil {
		return nil, err
	}
	var out Customer
	if err := c.do(ctx, http.MethodGet, "/v1/customers", url.Values{"customer_id": {id}}, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CustomerByEmail returns the customer with the address.
func (c *Client) CustomerByEmail(ctx context.Context, email string) (*Customer, error) {
	if email == "" {
		return nil, fmt.Errorf("%w: empty email", ErrInvalid)
	}
	var out Customer
	if err := c.do(ctx, http.MethodGet, "/v1/customers", url.Values{"email": {email}}, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CustomerPortalLink returns a link to the customer's billing portal.
func (c *Client) CustomerPortalLink(ctx context.Context, customerID string) (string, error) {
	if err := checkID("customer", customerID); err != nil {
		return "", err
	}
	var out struct {
		Link string `json:"customer_portal_link"`
	}
	body := map[string]string{"customer_id": customerID}
	if err := c.do(ctx, http.MethodPost, "/v1/customers/billing", nil, body, &out); err != nil {
		return "", err
	}
	return out.Link, nil
}

// GetTransaction returns a transaction.
func (c *Client) GetTransaction(ctx context.Context, id string) (*Transaction, error) {
	if err := checkID("transaction", id); err != nil {
		return nil, err
	}
	var out Transaction
	if err := c.do(ctx, http.MethodGet, "/v1/transactions", url.Values{"transaction_id": {id}}, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Refund statuses returned by RefundTransaction.
const (
	RefundPending        = "pending"
	RefundRequiresAction = "requiresAction"
	RefundSucceeded      = "succeeded"
	RefundFailed         = "failed"
	RefundCanceled       = "canceled"
)

// RefundTransaction refunds what is left of a transaction and returns the
// refund's status. The API refunds in full only. The refund.created webhook
// follows.
//
// It is not retried on a 5xx: see the package documentation.
func (c *Client) RefundTransaction(ctx context.Context, transactionID string) (string, error) {
	if err := checkID("transaction", transactionID); err != nil {
		return "", err
	}
	var out struct {
		Status string `json:"status"`
	}
	body := map[string]string{"transaction_id": transactionID}
	if err := c.do(ctx, http.MethodPost, "/v1/refunds", nil, body, &out); err != nil {
		return "", err
	}
	return out.Status, nil
}
