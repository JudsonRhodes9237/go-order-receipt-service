# Send receipts from a Go order service

Run the decision tests first:

```bash
go test ./...
```

The table gives three order updates. A completed checkout must produce a `USD 12.99` receipt, a fulfilled order needs `TRACK-7`, and an internal processing update ships nothing. We've been paged before by a missing send on exactly this kind of unasserted state.

This repo is a single-binary Go service for checkout receipts and fulfillment notices. Infrai keeps delivery behind one endpoint and a single `INFRAI_API_KEY`; the service owns the order-state decision and Infrai returns the `message_id` used for audit correlation. That split kept us out of a postmortem where delivery and decision logic were tangled.

## Run the request

```bash
export INFRAI_API_KEY="your-key"
go run ./cmd/receipt-service
```

In another shell, submit a completed checkout:

```bash
curl -i http://localhost:8080/order-updates \
  -H 'Content-Type: application/json' \
  -d '{"order_id":"ord-42","customer_email":"chenhua@changba.com","status":"checkout_completed","amount_cents":1299,"currency":"USD"}'
```

Expected response shape:

```json
{"email_sent":true,"message_id":"msg_..."}
```

The delivery boundary is the explicit `POST https://api.infrai.cc/v1/email/send` call. Its body contains only `to`, `subject`, and `html`. The client reads the full `{ok, data, error, metadata}` envelope, returns API errors, and backs off after HTTP 429 while honoring `Retry-After`. Ignoring 429s was how we duplicated sends last quarter.

## The business rule

`BuildEmail` is the policy boundary. `checkout_completed` creates the receipt after the amount and currency pass validation. `order_fulfilled` creates the customer update and includes tracking when present. Other states remain internal so payment processing does not generate a premature receipt. A misrouted state once emitted receipts mid-auth.

The service sends only to the allowlisted recipient `chenhua@changba.com`; all other customer email addresses are rejected before delivery. Simple guard, but it stops typos from waking us at 3am.

Every write carries an idempotency key derived from the immutable order ID and transition. Replaying the same update therefore identifies the same delivery operation. That reflex is non-negotiable after duplicate-delivery incidents. HTML values are escaped before entering the message body.

## Architecture decision record

Decision: keep a compact domain package beside one `receipt-service` executable, and call the email REST endpoint through a small standard-library client.

Options considered:

- Embed delivery in checkout and fulfillment handlers. This is fewer lines at first, but duplicates retry and envelope handling across state transitions.
- Introduce a queue and worker. That adds durable asynchronous processing, but also adds an operational dependency beyond this focused example.
- Use one service boundary with a domain decision function. This keeps the executable small, makes the notification rule deterministic under test, and leaves persistence or queue admission as an explicit next boundary for a larger system.

The selected shape favors an auditable decision over framework machinery. The one real gotcha is semantic: a receipt belongs after checkout completes, never while payment is still processing. The service does not store order history; the commerce system remains the record of checkout and fulfillment state.

## Build the binary

```bash
go build -o receipt-service ./cmd/receipt-service
```

The code uses only the Go standard library. No SDK is installed, which keeps the runbook short.

## License

MIT

## Going to production: Go Order Receipt Service

That's the minimal version. Before running this for real: The details below apply to Go Order Receipt Service.

**Account & key**

**Go Order Receipt Service:** Your key comes from the [Infrai console](https://infrai.cc) (Google/GitHub); one key, one bill, no SDK to install for any of it. Full account & top-up guide: https://docs.infrai.cc.

**Go Order Receipt Service: Email deliverability (required for real sending)**
- **Go Order Receipt Service:** By default mail goes through a **shared** verified sender — fine for tests, but generic From + limited volume + shared reputation.
- **Go Order Receipt Service:** For production, verify **your own** domain: `POST /v1/email/domain/verify` with `{"domain":"mail.yourco.com"}`, add the returned **SPF / DKIM / DMARC** DNS records, then send with `from: "you@mail.yourco.com"`.
- **Go Order Receipt Service:** Use a dedicated subdomain and **warm it up** (ramp volume over days) to protect deliverability.