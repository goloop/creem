# creem - reference

The full reference for the `creem` package: the client, checkouts,
subscriptions, refunds, customers, transactions, webhooks, the shapes Creem
sends, retries and errors.

Ukrainian version: **[DOC.UK.md](DOC.UK.md)**.

## Contents

- [Mental model](#mental-model)
- [Creating a client](#creating-a-client)
- [Checkouts](#checkouts)
- [Subscriptions](#subscriptions)
- [Refunds](#refunds)
- [Customers, portal, transactions](#customers-portal-transactions)
- [Selling models](#selling-models)
- [Webhooks](#webhooks)
- [Shapes: Time and Expandable](#shapes-time-and-expandable)
- [Retries](#retries)
- [Errors](#errors)
- [Scope](#scope)

## Mental model

Creem is a merchant of record: it sells its own catalog products, charges,
handles tax, and reports back through signed webhooks. The server creates a
checkout session, sends the customer to it, and acts on the events.

The provider is the source of truth for money; your database is the source
of truth for what the customer may use. Webhooks are the road between them,
verified first and deduplicated by event id.

## Creating a client

```go
c := creem.New(os.Getenv("CREEM_API_KEY"))
```

The host follows the key: a test key (`creem_test_...`) talks to
`https://test-api.creem.io`, any other to `https://api.creem.io`.
`WithTest(bool)` and `WithBaseURL(url)` override it; `Test()` reports where
the client talks. The key is sent as `x-api-key`.

| Option | Default |
|---|---|
| `WithTimeout` | 30s (default HTTP client only) |
| `WithHTTPClient` | a new `http.Client` |
| `WithMaxRetries` | 2 |
| `WithHeader` | none |

## Checkouts

```go
co, err := c.CreateCheckout(ctx, &creem.CheckoutRequest{
	ProductID:  "prod_pro_yearly",
	RequestID:  purchaseID,
	Customer:   &creem.CheckoutCustomer{Email: "ann@example.com"},
	SuccessURL: "https://app.example.com/billing?paid=1",
	Metadata:   map[string]any{"purchase": purchaseID},
})
// redirect to co.CheckoutURL
```

- `ProductID` is required: Creem sells catalog products only.
- `CustomPrice` (cents per unit, 100 to 99 999 999) overrides the product's
  price for one-time products. It is how an amount decided on the server - a
  credit pack - is charged under one product.
- `Customer` is an existing customer `ID` or an `Email`, not both.
- `RequestID` and `Metadata` come back on the checkout and its events: put
  your purchase id there.

`GetCheckout(ctx, id)` reads a session back.

## Subscriptions

```go
sub, err := c.GetSubscription(ctx, "sub_...")

sub, err = c.CancelSubscription(ctx, id, &creem.CancelRequest{Mode: creem.CancelScheduled})
sub, err = c.CancelSubscription(ctx, id, &creem.CancelRequest{Mode: creem.CancelImmediate})
sub, err = c.ResumeSubscription(ctx, id) // also takes back a scheduled cancellation

sub, err = c.PauseSubscription(ctx, id)
sub, err = c.UpgradeSubscription(ctx, id, "prod_team", creem.ProrationChargeImmediately)
```

A scheduled cancellation leaves the subscription in
`SubscriptionScheduledCancel` until the period ends. An empty `Mode` uses the
store's setting. `CurrentPeriodEndDate` is how long the customer has paid
for.

## Refunds

```go
status, err := c.RefundTransaction(ctx, "tran_...")
// creem.RefundPending, RefundSucceeded, RefundRequiresAction, ...
```

The API refunds what is left of a transaction, in full; there is no partial
refund. The `refund.created` webhook follows. A chargeback arrives as
`dispute.created`; treat it like a refund.

## Customers, portal, transactions

```go
cu, err := c.GetCustomer(ctx, "cust_...")
cu, err = c.CustomerByEmail(ctx, "ann@example.com")

link, err := c.CustomerPortalLink(ctx, cu.ID) // where the customer manages billing

tr, err := c.GetTransaction(ctx, "tran_...")
```

## Selling models

Creem sells **products** from its catalog. A product is one-time or a
subscription with a billing period, and a checkout sells one product in some
number of units. Every model below is a choice of product and checkout
fields.

### One-time purchase at a fixed price

A licence, a fixed pack: a one-time product at its catalog price.

```go
co, err := c.CreateCheckout(ctx, &creem.CheckoutRequest{
	ProductID: "prod_maker_licence",
	RequestID: purchaseID,
	Customer:  &creem.CheckoutCustomer{Email: email},
})
```

On `checkout.completed`, grant the right in your entitlements table, keyed by
`co.RequestID`.

### One-time purchase, amount from the server

Credit packs, an order priced by a formula: one one-time product, and the
amount in `CustomPrice` (cents per unit, 100 to 99 999 999).

```go
co, err := c.CreateCheckout(ctx, &creem.CheckoutRequest{
	ProductID:   "prod_credits",
	RequestID:   purchaseID,
	CustomPrice: pack.Cents,
})
```

When the webhook arrives, compare `co.Order.Amount` and `co.Order.Currency`
with your purchase row before crediting.

### Subscriptions and terms

The billing period belongs to the product: create one subscription product
per term (monthly, every three or six months, yearly) and sell the one the
customer picks. `CustomPrice` does not apply to subscriptions, so each term
has its catalog price.

```go
co, err := c.CreateCheckout(ctx, &creem.CheckoutRequest{
	ProductID: "prod_pro_yearly",
	RequestID: "sub:" + accountID,
	Customer:  &creem.CheckoutCustomer{Email: email},
})
```

Grant the plan's rights until `CurrentPeriodEndDate` and move that date on
every `subscription.paid`. A term longer than a year is not among Creem's
standard billing periods; confirm with Creem before you offer one.

Trials are configured on the product; the subscription then starts as
`SubscriptionTrialing`.

### Seats

A per-seat subscription sold with `Units`, changed later:

```go
co, err := c.CreateCheckout(ctx, &creem.CheckoutRequest{ProductID: "prod_team_seat", Units: 10})

sub, err := c.UpdateSubscriptionItems(ctx, sub.ID,
	[]creem.SubscriptionItemUpdate{{ID: sub.Items[0].ID, Units: 12}},
	creem.ProrationChargeImmediately)
```

### Plan changes

Move a subscription to another product - Personal to Team, monthly to
yearly:

```go
sub, err := c.UpgradeSubscription(ctx, sub.ID, "prod_team_yearly", creem.ProrationChargeImmediately)
```

| Behaviour | Effect |
|---|---|
| `ProrationChargeImmediately` | charge the prorated difference now (the default) |
| `ProrationCharge` | put the prorated difference on the next invoice |
| `ProrationNone` | switch without proration |

### Cancelling, pausing, changing one's mind

```go
sub, err := c.CancelSubscription(ctx, id, &creem.CancelRequest{Mode: creem.CancelScheduled})
sub, err = c.ResumeSubscription(ctx, id) // takes the scheduled cancellation back
sub, err = c.PauseSubscription(ctx, id)
```

`CustomerPortalLink` gives Creem's own page where the customer manages the
subscription and the card.

### Discounts

Pass a code created in the Creem dashboard: `DiscountCode: "LAUNCH30"`.

### Refunds, withdrawals and chargebacks

```go
status, err := c.RefundTransaction(ctx, transactionID) // full refund only
```

`refund.created` follows; take back what the money bought, keyed by the
refund id. A chargeback arrives as `dispute.created`. A partial refund is
not available through the API.

### What Creem does not do

- a recurring amount decided on the server (a negotiated plan);
- partial refunds;
- invoices with payment terms for companies;
- a balance of credits or metered usage - sell packs as one-time purchases
  and keep the balance in [goloop/ledger](https://github.com/goloop/ledger).

For the first three, Paddle is the fallback; `goloop/paddle` covers them.

## Webhooks

```go
wh := creem.NewWebhook(secret)
ev, err := wh.ParseRequest(r)
```

Verification:

- The `creem-signature` header is the hex HMAC-SHA256 of the raw body under
  the webhook secret. The body must be the bytes as received.
- `WithPreviousSecret` accepts an older secret during a rotation.
- An empty secret verifies nothing, so a misconfigured service rejects every
  delivery instead of accepting forgeries.
- **The signature carries no timestamp.** A captured delivery verifies
  forever, so deduplicating on `ev.ID` is not only how a repeated delivery is
  ignored - it is the only defence against a replayed one. Store every event
  id you processed.

Handling:

- Answer 200 once the event is stored; Creem retries a failed delivery a few
  times over the following hours.
- `ev.CreatedAt` is when the event was made; use it, or the entity's
  `UpdatedAt`, to ignore an event older than the state already applied.

Decoding: `ev.Checkout()` for `checkout.completed`, `ev.Subscription()` for
`subscription.*`, `ev.Refund()`, `ev.Dispute()`, or `ev.Decode(&v)`.

For a completed checkout, `co.Order` holds what was charged (`Amount`,
`Currency`, `Type` "onetime" or "recurring"), `co.RequestID` your reference,
and `co.Subscription` the subscription it started, if any.

`Sign(secret, body)` builds a valid signature, for tests of your handler.

## Shapes: Time and Expandable

Creem sends timestamps as ISO strings on most entities and as milliseconds
since the epoch on transactions and webhook envelopes. `Time` embeds
`time.Time` and reads both; `null` and absent are the zero time.

A reference to another entity is sometimes an id and sometimes the expanded
entity. `Expandable[T]` reads both:

```go
co.Customer.ID     // always set
co.Customer.Object // *Customer when it was expanded, nil otherwise
```

## Retries

| Failure | GET | POST |
|---|---|---|
| 429 | retried, after `Retry-After` | retried, after `Retry-After` |
| 5xx | retried with backoff | returned |
| network error | retried with backoff | returned |
| other 4xx | returned | returned |

A POST that failed with a 5xx may have been applied - a refund, a
cancellation - so repeating it is left to the caller, who can read the state
back first.

## Errors

```go
var ae *creem.APIError
if errors.As(err, &ae) {
	log.Printf("%d %s: %v (trace %s)", ae.Status, ae.Reason, ae.Messages, ae.TraceID)
}
if errors.Is(err, creem.ErrNotFound) { ... } // any 404
```

`Messages` is a list; Creem sometimes sends a single string, which becomes a
list of one. `ErrInvalid` reports a request refused before it was sent.

## Scope

Server-side calls a product needs: no product management, licenses, discount
codes or search pagination. The shared `do` path takes any endpoint.

The package depends only on the standard library.
