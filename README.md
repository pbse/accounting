# Close preparation

**Documentation:** start with the [repository index](INDEX.md) for the governing build plan, B01–B12 pointers, feature contracts and validation evidence.

**Client attachment requirement:** configure [Cloudflare WAF upload scanning](docs/cloudflare-upload-scanning.md) before releasing client PDFs. PDFs need Cloudflare; local TXT/CSV use the separate scanner service.

A foundation for an agent-prepared, firm-approved accounting preparation workspace. React presents the decisions; Go owns capabilities and accounting; Cloudflare supplies ingress and runtime adapters. The connected ledger remains the ledger of record.

**Implemented:** multi-firm/client access; evidence-to-preparation jobs with optional OpenAI interpretation; TXT/CSV/PDF and OCR; live Work and review queues; reviewed prepaid, accrual, loan, depreciation, owner, reconciliation and settlement methods; recurring closes and dependency re-review; QuickBooks/Xero reads, ledger certification, change hints and controlled supported writes (including Xero bill line recodes); client requests, scanned attachments, approved email/replies with a firm reminder rule; MCP access with agent attribution; procedure learning; Daily Books, aging, year-end handoff, binders and operational controls. Supported scope and activation differ by feature: [current coverage and limits](docs/implementation-backlog.md).

**Start with [onboarding and the acceptance matrix](docs/onboarding.md), then [staging/production setup and costs](docs/deployment.md).** WorkOS organizations represent accounting firms; clients and monthly closes live within them. Provider accounts, hosted resources and accountant acceptance are not provisioned by this repository. A merge to `main` that changes more than documentation migrates and deploys production through **Deploy production**, once the merged pull request's CI has passed; the edge forwards `/mcp`, and hosted MCP still needs its WorkOS Connect setup ([MCP](docs/mcp.md)). Hosted `DATABASE_URL` must be a direct endpoint, not a `-pooler` one. Production posting kinds are an operator setting (`PRODUCTION_POSTING_KINDS`, prepaid when unset), not a check the app enforces: enable a kind only after its sandbox check passes ([posting activation](docs/production-posting.md)).

## Start locally

Install Docker with Compose v2.22+ and Node.js 24 (`nvm use` if installed), then:

```sh
cp .env.example .env
# Set WORKSPACE_TOKEN and a separate UPLOAD_SCAN_KEY in .env
# Generate each using: openssl rand -hex 32
npm run dev
```

Open http://localhost:5173. Compose installs locked dependencies, starts the API on localhost:8080, the worker on localhost:8081, and Postgres on localhost:5432. React hot-reloads; Go edits rebuild through Compose Watch. Stop with Ctrl-C; `docker compose down` removes containers while preserving the database volume.

No Cloudflare account, model key, live ledger, or Go installation is needed for this path. Saved closes connect to Postgres. Fresh Compose volumes receive all numbered migrations; existing volumes need the reviewed migration procedure linked from the onboarding guide. Its local-only credentials are in `compose.yaml`; never reuse them in cloud environments.

For faster native iteration (Node 24 and Go 1.27 installed):

```sh
npm ci
# Terminal 1
# Export DATABASE_URL and WORKSPACE_TOKEN from your private .env first.
cd services/backend && go run ./cmd/api
# Terminal 2, from repo root
npm run dev:frontend
# Terminal 3, required for intake, PDF/OCR and preparation, from services/backend
HTTP_ADDR=127.0.0.1:8081 go run ./cmd/worker
```

Restart Go after changes in native mode. Start only Postgres when needed: `docker compose up -d postgres`. No database reset or production credentials are part of startup.

## Layout

```text
apps/frontend/               React + Vite; Work, Client close, Review
services/backend/            One Go module and portable container image
  cmd/api/                   Public capability API process
  cmd/worker/                Background delivery process; no public route
  internal/transport/        HTTP lifecycle and route adapters
  internal/reference/        Labeled synthetic PRD workpaper
services/scanner/            Isolated text-scanner image; Go entrypoint cmd/scanner
workers/scanner/             On-demand scanner container, no cron or database
workers/edge/                Cloudflare assets + Go API container routing
workers/queue/               Go worker container, woken by a one-minute cron (no Cloudflare Queue is deployed)
workers/workflows/           Retired boundary; durable work runs in the Go workflow engine
packages/contracts/          Shared reference shape and future API/MCP contract rules
infra/postgres/migrations/   Ordered SQL migrations for saved closes/workpapers
docs/                        PRD, full review, architecture and delivery decisions
.github/workflows/           CI, manual staging deployment, production deployment on merge
```

Go business modules belong in `services/backend/internal/<domain>` as real behavior arrives. API and worker processes call those modules directly. Do not duplicate the domain in TypeScript or split Go into microservices per feature. Separate scaling roles does not require separate business implementations.

## Checks

```sh
npm ci
npm run check
npm test
npm run build
npm run check:go    # Go 1.27 required
```

Checks include exact calculations, durable workflow transitions, review freshness and organization isolation with synthetic providers/data. They do not certify live provider behavior or enterprise readiness. CI also builds the Go container and validates Compose.

## Read next

- [PRD review and scoped implementation plan](docs/prd-review.md)
- [Architecture and agent execution contract](docs/architecture.md)
- [Cloudflare and GitHub deployment](docs/deployment.md)
- [Original supplied PRD](docs/product-requirements.md)
- [Validation record](docs/validation.md)
- [Downloadable close review package](docs/close-review-package.md)
- [Evidence uploads and durable preparation jobs](docs/evidence-and-preparation.md)
- [Workpaper review and evidence changes](docs/workpaper-review.md)

## Public website and product routes

The frontend is one SPA with separate public and product features:

- `/`: marketing homepage (`src/features/marketing`).
- `/contact`: reach-out page; contact details intentionally unset.
- `/login`: sign-in landing page (`src/features/auth`); AuthKit sign-in when configured; credentials are handled by WorkOS.
- `/dashboard`: Work — decisions, a cross-client review queue, re-review and the agent command (`src/features/closes/Work.tsx`).
- `/dashboard/closes`, `/dashboard/closes/:id?tab=…`: client closes and one close split into Overview, Workpapers, Evidence, Requests, Close & sign-off and Settings.
- `/dashboard/closes/:id/workpapers/:paperId`: review canvas — linked evidence with cited lines beside the calculation and the decision.
- `/dashboard/firm`: firm membership, client access, email sender and operations.
- `/dashboard/close`, `/dashboard/review`: labeled synthetic examples.

React Router handles links and browser history; Cloudflare's existing SPA fallback supports direct URL loads. The dashboard is loaded separately from marketing and only it requests workpaper data. Reference pages are public synthetic examples. Saved client data requires the local development token or, in hosted mode, an authorized WorkOS firm session and client access.

### Independently hosted backend

Copy `apps/frontend/.env.example` to `apps/frontend/.env.local` and set:

```dotenv
VITE_API_BASE_URL=http://127.0.0.1:8080
```

Restart Vite (or rebuild the frontend for deployment). Leave this value empty to retain the same-origin Vite proxy/Cloudflare ingress. The base is the backend origin, without `/v1`; workpaper reads and exports both use it. All `VITE_*` values are public—never include credentials.

For direct cross-origin access, run Go with the exact frontend origin:

```sh
FRONTEND_ORIGIN=http://127.0.0.1:5173 go run ./cmd/api
```

Run that command from `services/backend`. With Compose, set `FRONTEND_ORIGIN` in the shell before startup. With Cloudflare, set the edge Worker's `FRONTEND_ORIGIN` variable in its Wrangler configuration; it is forwarded to the Go container. An empty value enables no cross-origin browser access. CORS is not authentication. Saved-close requests use the local workspace bearer token only in development; hosted sessions use WorkOS organization JWTs and recorded membership/client access.


## First working feature: prepaid preparation

Open `/dashboard/prepaids/new` (or choose **Prepare a prepaid** in the dashboard). Enter the client label, invoice/source reference, amount, service dates, close month and account names; explicitly confirm the method and booking assumptions. The **Use the $12,000 example** button fills terms but leaves the confirmations unchecked.

The Go capability computes exact-cent allocation and a proposed reclassification, then the UI shows every month, the month-end balances and supplied source context. Changing an input clears the previous result. Export the complete draft as JSON or the schedule as CSV, or start from a client close and save an immutable draft before leaving.

Supported methods now include equal-month and actual-day prepaids, reviewed credits/cancellations/booked-balance adjustments, and several explicitly reviewed accrual treatments. Client currency, basis, eligibility and approved procedures constrain each method. Review the [method definitions, synthetic examples and accountant approval flow](docs/accounting-methods.md). These remain draft calculations, not verified ledger facts or permission to post.

- Capability: `POST /v1/workpapers:prepare` (`application/json`, maximum 16 KiB).
- Contract: `packages/contracts/prepaid.ts`; implementation: `services/backend/internal/prepaids`.
- Go checks: `go test ./internal/prepaids ./internal/transport` from `services/backend`.
- [Feature scope and acceptance](docs/prepaid-preparation.md).

Cross-origin JSON POST is supported only for the configured `FRONTEND_ORIGIN` and `Content-Type` / `Authorization` preflight. No credentialed session or authentication is implied. This fast deterministic operation needs no queue or model call; future agents call the same capability.

## Saved closes and reconciliation

Open `/dashboard/closes` to unlock the private workspace and create a client/month. Start prepaid or statement preparation there to save drafts. Reopen a saved workpaper to inspect the original inputs and calculation. Standalone reconciliation is at `/dashboard/reconciliations/new`. See [scope, API, setup and limits](docs/saved-closes-and-reconciliation.md).

## AuthKit and sandbox posting

[Setup and verification boundaries](docs/auth-sandbox-staging.md) covers hosted sign-in, explicit sandbox approval/execution/readback, and the staging workflow. Provider accounts and live validation are still required. The private local token cannot authorize a posting. Per-client connectors are the multi-client path; the legacy global sandbox journal is single-firm.

Revised workpapers and PDF/OCR: [workflow, local setup and activation status](docs/recalculation-and-pdf.md).

Change-focused review and consolidated unsent client requests: [workflow and API](docs/change-review-and-requests.md).

Private client request links: [workflow, scope and setup](docs/client-request-links.md).

Next-month prepaid carry-forward: [workflow, accounting boundary and setup](docs/carry-forward.md).

Approved firm procedures and assigned client accounting authority: [workflow and roles](docs/firm-procedures.md).

Approved optional email setup and recovery: [Resend delivery](docs/email-delivery.md).

In-app assistant that answers from your scoped reads: [context, history and boundaries](docs/chat.md).

Hand a close to the agent until it is ready for review: [close goals](docs/close-coordinator.md#hand-the-close-to-the-agent).

Close-wide scope and recovery: [coordinator flow](docs/close-coordinator.md) · [operations and local restore rehearsal](docs/operations.md).

QuickBooks per-client setup: [sandbox OAuth and account snapshots](docs/quickbooks-onboarding.md).

Stripe payouts traced to bank deposits, from exports or the client's Stripe account, including manual and instant payouts with a computed balance roll-forward: [settlement reconciliation](docs/settlement-reconciliation.md).

Coverage resolution, consolidated requests and mandatory scanned client attachments: [complete workflow and scanner setup](docs/coverage-requests.md).
