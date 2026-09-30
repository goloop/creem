# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
