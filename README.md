# Close preparation

**Client attachment requirement:** configure [Cloudflare WAF upload scanning](docs/cloudflare-upload-scanning.md) before releasing client PDFs. PDFs need Cloudflare; local TXT/CSV use the separate scanner service.

A foundation for an agent-first accounting preparation workspace. React presents the decisions; Go owns capabilities and accounting; Cloudflare supplies delivery and runtime adapters. The connected ledger remains the ledger of record.

**Implemented locally:** saved closes; exact prepaid, accrual and reconciliation calculations; reviewed prepaid carry-forward and recurring accrual refresh; secure evidence upload with PDF/OCR; source-linked review and recalculation; a live Work inbox that connects proposed facts, approved requests, held email replies, client replies, and durable preparation. OpenAI interpretation needs worker credentials; without them, local proposals recognize labeled fields and require review. WorkOS AuthKit, QuickBooks sandbox VendorCredit and controlled production prepaid posting, and Cloudflare/GitHub delivery have code but require provider activation. Supported-scope readiness, sign-off, release-gated accountant binders with PDF workpapers, firm membership and operational controls are available locally. See [the Work inbox guide](docs/work-inbox.md).

See [onboarding and end-to-end flows](docs/onboarding.md), [enterprise controls and recovery](docs/operations.md), and the [current delivery backlog](docs/implementation-backlog.md). Multi-firm API access is bound to enrolled WorkOS organizations; existing local data belongs to the development workspace. Scoped MCP exposure and live enterprise validation remain pending.

## Start locally

Install Docker with Compose v2.22+ and Node.js 24 (`nvm use` if installed), then:

```sh
cp .env.example .env
# Set WORKSPACE_TOKEN and a separate UPLOAD_SCAN_KEY in .env
# Generate each using: openssl rand -hex 32
npm run dev
```

Open http://localhost:5173. Compose installs locked dependencies, starts the API on localhost:8080, the worker on localhost:8081, and Postgres on localhost:5432. React hot-reloads; Go edits rebuild through Compose Watch. Stop with Ctrl-C; `docker compose down` removes containers while preserving the database volume.

No Cloudflare account, model key, live ledger, or Go installation is needed for this path. Saved closes connect to Postgres. Fresh Compose volumes receive all numbered migrations; existing volumes need the explicit migration command in the onboarding guide. Its local-only credentials are in `compose.yaml`; never reuse them in cloud environments.

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
workers/queue/               Queue delivery adapter + Go worker container
workers/workflows/           Durable coordinator boundary and activation requirements
packages/contracts/          Shared reference shape and future API/MCP contract rules
infra/postgres/migrations/   Ordered SQL migrations for saved closes/workpapers
docs/                        PRD, full review, architecture and delivery decisions
.github/workflows/           CI and gated manual staging deployment
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

React Router handles links and browser history; Cloudflare's existing SPA fallback supports direct URL loads. The dashboard is loaded separately from marketing and only it requests workpaper data. Reference pages are public synthetic examples. Saved client data requires the private workspace token.

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

Run that command from `services/backend`. With Compose, set `FRONTEND_ORIGIN` in the shell before startup. With Cloudflare, set the edge Worker's `FRONTEND_ORIGIN` variable in its Wrangler configuration; it is forwarded to the Go container. An empty value enables no cross-origin browser access. CORS is not authentication. Saved-close requests separately require the workspace bearer token; hosted user sessions use WorkOS organization JWTs.


## First working feature: prepaid preparation

Open `/dashboard/prepaids/new` (or choose **Prepare a prepaid** in the dashboard). Enter the client label, invoice/source reference, amount, service dates, close month and account names; explicitly confirm the method and booking assumptions. The **Use the $12,000 example** button fills terms but leaves the confirmations unchecked.

The Go capability computes exact-cent allocation and a proposed reclassification, then the UI shows every month, the month-end balances and supplied source context. Changing an input clears the previous result. Export the complete draft as JSON or the schedule as CSV, or start from a client close and save an immutable draft before leaving.

Supported: USD accrual-basis prepaids using equal full months or inclusive actual days (up to 120 months), one prospective credit and adjustments against a confirmed booked prepaid balance. Fixed incurred accruals with optional next-month reversal proposals are also available from a client close. Review the [method definitions, synthetic examples and accountant approval flow](docs/accounting-methods.md). These remain draft calculations, not verified ledger facts or permission to post.

- Capability: `POST /v1/workpapers:prepare` (`application/json`, maximum 16 KiB).
- Contract: `packages/contracts/prepaid.ts`; implementation: `services/backend/internal/prepaids`.
- Go checks: `go test ./internal/prepaids ./internal/transport` from `services/backend`.
- [Feature scope and acceptance](docs/prepaid-preparation.md).

Cross-origin JSON POST is supported only for the configured `FRONTEND_ORIGIN` and `Content-Type` / `Authorization` preflight. No credentialed session or authentication is implied. This fast deterministic operation needs no queue or model call; future agents call the same capability.

## Saved closes and reconciliation

Open `/dashboard/closes` to unlock the private workspace and create a client/month. Start prepaid or statement preparation there to save drafts. Reopen a saved workpaper to inspect the original inputs and calculation. Standalone reconciliation is at `/dashboard/reconciliations/new`. See [scope, API, setup and limits](docs/saved-closes-and-reconciliation.md).

## AuthKit and sandbox posting

[Setup and verification boundaries](docs/auth-sandbox-staging.md) covers hosted sign-in, explicit sandbox approval/execution/readback, and the staging workflow. Provider accounts and live validation are still required. The private local token cannot authorize a posting.

Revised workpapers and PDF/OCR: [workflow, local setup and activation status](docs/recalculation-and-pdf.md).

Change-focused review and consolidated unsent client requests: [workflow and API](docs/change-review-and-requests.md).

Private client request links: [workflow, scope and setup](docs/client-request-links.md).

Next-month prepaid carry-forward: [workflow, accounting boundary and setup](docs/carry-forward.md).

Approved firm procedures and assigned client accounting authority: [workflow and roles](docs/firm-procedures.md).

Approved optional email setup and recovery: [Resend delivery](docs/email-delivery.md).

Close-wide scope and recovery: [coordinator flow](docs/close-coordinator.md) · [operations and local restore rehearsal](docs/operations.md).

QuickBooks per-client setup: [sandbox OAuth and account snapshots](docs/quickbooks-onboarding.md).

Stripe payouts traced to bank deposits from exports: [settlement reconciliation](docs/settlement-reconciliation.md).

Coverage resolution, consolidated requests and mandatory scanned client attachments: [complete workflow and scanner setup](docs/coverage-requests.md).
