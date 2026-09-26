# Saved closes and statement reconciliation

## Scope and acceptance

Create/open a client and month; save server-recomputed immutable prepaid and reconciliation drafts; reopen the exact saved calculation and its inputs. Retrying the same save returns the same record. Client and period must match the close. The database is PostgreSQL; no browser or container filesystem substitutes.

Prepare a USD bank or credit-card workpaper from manually supplied statement and ledger CSV. For card liabilities, use positive charges and negative payments or credits on both sources, with the client accountant's approved sign rule. Check reported page inventory, date boundaries, duplicate IDs, exact cents, each source's opening-plus-movement roll-forward, and opening/closing differences. Produce unique exact date/signed-amount match candidates and preserve every unmatched/ambiguous row. Nothing becomes reconciled, approved, posted, or signed off.

Each unmatched row now carries a review hint: a non-unique exact candidate, the same signed amount on another date, another amount on the same date, or no counterpart in the imported rows. The workpaper and close export keep source-side row IDs, up to five candidate IDs and the full candidate count. These hints are leads for the reviewer, not matches, timing classifications or proposed journal entries. Missing pages, roll-forward gaps and balance differences remain explicit blockers. The package includes `-exceptions.csv` alongside the original unmatched-row CSVs.

Unmatched rows show exact two-row, same-sign group candidates and net split candidates such as a $100 gross deposit plus a -$3 fee against a $97 bank credit. Both one-statement-to-many-ledger and many-statement-to-one-ledger cases are inspected. The first five ID pairs and full count for each kind appear in the workpaper and export. Rows remain unmatched until a reviewer checks the originals. Grouped exact matches require an accountant-approved rule; a net split requires one row against 2–5 mixed-sign components, an exact nonzero net, a cited rule and an explicit date window. Approved tolerances may permit bounded date or amount differences, but a nonzero amount difference stays blocked rather than creating a balancing entry.

For an outstanding item, the reviewed bridge uses signed USD amounts: statement opening plus prior ledger-only items minus prior statement-only items must equal ledger opening. At close, remove each prior item only when an opposite-source clearing row with the same signed amount is cited, then add current ledger-only items and subtract current statement-only items. That total must equal ledger closing. The original statement-minus-ledger difference stays visible even when the bridge ties. The previous month's latest approved workpaper can suggest its uncleared prior and current outstanding items for this month's draft; the reviewer confirms each item and links prior support before durable preparation. A changed or unapproved prior workpaper produces no suggestion. Each timing classification needs an accountant-approved rule. A transfer needs current and other account statement and ledger CSVs, four distinct cited row IDs, and equal/opposite signed amounts. No balancing entry or native reconciliation is inferred.

Synthetic review examples: (1) Opening statement $1,000 and ledger $900 with a prior ledger-only check of -$100; a current statement -$100 clearing row leaves both closes at $900 and both bridge differences $0. (2) Both open at $1,000; a current ledger-only -$50 check leaves statement $1,000 and ledger $950, while the closing bridge ties at $0. (3) A $200 transfer out of operating requires -$200 in both operating records and +$200 in both savings records. (4) One $97 bank credit may be reviewed against a $100 gross ledger credit and a -$3 fee only under an approved net-settlement rule; an unexplained one-cent residual is blocked. The client accountant must approve the method and examples before real use.

QuickBooks' own reconciliation status is separate. A reviewer may record the status observed in [QuickBooks' reconciliation report](https://quickbooks.intuit.com/learn-support/en-us/help-article/statement-reconciliation/reconcile-account-quickbooks-online/L3XzsllsK_US_en_US), and a source-linked workpaper requires the report as separate PDF or text evidence. This does not read or change a native flag through the API. Page inventory and native status remain reviewer-cited observations, not independent provider verification. Automatic timing classification, implied accountant policy and posting from this workpaper are unsupported.

## Local setup

Follow [local onboarding](onboarding.md#local-development). The shared token is for synthetic local development; hosted firms use AuthKit, enrolled membership and client assignments. Fresh Compose volumes apply all numbered migrations; existing volumes follow the [upgrade procedure](../infra/postgres/migrations/README.md#existing-database-upgrades). Native Go needs exported settings and does not load `.env` automatically.

Open `/dashboard/closes`, unlock, create a close, choose a preparation type, then save the resulting draft. Inputs must match the selected client and month. Changing inputs requires a new calculation and creates a new snapshot; old snapshots are retained. Lists currently show the latest 100 records and explicitly disclose truncation. This local development ceiling needs pagination before larger workspaces.

Standalone preparation still supports export without saving. CSV format is `id,date,amount`, one source ID per row, dates within the selected full month, positive deposits and negative withdrawals. Header-only means no movement. Limits: 2,000 rows per source, 512 KiB request, 1,000 statement pages. Page presence is user-reported, not PDF inspection. Matched rows are candidates and cannot establish population completeness. No unexplained balancing entry is generated.

## API for UI and agents

- `POST /v1/reconciliations:prepare`: typed `ReconciliationInput` to deterministic draft; no persistence.
- `GET /v1/closes/{id}/reconciliation/prior-items?account=...`: draft carry-forward suggestions from the previous month's latest approved workpaper for that client and account.
- `GET /v1/closes`: private close list.
- `POST /v1/closes`: `{entity, period}`; create or open existing case-insensitive entity/month.
- `GET /v1/closes/{id}`: close and saved workpapers.
- `POST /v1/closes/{id}/workpapers`: `{kind: "prepaid" | "reconciliation", input: ...}`. Recalculates on the server; never trusts client-supplied results.

Saved-close routes require either local development authorization or a verified AuthKit token, active firm membership and client access. Exact-origin CORS permits Content-Type and Authorization, not credentialed cookies. Repeat saves are deduplicated by a server-calculated content digest. Stateless calculators do not persist inputs. [MCP](mcp.md) and the [durable preparation coordinator](evidence-and-preparation.md) reuse the authenticated application flows.

## Cloudflare and CI

The hosted edge forwards server-only database and AuthKit settings to Go; it does not enable the local workspace token. Use the [deployment guide](deployment.md) for runtime-role isolation and complete environment setup.

CI has a disposable Postgres service, applies all numbered migrations, and runs the persistence test through `TEST_DATABASE_URL`. Docker Compose validation uses a non-production CI token. Local focused checks:

```sh
cd services/backend
TEST_DATABASE_URL=postgres://... go test ./internal/transport ./internal/prepaids ./internal/reconciliation
go vet ./internal/transport ./internal/reconciliation
```

The test database must be disposable: the persistence test inserts and removes its own synthetic fixture. These checks validate software behavior, not accounting-policy approval or hosted multi-tenant security.
