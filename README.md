# MyScoutee payment simulator

Lightweight Stripe and Barion-compatible test server for the MyScoutee manual
QA and deployment-emulation stack. It is not a payment provider and never
collects card data.

## Supported contract

- Stripe Checkout creation requires a manual-capture PaymentIntent. It exposes
  Checkout and PaymentIntent retrieval, authorization, capture and release.
- `POST /v2/Payment/Start`, v4 PaymentState, Capture and
  CancelAuthorization implement the Barion `DelayedCapture` subset used by
  MyScoutee.
- When 3DS is enabled, the member receives a read-only waiting surface while
  the separate Admin 3DS simulator surface exposes approve and decline actions.
- Stripe emits signed event payloads. Barion emits the intentionally smaller
  `PaymentId` callback notification so the application must read PaymentState.
- Stripe gateway outcomes emit Stripe-shaped events with a
  `Stripe-Signature` HMAC over the exact raw request body.
- `POST /test/events/{id}/replay` repeats the same event for idempotency and
  recovery tests.
- `GET /test/audit` returns sanitized session/event/delivery evidence.
- SQLite persists Stripe sessions/intents/events, Barion payments and both
  callback delivery audits across container restarts.
- The MyScoutee card-registration flow creates a time-limited provider session,
  accepts only the documented simulator card profiles, returns a provider token
  and never returns or persists a PAN/CVC in MyScoutee.

The create endpoint deliberately requires `Idempotency-Key` by default. This
is stricter than Stripe's optional API field and acts as a QA assertion for the
MyScoutee integration.

## Security boundary

- Only `sk_test_` API keys are accepted; `sk_live_` is always rejected.
- The listener is loopback-only unless
  `PAYMENT_SIMULATOR_ALLOW_NON_LOOPBACK=true` is explicit (Compose needs this
  inside its private bridge network).
- Outcome mutations require an unguessable per-session capability embedded in
  the generated checkout URL.
- Audit and webhook replay require `X-Test-Audit-Token`; the endpoint is
  disabled when no audit token is configured.
- Card-registration and member waiting documents are capability URLs tied to
  one provider session. The actionable 3DS surface is available only through a
  protected, one-use admin access ticket.
- The configuration page is not a public simulator URL. In the development
  stack an authenticated MyScoutee administrator opens **Payment simulator**
  from the Admin side menu. MyScoutee issues a one-use, ten-minute access ticket
  and embeds the configuration page in the common popup component. Direct access
  to `/`, `/simulator-ui/config.html` or `/configuration-session` is rejected.
- The configuration page can select Stripe, Barion, or **Cash only**. Cash only
  keeps the deployment usable without a card provider.
- API keys, webhook secrets, audit tokens and outcome capabilities are never
  returned by the audit API or logged.
- This service must not be present in a production Compose topology. The
  MyScoutee backend separately rejects its custom Stripe base URL outside the
  development profile.

## Configuration

| Environment variable | Default |
| --- | --- |
| `PAYMENT_SIMULATOR_LISTEN_ADDR` | `127.0.0.1:18082` |
| `PAYMENT_SIMULATOR_PUBLIC_BASE_URL` | URL derived from the listen address |
| `PAYMENT_SIMULATOR_DATABASE_PATH` | `.state/payment-simulator.db` |
| `PAYMENT_SIMULATOR_API_KEY` | `sk_test_myscoutee` |
| `PAYMENT_SIMULATOR_WEBHOOK_URL` | disabled |
| `PAYMENT_SIMULATOR_WEBHOOK_SECRET` | `whsec_myscoutee_test` |
| `PAYMENT_SIMULATOR_BARION_POS_KEY` | fixed test GUID |
| `PAYMENT_SIMULATOR_BARION_CALLBACK_URL` | use request CallbackUrl |
| `PAYMENT_SIMULATOR_AUDIT_TOKEN` | audit/replay disabled |
| `PAYMENT_SIMULATOR_REQUIRE_IDEMPOTENCY` | `true` |

The repository Dockerfile uses Go 1.25, matching `myscoutee-registry`. The
normal QA route is the parent MyScoutee development Compose stack; do not start
a second backend/frontend build to exercise it.

The GitHub/frontend-local build deliberately has no simulator URL. Its Admin
menu keeps both simulator entries visible but disabled, while the seeded
payment-card expiry job remains available in the normal Jobs screen. Production
and E2E frontend environments neither expose nor embed the simulator config
surface.

## Provider references

The emulated boundary follows Stripe's official documentation for
[Checkout Session creation](https://docs.stripe.com/api/checkout/sessions/create),
[idempotent requests](https://docs.stripe.com/api/idempotent_requests),
[Checkout fulfillment](https://docs.stripe.com/checkout/fulfillment), and
[webhook signature verification](https://docs.stripe.com/webhooks/signature).
The authorization lifecycle follows Stripe's
[PaymentIntent states](https://docs.stripe.com/payments/paymentintents/lifecycle),
[manual capture](https://docs.stripe.com/payments/place-a-hold-on-a-payment-method),
and [3DS authentication](https://docs.stripe.com/payments/3d-secure/authentication-flow).

The Barion subset follows official documentation for
[Delayed Capture](https://docs.barion.com/Delayed_Capture),
[Payment Start](https://docs.barion.com/Payment-Start-v2),
[PaymentState v4](https://docs.barion.com/Payment-PaymentState-v4),
[Capture](https://docs.barion.com/Payment-Capture-v2),
[CancelAuthorization](https://docs.barion.com/Payment-CancelAuthorization-v2),
and the [callback mechanism](https://docs.barion.com/Callback_mechanism).

The simulator intentionally supports only card-capable delayed capture.
Open-banking transfers do not advertise a reversible authorization-hold
capability and therefore cannot participate in the MyScoutee seat-replacement
lifecycle.
