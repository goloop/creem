[![deps.dev](https://img.shields.io/badge/deps.dev-insights-4c8dbc)](https://deps.dev/go/github.com%2Fgoloop%2Fcreem) [![Go Reference](https://img.shields.io/badge/godoc-reference-blue)](https://pkg.go.dev/github.com/goloop/creem) [![License](https://img.shields.io/badge/license-MIT-brightgreen)](https://github.com/goloop/creem/blob/v1/LICENSE) [![Stay with Ukraine](https://img.shields.io/static/v1?label=Stay%20with&message=Ukraine%20♥&color=ffD700&labelColor=0057B8&style=flat)](https://u24.gov.ua/)

# creem

`creem` is a Go client for the Creem API and a verifier for its webhooks:
the server side of selling through Creem as merchant of record - one-time
products, custom amounts, subscriptions, seats, upgrades, refunds and signed
events. Standard library only.

Ukrainian version: **[README.UK.md](README.UK.md)**.

## The problem it solves

Creem hosts the checkout, charges and handles tax; your server creates
sessions and acts on events. The traps are the same as with any provider,
plus two of Creem's own:

- **The amount must come from the server.** Checkouts are created here, and
  a custom price for a credit pack is validated before it is sent.
- **The webhook signature has no timestamp.** A captured delivery would
  verify forever. The package checks the event's `created_at` - which is in
  the signed body - and refuses an event older than two days, and the event
  id must still be stored and checked.
- **Shapes vary.** Timestamps arrive as ISO strings on some entities and as
  milliseconds on others; references arrive as ids or as expanded objects.
  `Time` and `Expandable` read both, so a handler does not break on the day
  a field changes form.
- **A failed POST may have succeeded.** A refund or cancellation is not
  retried after a 5xx; only what cannot have run is.
- **A lost webhook is lost money.** Creem stops retrying after a day;
  `ListTransactions` lets a periodic job find what never arrived.

## Features

- Checkout sessions with `request_id`, metadata, a prefilled customer,
  discount codes and custom prices for one-time products.
- Subscriptions: get, cancel (at once or at the period end), pause, resume,
  upgrade to another product, change the number of seats.
- Full refunds; customers by id or email; billing portal links;
  transactions, one by one or paged for reconciliation.
- Webhooks: HMAC-SHA256 signature check with secret rotation and an age
  check; typed checkouts, subscriptions, refunds and disputes.
- Typed `*APIError`; the host follows the key (test or live); no redirects
  followed, ids checked against an allowlist, keys never printed.
- Zero dependencies.

## Installation

```bash
go get github.com/goloop/creem
```

## Quick start

```go
c := creem.New(os.Getenv("CREEM_API_KEY")) // a test key talks to the test host

co, err := c.CreateCheckout(ctx, &creem.CheckoutRequest{
	ProductID:   "prod_credits",
	RequestID:   purchaseID,
	CustomPrice: 1000, // cents, one-time products only
	Customer:    &creem.CheckoutCustomer{Email: email},
	SuccessURL:  "https://app.example.com/billing?paid=1",
})
// redirect to co.CheckoutURL
```

## Selling models

| Model | Example | How |
|---|---|---|
| **One-time, fixed price** | a licence, a fixed pack | a one-time product |
| **One-time, amount from the server** | credit packs, custom orders | `CustomPrice` on one one-time product |
| **Subscription** | $15 a month, $60 a year | a subscription product per term (monthly, 3 or 6 months, yearly) |
| **Seats** | $8 per member | `Units` at checkout, `UpdateSubscriptionItems` later |
| **Plan changes** | Personal to Team, monthly to yearly | `UpgradeSubscription` with a proration behaviour |
| **Cancel, pause, change of mind** | cancel at the period end | `CancelSubscription`, `ResumeSubscription`, `PauseSubscription` |
| **Discounts** | a launch code | `DiscountCode` at checkout |
| **Refunds and chargebacks** | 14-day withdrawal | `RefundTransaction` (full only), `refund.created`, `dispute.created` |

Not available through Creem: a recurring amount decided on the server
(`CustomPrice` is for one-time products only), partial refunds, invoices
with payment terms, and terms longer than a year among the standard billing
periods. Credits and allowances live in
[goloop/ledger](https://github.com/goloop/ledger). The reference shows every
model in code: [Selling models](DOC.md#selling-models).

## A real-world example

```go
wh := creem.NewWebhook(os.Getenv("CREEM_WEBHOOK_SECRET"))

http.HandleFunc("POST /webhooks/creem", func(w http.ResponseWriter, r *http.Request) {
	ev, err := wh.ParseRequest(r) // also refuses an event older than DefaultMaxAge
	if err != nil {
		http.Error(w, "bad signature", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	// // The event table is a shortcut, not the guard: apply is idempotent on
	// its own (keyed writes), and an event is marked done only after it was
	// applied, so a failure here is retried by Creem and never lost.
	done, err := events.Done(ctx, ev.ID) // your table
	if err != nil {
		http.Error(w, "retry", http.StatusInternalServerError)
		return
	}
	if done {
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := apply(ctx, ev); err != nil {
		http.Error(w, "retry", http.StatusInternalServerError) // Creem sends it again
		return
	}
	if err := events.MarkDone(ctx, ev.ID); err != nil {
		http.Error(w, "retry", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
})

func apply(ctx context.Context, ev *creem.Event) error {
	switch ev.Type {
	case creem.EventCheckoutCompleted:
		co, err := ev.Checkout()
		if err != nil {
			return err
		}
		if co.Order == nil {
			return fmt.Errorf("checkout %s completed without an order", co.ID)
		}
		// // Compare the amount and currency with the purchase, then credit, keyed by the transaction id.
		return purchases.Paid(ctx, co.RequestID, co.Order.Transaction, co.Order.AmountPaid, co.Order.Currency)

	case creem.EventSubscriptionPaid, creem.EventSubscriptionCanceled, creem.EventSubscriptionExpired:
		sub, err := ev.Subscription()
		if err != nil {
			return err
		}
		return entitlements.Sync(ctx, sub, ev.CreatedAt.Time) // ignores an event older than the state

	case creem.EventRefundCreated:
		ref, err := ev.Refund()
		if err != nil {
			return err
		}
		return purchases.Refunded(ctx, ref.Transaction.ID, ref.ID, ref.RefundAmount) // // may be partial (refunded from the dashboard): take back in proportion, keyed by ref.ID

	case creem.EventDisputeCreated:
		d, err := ev.Dispute()
		if err != nil {
			return err
		}
		return purchases.Refunded(ctx, d.Transaction.ID, d.ID, d.Amount)
	}
	return nil
}
```

## The API at a glance

| Call | Endpoint |
|---|---|
| `CreateCheckout`, `GetCheckout` | `/v1/checkouts` |
| `GetSubscription`, `CancelSubscription`, `PauseSubscription`, `ResumeSubscription` | `/v1/subscriptions[/{id}/cancel,/pause,/resume]` |
| `UpgradeSubscription`, `UpdateSubscriptionItems` | `/v1/subscriptions/{id}[/upgrade]` |
| `RefundTransaction` | `/v1/refunds` |
| `GetCustomer`, `CustomerByEmail`, `CustomerPortalLink` | `/v1/customers[/billing]` |
| `GetTransaction`, `ListTransactions` | `/v1/transactions[/search]` |
| `NewWebhook`, `Webhook.ParseRequest`, `Sign` | webhook events |

## Documentation

- Full reference and selling models: [DOC.md](DOC.md) · [DOC.UK.md](DOC.UK.md)
- Package API: [pkg.go.dev/github.com/goloop/creem](https://pkg.go.dev/github.com/goloop/creem)
- Changes between versions: [CHANGELOG.md](CHANGELOG.md)
- Credit wallets on top: [goloop/ledger](https://github.com/goloop/ledger)

## Contributing

Contributions are welcome. Please run `go test -race ./...`, `go vet ./...`
and `gofmt -l .` before submitting a pull request.

## License

`creem` is released under the MIT License. See [LICENSE](LICENSE).

## Release v1.0.0

Publish the existing payment and webhook API as version 1.0.0. The v1 release keeps the existing import path and public API. Future incompatible changes require a new major version.
