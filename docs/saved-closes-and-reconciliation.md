# Saved closes and statement reconciliation

## Scope and acceptance

Create/open a client and month; save server-recomputed immutable prepaid and reconciliation drafts; reopen the exact saved calculation and its inputs. Retrying the same save returns the same record. Client and period must match the close. The database is PostgreSQL; no browser or container filesystem substitutes.

Prepare a USD bank-account workpaper from manually supplied statement and ledger CSV. Check reported page inventory, date boundaries, duplicate IDs, exact cents, each source's opening-plus-movement roll-forward, and opening/closing differences. Produce unique exact date/signed-amount match candidates and preserve every unmatched/ambiguous row. Nothing becomes reconciled, approved, posted, or signed off.

Each unmatched row now carries a review hint: a non-unique exact candidate, the same signed amount on another date, another amount on the same date, or no counterpart in the imported rows. The workpaper and close export keep source-side row IDs, up to five candidate IDs and the full candidate count. These hints are leads for the reviewer, not matches, timing classifications or proposed journal entries. Missing pages, roll-forward gaps and balance differences remain explicit blockers. The package includes `-exceptions.csv` alongside the original unmatched-row CSVs.

Unmatched rows also show exact two-row group candidates on the same date and with the same sign. Both one-statement-to-two-ledger and two-statement-to-one-ledger cases are inspected. The first five source-ID pairs and full group count appear in the workpaper and export; rows stay unmatched until an accountant reviews the original transactions. Date tolerances, larger groups, transfer relationships and native reconciliation still require separate approved methods.

Non-goals: document extraction, independently verified page continuity, card accounts, tolerance/grouped matching, prior-period outstanding-item rollforward, native ledger reconciliation, model execution, queue jobs, multi-user identity, posting. Marketing and the existing prepaid method are unchanged. This is the first bounded reconciliation method, not all of PRD REC-01.

Ponytail ladder: reuse the existing calculator and React form patterns; stdlib CSV, JSON, integer arithmetic, HTTP and token comparison; native PostgreSQL constraints and transactions. One new runtime dependency, pgx, supplies the required PostgreSQL driver. No repository interface, ORM, auth-provider abstraction or queue for short synchronous calculations.

## Local setup

Copy `.env.example` to `.env`, generate `WORKSPACE_TOKEN` with `openssl rand -hex 32`, and keep `.env` private. The token grants access to the entire single workspace. It is not a user identity or firm-level authorization model. The browser retains it in tab memory only; refreshing requires unlocking again. Use synthetic/non-sensitive data during development. WorkOS AuthKit is the likely hosted identity choice; it is not configured in this feature.

`npm run dev` uses Compose's Postgres and applies SQL migrations on a fresh database volume. For an existing volume, apply explicitly:

```sh
docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/001_saved_closes.sql
```

Native Go needs `DATABASE_URL`, `WORKSPACE_TOKEN` and optional exact `FRONTEND_ORIGIN` exported before `go run ./cmd/api`. Go does not implicitly load `.env`. Apply migration with `psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f infra/postgres/migrations/001_saved_closes.sql` from the repository root. Migration runs separately from API startup.

Open `/dashboard/closes`, unlock, create a close, choose a preparation type, then save the resulting draft. Inputs must match the selected client and month. Changing inputs requires a new calculation and creates a new snapshot; old snapshots are retained. Lists currently show the latest 100 records and explicitly disclose truncation. This local development ceiling needs pagination before larger workspaces.

Standalone preparation still supports export without saving. CSV format is `id,date,amount`, one source ID per row, dates within the selected full month, positive deposits and negative withdrawals. Header-only means no movement. Limits: 2,000 rows per source, 512 KiB request, 1,000 statement pages. Page presence is user-reported, not PDF inspection. Matched rows are candidates and cannot establish population completeness. No unexplained balancing entry is generated.

## API for UI and agents

- `POST /v1/reconciliations:prepare`: typed `ReconciliationInput` to deterministic draft; no persistence.
- `GET /v1/closes`: private close list.
- `POST /v1/closes`: `{entity, period}`; create or open existing case-insensitive entity/month.
- `GET /v1/closes/{id}`: close and saved workpapers.
- `POST /v1/closes/{id}/workpapers`: `{kind: "prepaid" | "reconciliation", input: ...}`. Recalculates on the server; never trusts client-supplied results.

All saved-close routes require `Authorization: Bearer <WORKSPACE_TOKEN>`. Missing configuration fails closed. Exact-origin CORS permits Content-Type and Authorization, not credentialed cookies. Repeat writes are deduplicated by server-calculated content digest. Preparation endpoints remain stateless and public like prepaid preparation; they do not log/store inputs. There is no MCP transport or autonomous agent coordinator yet.

## Cloudflare and CI

Edge forwards server-side `DATABASE_URL` and `WORKSPACE_TOKEN` secrets to Go. Never put either in a VITE variable or Wrangler plaintext vars. Cloud deployment needs a reachable TLS-enabled PostgreSQL database, separately applied migrations, restricted database roles and hosted identity/data controls before real client use. No provider was provisioned and no cloud secrets or deployments were changed.

CI has a disposable Postgres service, applies migration, and runs the persistence test through `TEST_DATABASE_URL`. Docker Compose validation uses a non-production CI token. Local focused checks:

```sh
cd services/backend
TEST_DATABASE_URL=postgres://... go test ./internal/transport ./internal/prepaids ./internal/reconciliation
go vet ./internal/transport ./internal/reconciliation
```

The test database must be disposable: the persistence test inserts and removes its own synthetic fixture. These checks validate software behavior, not accounting-policy approval or hosted multi-tenant security.

## Subsequent identity and sandbox integration

WorkOS AuthKit and the controlled sandbox-posting path now have implementation code and local checks. They require provider configuration and live verification before activation. The development token remains local-only and cannot authorize posting. See `auth-sandbox-staging.md` for the current setup and verification boundary.
