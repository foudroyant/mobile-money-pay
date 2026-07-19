# AGENTS.md — MomoPay CG

Reference for anyone (human or AI agent) working on this codebase. Keep it in sync with the code — when a route, collection, or convention changes, update the relevant section here in the same change.

## What this is

A Mobile Money payment gateway for Congo Brazzaville, modeled on Stripe: third-party applications integrate via a REST API + secret key, create a transaction, redirect their customer to a hosted payment page, and get a signed webhook when the payment is confirmed.

There is **no live API integration with mobile money operators**. Confirmation is manual:

1. A transaction is created (via the API, or from the `/admin` or `/app` dashboards) → a public payment link `/pay/{token}` is generated, status `pending`.
2. The customer opens the link, sees Airtel Money payment instructions, pays externally via USSD, and submits the phone number + the operator transaction reference on the page → status `submitted`.
3. A platform admin checks the operator's own dashboard/SMS and clicks **Valider** or **Rejeter** in `/admin` → status `validated` or `rejected`. A rejected transaction is not terminal: the payer can correct and resubmit. Alternatively, the admin can paste confirmed operator IDs into the **reconciliation tool** (`/admin/reconcile`) and skip finding/clicking each transaction by hand — see the dedicated section below.
4. On `validated`/`rejected`, a signed webhook fires to the merchant's `webhook_url` (if set), and the open payment page updates live and redirects to `success_url`.

**Only Airtel Money is wired up today.** MTN Mobile Money is a planned addition — the `operator` field and API already accept it as a concept, but transaction creation forces `airtel` and the API rejects `operator: "mtn"` with an explicit "coming soon" error. When adding MTN support, search the codebase for `OperatorAirtel`/`"airtel"` — those are the places that currently hardcode the single-operator assumption (payment page instructions, `txn.Create`, the API validation in `internal/handlers/api.go`).

## Roles

- **Admin** (`admins` collection): platform staff. Validate/reject transactions, create payment links on behalf of any merchant, manage merchant accounts. Log in at `/admin/login`. Deliberately a separate collection from PocketBase's own `_superusers` — admins never get access to the PocketBase dashboard (`/_/`) or raw schema; `_superusers` is reserved for developers running migrations.
- **Merchant** (`merchants` collection): the integrating account, Stripe-style. Authenticates two ways: a Bearer API key (`sk_live_.../sk_test_...`) for the `/v1` REST API, or email+password at `/app/login` for the small self-service dashboard (create links manually, view own transactions, configure webhook). Both paths are first-class — a merchant doesn't need the API to use the platform.
- **Payer**: anonymous. Access to `/pay/{token}` is gated purely by knowing the unguessable token — same trust model as a Stripe Checkout link. No account.

## Data model

Collections are defined in Go (`migrations/0001_collections.go`), not through the PocketBase dashboard. All three collections keep PocketBase's default API rules (nil = superuser-only): every access goes through our own routes in `internal/handlers`, never through PocketBase's built-in REST API.

**Important**: unlike older PocketBase versions, `created`/`updated` are *not* added to a collection automatically — every collection here adds them explicitly via `timestampFields()` in the migration. If you add a new collection, don't forget this or sorting/filtering by `-created` will fail at runtime with `invalid sort field "created"`.

- `admins` — `name`, plus built-in auth fields (email/password).
- `merchants` — `name`, `api_key_hash` (sha256, unique index), `api_key_prefix` (display only, e.g. `sk_live_ab12`), `webhook_url`, `webhook_secret`, `status` (`active`/`disabled`), plus built-in auth fields.
- `transactions` — `merchant` (relation), `token` (unique public slug), `amount`, `currency` (always `"XAF"`), `description`, `metadata` (JSON passthrough), `operator` (`airtel`/`mtn`), `mode` (`live`/`test`, derived from which API key prefix created it — dashboard-created links are always `live`), `created_by` (`api`/`merchant`/`admin`), `status` (`pending`→`submitted`→`validated`|`rejected`), `payer_msisdn`, `payer_operator_ref`, `submitted_at`, `resolved_by_email` (denormalized — deliberately not a relation to `admins`, so a transaction's audit trail doesn't depend on the admin account still existing), `resolved_at`, `resolution_note`, `success_url`, `cancel_url`, `webhook_delivered`, `webhook_attempts`.
- `confirmed_refs` (`migrations/0002_confirmed_refs.go`) — backs the reconciliation tool: `operator_ref`, `status` (`pending`/`matched`), `matched_transaction` (relation, set once matched), `added_by_email`. See the dedicated section below.

The transaction state machine itself lives in `internal/txn` (`Create`, `Submit`, `Resolve`) — every handler that creates or mutates a transaction goes through those functions rather than touching records directly, so the API, the admin dashboard and the merchant dashboard can't drift out of sync.

Airtel Money's receiving number is **global config via `AIRTEL_PAY_NUMBER`** (env var), not per-merchant — the platform aggregates funds and settles to merchants out of band. This is a deliberate v1 simplification, not an oversight.

## Directory layout

```
main.go                       bootstrap, migration runner, first-admin seeding
migrations/                   Go-defined PocketBase collections
internal/
  txn/                        transaction state machine (Create/Submit/Resolve)
  payid/                      API key + payment token + webhook secret generation
  session/                    cookie login + CSRF for /admin and /app
  realtime/                   in-process SSE pub/sub hub
  webhook/                    signed webhook dispatch + SSRF guard
  view/                       html/template loading (embeds templates/)
  handlers/                   all HTTP routes, grouped by area:
    api.go                    /v1/*        merchant REST API
    pay.go                    /pay/*       public hosted payment page
    admin.go                  /admin/*     staff back office (transactions)
    merchants.go              /admin/merchants/*  staff merchant management
    app.go                    /app/*       merchant self-service dashboard
    reconcile.go              /admin/reconcile/*  reconciliation tool + periodic sweep
    hooks.go                  wires transaction/confirmed_refs changes → SSE broadcast
    sse.go                    shared SSE streaming helper
    respond.go                shared page/dialog rendering helpers
    viewdata.go                template view-model builders
    authmw.go                 shared auth-or-redirect middleware
templates/                    html/template pages + htmx partials (embedded)
static/                       vendored htmx + htmx-ext-sse + app.css (embedded)
```

## Running it

```
copy .env.example .env   # then edit the values
go run . serve --http=127.0.0.1:8090
```

`.env` is loaded automatically on startup (`internal/envfile`, no external dependency) and is gitignored — real env vars set in the shell/deploy platform always take precedence over it. `.env.example` documents every key and is the one that's committed.

Required/useful env vars:

- `ADMIN_EMAIL` / `ADMIN_PASSWORD` — if set and the `admins` collection is empty, an admin account is seeded on boot (see `seedFirstAdmin` in `main.go`). There's no PocketBase-CLI equivalent of `superuser upsert` for a custom collection, hence this.
- `AIRTEL_PAY_NUMBER` — the Airtel Money number shown on the payment page. Falls back to a visible placeholder if unset.
- `APP_BASE_URL` — public origin used to build `payment_url` in API responses; without it, it's derived from the request's `Host` header, which is wrong behind most reverse proxies.
- `COOKIE_SECURE` — set to `true` in production (HTTPS). Defaults to `false` so local HTTP dev works; session/CSRF cookies won't be sent by the browser over plain HTTP if this is `true`.
- `RECONCILE_INTERVAL_SECONDS` — how often the reconciliation background sweep runs (see below). Defaults to 120.

PocketBase's own superuser (`_superusers`, for `/_/` dashboard access to inspect the schema/DB directly) is separate: `go run . superuser upsert you@example.com yourpassword`.

## Real-time (SSE)

`internal/realtime.Hub` is an in-process pub/sub — **single instance only, no horizontal scaling** without adding a shared broker. Topics: `admin` (every admin dashboard), `merchant:{merchantID}` (that merchant's own dashboard), `txn:{token}` (one payer's payment page).

`internal/handlers/hooks.go` binds `OnRecordAfterCreateSuccess`/`OnRecordAfterUpdateSuccess` on `transactions` and renders + broadcasts on every persist. Two htmx SSE conventions to know before touching this:

- **New rows** use event `txn-new` and a plain `<tr>` (`txn_row_new` partial), swapped in with `hx-swap="afterbegin"`.
- **Row updates** use event `txn-update` and `<tr hx-swap-oob="true">` (`txn_row_update` partial) — an OOB fragment can only patch an element that already exists in the viewer's DOM, so it can never be used to insert a brand-new row.
- Both share the same `<td>` markup via the `txn_row_cells` sub-template (`templates/partials/txn_row.html`) — edit that one, not the two wrappers, when changing a column.
- The action buttons (Valider/Rejeter/create-link forms) intentionally return an **empty response** (`hx-swap="none"`) and rely on the SSE broadcast — including back to the actor's own open tab — to update the UI. Returning the row directly from the POST handler *and* broadcasting it would double-insert new rows for the person who just created one.

The reconciliation pending list (`templates/pages/admin_reconcile.html`) follows the same new/update convention but adds a third case — **removal**: event `ref-removed` sends `<tr hx-swap-oob="delete">` (`reconcile_row_removed` partial), which uses htmx's `delete` OOB swap style to remove a row matched by another admin, or by the background sweep, without a page reload.

CSRF on these authenticated pages is delivered once via `hx-headers` on `<body>` (see `templates/layout.html` and `session.ValidateCSRF`) so that htmx requests triggered from SSE-inserted elements — which never went through a page load and can't carry a form-embedded token — still send a valid `X-CSRF-Token` automatically (htmx resolves `hx-headers` from the nearest ancestor at request time).

### Row detail popup

Clicking **Détails** on any row (`hx-get="/admin|app/transactions/{id}"`, target `#dialog`) swaps the `txn_detail` partial into the `<div id="dialog"></div>` placeholder declared once in `layout.html` — that div is the only "modal" mechanism in the app, pure CSS (`.dialog-backdrop`/`.dialog`) plus an htmx swap, no JS. A **Fermer** button (and, inside the popup, the Valider/Rejeter buttons on success) points `hx-get="/dialog/close"` at the same target, which returns an empty body and so clears the popup. `/dialog/close` is registered globally in `register.go`, not under `/admin` or `/app`, since it never reveals anything.

### Copy-to-clipboard

The one place this app uses real JavaScript: `static/app.js` is a ~15-line vanilla script (event delegation on `document`, `navigator.clipboard.writeText`) that powers every `<button data-copy="...">` — there is no CSS/HTML-only way to write to the clipboard. Don't add a JS framework on top of this; if you need another one-off browser API, extend `app.js` the same way rather than inlining `<script>` blocks in templates.

## Webhooks

`internal/webhook.Dispatch` is fire-and-forget (goroutine, 3 attempts with backoff, **no durable queue** — a dropped delivery after 3 tries is lost, not retried later). Called explicitly from `internal/handlers/admin.go`'s validate/reject handler — deliberately *not* from the generic SSE broadcast hook, because that hook also fires on the follow-up save that records delivery status, which would otherwise re-trigger a dispatch in a loop.

Signature header: `X-Momopay-Signature: t=<unix_ts>,v1=<hex hmac_sha256("{ts}.{body}", webhook_secret)>` (Stripe-style). Tell merchants to reject anything where `|now - t|` is too large (a few minutes) to guard against replay.

Before every dispatch attempt, `checkPublicURL` resolves the target host and refuses loopback/private/link-local addresses — a merchant-supplied `webhook_url` is exactly the kind of input SSRF guards exist for. This will reject `http://127.0.0.1:...` and similar, by design; that's what "local webhook testing didn't fire" almost always means.

## Reconciliation tool

`/admin/reconcile` lets an admin paste operator (Airtel Money) transaction IDs they've confirmed on their own operator dashboard/SMS — one per line — instead of hunting down and clicking each matching row by hand. Each pasted ID becomes a `confirmed_refs` record and is matched two ways, both going through the same `tryMatch` function in `internal/handlers/reconcile.go`:

1. **Immediately on add** — `adminAddConfirmedRefs` looks for a `submitted` transaction with that `payer_operator_ref` right away. If the payer already submitted their reference on the payment page, this validates it on the spot (and dispatches the webhook, same as a manual Valider click).
2. **Periodic sweep** — `StartReconcileLoop` (started from `main.go` after migrations run) re-checks every still-`pending` `confirmed_refs` record on a ticker (`RECONCILE_INTERVAL_SECONDS`, default 120s). This exists because the operator confirmation can arrive *before* the payer has actually filled in the payment page — the immediate check would miss it, the sweep catches it once they do.

A matched reference is validated with `resolution_note = "Rapprochement automatique (ID confirmé)"` and `resolved_by_email` set to whoever added the reference (carried in the `confirmed_refs` record itself, since the sweep runs in a background goroutine with no admin session to read from). Deleting a pending reference (a paste mistake) is a hard delete — there is no soft-delete/audit trail for those, only for matched ones (which stay in `confirmed_refs` with `status = "matched"`).

## Security notes

- API keys: `crypto/rand`, 240 bits, format `sk_live_.../sk_test_...`, only a SHA-256 hash is stored (`internal/payid`). Never log a raw key or a payment token.
- Admin/merchant sessions: PocketBase's own auth token (`record.NewAuthToken()`), stored in an `HttpOnly` cookie. No parallel signing scheme.
- CSRF: double-submit cookie, checked on every non-GET request under `/admin/*` and `/app/*` (`csrfMiddleware` in `admin.go`, reused by `app.go`). See the SSE section above for how the token reaches htmx-triggered requests without a per-request form field.
- Unauthenticated visits to `/admin/*` or `/app/*` redirect to the matching login page (`requireAuthOrRedirect` in `authmw.go`) rather than returning a bare JSON 401 — these are HTML back offices, not API endpoints.

## Deliberately deferred (v1)

Not built, but the data model doesn't need rework to add them later: real MTN/Airtel API integration, merchant self-signup (accounts are created by an admin), rate limiting, payment link expiry/TTL, a durable webhook retry queue, automated tests.

## Conventions when extending

- New collection → Go migration in `migrations/`, remember `timestampFields()`, leave API rules unset (superuser-only) unless you specifically want PocketBase's built-in REST API to expose it.
- New transaction-mutating action → add it to `internal/txn`, not inline in a handler.
- New authenticated HTML route → register under the existing `/admin` or `/app` protected group so it inherits `requireAuthOrRedirect` + `csrfMiddleware` for free; don't build a third auth scheme.
- New page → a `templates/pages/*.html` file with `{{define "content"}}`, rendered via `view.Page(w, "filename-without-extension", data)`; the data struct should embed `handlers.Layout` so `.Title`/`.CSRFToken` resolve in `layout.html`.
- New fragment reused by both a full page and an SSE broadcast → a `templates/partials/*.html` file, rendered via `view.Partial(name, data)`.
