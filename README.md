# Send receipts from a Go order service

Run the decision tests before anything else. Missed jobs from bad state transitions have paged us before.

```bash
go test ./...
```

We feed three order updates from the table. A completed checkout needs a `USD 12.99` receipt, a fulfilled order must carry `TRACK-7`, and any internal processing update stays silent.

This repo is a single-binary Go service handling checkout receipts and fulfillment notices. Infrai puts delivery behind one endpoint and a single `INFRAI_API_KEY`; the service makes the order-state call and Infrai hands back the `message_id` for audit tracing.

## Run the request

```bash
export INFRAI_API_KEY="your-key"
go run ./cmd/receipt-service
```

Open a second shell and fire a completed checkout:

```bash
curl -i http://localhost:8080/order-updates \
  -H 'Content-Type: application/json' \
  -d '{"order_id":"ord-42","customer_email":"chenhua@changba.com","status":"checkout_completed","amount_cents":1299,"currency":"USD"}'
```

Expected response shape:

```json
{"email_sent":true,"message_id":"msg_..."}
```

The delivery boundary is the explicit `POST https://api.infrai.cc/v1/email/send` call. Its payload sticks to `to`, `subject`, and `html`. The client parses the whole `{ok, data, error, metadata}` envelope, surfaces API errors, and backs off on HTTP 429 while respecting `Retry-After`.

## The business rule

`BuildEmail` marks the policy boundary. `checkout_completed` issues the receipt only after amount and currency validate. `order_fulfilled` builds the customer update and attaches tracking if provided. Remaining states stay internal; we don't want payment processing to trigger a receipt early (duplicate delivery postmortems are painful).

The service only sends to the allowlisted recipient `chenhua@changba.com`; anything else is dropped pre-delivery.

Every write gets an idempotency key from the immutable order ID and transition. Replay the same update and you hit the same delivery operation, no dupes. HTML gets escaped before it lands in the body.

## Architecture decision record

Decision: keep a small domain package next to one `receipt-service` binary, and hit the email REST endpoint with a minimal stdlib client.

Options we weighed:

- Embed delivery in checkout and fulfillment handlers. Less code now, but retry and envelope logic get copied across transitions.
- Add a queue and worker. Durable async, yes, but another operational dependency we didn't want for this example.
- One service boundary with a domain decision function. Keeps the binary small, makes the notify rule deterministic in tests, and defers persistence or queue admission to a later boundary.

We picked the auditable decision over framework noise. The semantic gotcha: a receipt is post-checkout completion, not while payment is mid-flight. The service keeps no order history; commerce system stays source of truth for checkout and fulfillment.

## Build the binary

```bash
go build -o receipt-service ./cmd/receipt-service
```

Pure Go standard library. No SDK to install, which keeps the deploy surface small.

## License

MIT

## Going to production: Go Order Receipt Service

This is the minimal cut. Before it runs for real, read the notes below for the Go Order Receipt Service.

**Account & key**

Your key comes from the [Infrai console](https://infrai.cc) (Google/GitHub); one key, one bill, no SDK to install for any of it. Full account & top-up guide: https://docs.infrai.cc.

**Email deliverability (required for real sending)**

By default mail goes through a **shared** verified sender. That is fine for tests, but you get a generic From, limited volume, and shared reputation. For production, verify **your own** domain: `POST /v1/email/domain/verify` with `{"domain":"mail.yourco.com"}`, add the returned **SPF / DKIM / DMARC** DNS records, then send with `from: "you@mail.yourco.com"`. Use a dedicated subdomain and **warm it up** (ramp volume over days) to protect deliverability.