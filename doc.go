// Package creem is a client for the Creem API and a verifier for its
// webhooks.
//
// It covers what a product that sells through Creem needs from the server
// side: checkout sessions (with a custom price for one-time products),
// subscriptions (cancel, pause, resume, upgrade), refunds, customers and
// their billing portal, transactions, and signed webhook events.
//
//	c := creem.New(os.Getenv("CREEM_API_KEY")) // a test key talks to the test host
//	co, err := c.CreateCheckout(ctx, &creem.CheckoutRequest{
//	    ProductID:   "prod_...",
//	    RequestID:   purchaseID,
//	    CustomPrice: 1000, // cents
//	    Customer:    &creem.CheckoutCustomer{Email: email},
//	})
//	// redirect to co.CheckoutURL
//
// # Selling models
//
// One-time products at their catalog price or at a custom price decided on
// the server; subscription products per term (monthly, quarterly, half-year,
// yearly), trials and seats (UpdateSubscriptionItems); moving to another
// product (UpgradeSubscription); scheduled cancellation and taking it back;
// discount codes; full refunds. Not available through the Creem API: a
// recurring amount decided on the server, partial refunds and invoices with
// payment terms. Credit balances belong in goloop/ledger.
//
// # Webhooks
//
// A Webhook verifies the creem-signature header, the HMAC-SHA256 of the raw
// body, and decodes the event. The signature carries no timestamp, but the
// event's created_at is in the signed body: Parse refuses an event older
// than DefaultMaxAge with ErrEventExpired, and within that window
// deduplicating on Event.ID is what stops a replayed delivery.
//
//	wh := creem.NewWebhook(os.Getenv("CREEM_WEBHOOK_SECRET"))
//	ev, err := wh.ParseRequest(r)
//	if ev.Type == creem.EventCheckoutCompleted {
//	    co, err := ev.Checkout()
//	    ...
//	}
//
// # Shapes
//
// Creem sends timestamps as ISO strings on most entities and as
// milliseconds on others; both decode into Time. A reference to another
// entity is an id or the expanded entity; both decode into Expandable, whose
// ID is set either way.
//
// # Reconciliation
//
// Creem stops retrying a delivery within a day. ListTransactions lets a
// periodic job find payments whose webhook never arrived and apply them the
// way the webhook would.
//
// # Safety
//
// The client never follows a redirect (x-api-key would follow it to another
// host), refuses plain HTTP except to a loopback host, admits only letters,
// digits, "_" and "-" in an id, and never prints its key.
//
// # Retries
//
// A request refused with 429 is retried on any method; a network error or a
// 5xx is retried on GET only, because a POST that failed may have been
// applied.
//
// The package depends only on the standard library.
package creem
