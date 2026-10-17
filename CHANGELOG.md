# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.0] - 2026-10-18

Publish the existing payment and webhook API as version 1.0.0. The v1 release keeps the existing import path and public API. Future incompatible changes require a new major version.

## [0.3.0] - 2026-10-01

### Fixed
- A successful response with an empty body, an entity without an id, or
  another entity than the one asked for is `ErrResponse` instead of an empty
  result. So is a checkout without a checkout URL, a portal answer without a
  link, and a refund status the package does not know.
- `CustomerByEmail` compares the address in the answer and never returns a
  customer with another one.
- A list page whose next page does not move forward, or with an item
  without an id, is `ErrResponse`.
- `Retry-After` is capped before it is converted, so a huge value no longer
  overflows into no wait; an HTTP date is read too.
- `Time` refuses a fraction or an out-of-range float instead of truncating
  it.

### Added
- `ErrResponse`.
- Notes on pending refunds, checking `Mode`, and walking pages that shift.

## [0.2.0] - 2026-10-01

### Fixed
- The client no longer follows redirects: `x-api-key` is not a header
  `net/http` drops on the way to another host, so a redirect handed the key
  over. A 3xx is an `*APIError`.
- Ids put into a request path are checked against an allowlist (letters,
  digits, `_`, `-`, at most 128 bytes) instead of a denylist that let `..`
  and escaped slashes through.
- The backoff no longer panics on a high attempt count, and
  `WithMaxRetries` is capped at `MaxRetries` (10).
- A request that cannot be built is returned at once instead of retried; a
  response beyond the size limit is an error instead of a truncated body.
- `WithHTTPClient(nil)` keeps the default client instead of panicking. An
  empty key and plain HTTP to a remote host are refused with `ErrInvalid`.
- `Time` reads a number below 10^11 as seconds rather than as milliseconds
  in 1973.
- The README example no longer panics on a checkout without an order and
  handles decoding errors.

### Added
- An age check on webhook events: `Parse` refuses an event whose signed
  `created_at` is further than `DefaultMaxAge` (48 hours) from the clock,
  with `ErrEventExpired`, which matches `ErrSignature`. `WithMaxAge` and
  `WithWebhookClock` configure it.
- `ListTransactions`, paged, for a reconciliation job that finds what a lost
  webhook never delivered.
- `String`, `GoString` and `LogValue` on `Client` and `Webhook`, which never
  show the key or the secrets.

## [0.1.0] - 2026-09-30

First release.

### Added
- First version: a Creem client for checkouts (with custom prices),
  subscriptions (cancel, pause, resume, upgrade), full refunds, customers,
  the billing portal and transactions.
- Webhook verification with secret rotation, and typed decoding of
  checkouts, subscriptions, refunds and disputes.
- `Time` for ISO and millisecond timestamps, `Expandable` for ids that may
  arrive expanded; retries on 429 for every method and on 5xx for GET only.
- `UpdateSubscriptionItems` for seat changes.
- README and reference in English and Ukrainian: the problem the package
  solves, the pricing and selling models it supports, a real-world example.
