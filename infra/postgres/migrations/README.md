# Product-state migrations

Apply every numbered SQL file in filename order for a fresh database. These migrations collectively implement firm isolation, durable jobs, review and posting; the notes below describe individual historical additions, not the full current schema.

## Fresh hosted database

Use a dedicated database and its owning role for migration. Stop API/worker traffic and set `DATABASE_URL` privately to that database's **direct** TLS endpoint. From the repository root, the following applies migrations only after verifying that `public` has no application tables:

```sh
bash <<'SH'
set -euo pipefail
: "${DATABASE_URL:?Set the approved fresh database owner URL privately}"
test "$(psql "$DATABASE_URL" -XAt -v ON_ERROR_STOP=1 -c "SELECT count(*) FROM pg_tables WHERE schemaname='public'")" = 0
for migration in infra/postgres/migrations/[0-9]*.sql; do
  psql "$DATABASE_URL" -X -v ON_ERROR_STOP=1 -f "$migration"
done
SH
```

Create a separate login role, for example `close_runtime`, with no superuser, role creation, database creation or BYPASSRLS, and no membership in the owner role. Set its password through the provider's secure role UI or interactive `psql` `\password close_runtime`, never a committed command. As the migration owner, grant it `CONNECT` to this database, `USAGE` on schema `public`, `SELECT, INSERT, UPDATE, DELETE` on all tables in `public`, and `USAGE, SELECT` on all sequences in `public`. Apply matching `ALTER DEFAULT PRIVILEGES` **for the migration owner** so future tables/sequences receive those grants. Views are included in PostgreSQL's table grants. Do not grant schema creation or ownership.

Use this runtime role's direct TLS URL for both API and worker; never use Neon's transaction-pooling endpoint with the current session-context adapter. Hosted `/readyz` rejects runtime RLS bypass and missing policies. Local Compose uses a superuser and cannot prove hosted isolation; test two firms with the restricted hosted role before client use. See [operations](../../../docs/operations.md#row-level-security).

## Existing database upgrades

The repository has no automatic migration ledger. Keep an operator record of exactly which files succeeded; readiness checks are not a replacement for that record. Back up, stop API/worker, compare the recorded list with this directory, and apply only unapplied files in numeric order as the migration owner. Do not rerun all migrations against an existing volume, delete that volume to upgrade, or guess the last migration from a single table. If history is unknown or a migration fails, inspect the schema and reconcile the record before continuing. Restart API/worker from the same revision and check `/readyz` plus a durable job.

Fresh Compose volumes run every migration automatically. Existing Compose volumes do not; apply each confirmed pending file using `docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1` with that file on stdin. Hosted migrations remain an explicitly authorized operator action; the staging workflow only checks schema readiness.

## Historical migration notes

`001_saved_closes.sql` introduced client/month closes and immutable snapshots. Later migrations add organizations and permissions.

`003_evidence_preparation.sql` adds immutable bounded text evidence, preparation instructions and durable close jobs. Existing databases must apply it explicitly before running the new API and worker. See `../../../docs/evidence-and-preparation.md`. Multi-firm isolation arrives in later migrations; all files are required.

`002_sandbox_posting.sql` adds digest-bound sandbox intents, expiring human approvals, durable execution attempts and readback status. Apply it explicitly to existing databases before enabling sandbox posting. See `../../../docs/auth-sandbox-staging.md`; no production ledger writer is enabled.

`004_workpaper_review.sql` adds append-only review decisions, explicit evidence replacement metadata and the derived workpaper review-state view. Apply before starting the updated API and worker. Hosted reviewers need `workpapers:review`; development decisions remain attributed to the shared workspace. See `../../../docs/workpaper-review.md`.

`019_email_followup.sql` extends the existing outbox with uniquely linked reminders and signed, deduplicated delivery events. Apply after 018 with API/worker stopped, then restart both. Fresh volumes apply it automatically. See `../../../docs/email-delivery.md` for per-firm webhook setup and synthetic local checks.

`020_accruals.sql` permits accrued-expense drafts and procedure versions. Apply after 019 with API/worker stopped. No existing workpaper or policy is rewritten. See `../../../docs/accounting-methods.md`.

`021_ledger_selections.sql` records immutable account-scoped General Ledger pages. `022_ledger_search.sql` adds the search term to their identity, so two distinct searches at the same offset cannot reuse the wrong evidence. Apply both in order with API/worker stopped before deploying ledger search.

`023_production_posting.sql` separates production OAuth connections from sandbox connections and adds client-specific grants plus durable one-attempt production intents. Apply after 022 with API/worker stopped. No live grant or credential is created by the migration; see [production posting](../../../docs/production-posting.md).

`035_procedure_learning.sql` adds tenant-scoped procedure proposals and accountant-approved regression examples. Apply after 034 with API/worker stopped. Existing approved versions retain their current behavior; learned method rules activate only through the review flow in [firm procedures](../../../docs/firm-procedures.md).

`036_procedure_model_drafting.sql` records whether a proposal came from the local prompt or the configured OpenAI model. Apply after 035 before deploying model drafting. It does not activate, revise or approve any existing rule.

`037_settlements.sql` permits Stripe settlement drafts. Apply after 036 with API/worker stopped; `/readyz` checks it. See [settlement reconciliation](../../../docs/settlement-reconciliation.md).

`047_row_level_security.sql` enables row-level security on every public table and sets every public view to check it as the caller. Apply with API/worker stopped, as the owning role; run API and worker as a separate non-owner role without superuser or BYPASSRLS. `/readyz` checks the migration, and hosted readiness checks the runtime role. See [operations](../../../docs/operations.md#row-level-security).

`048_xero.sql` adds read-only Xero connections and month-end Trial Balance snapshots, with the same row-level security as 047. Apply after 047 with API/worker stopped. See [Xero month-end Trial Balance](../../../docs/xero-onboarding.md).

`049_quickbooks_change_hints.sql` records when Intuit reports a change for a connected company, so the scheduler refreshes early. Apply after 048 with API/worker stopped; `/readyz` checks it. See [QuickBooks onboarding](../../../docs/quickbooks-onboarding.md).

`050_daily_books.sql` stores each transaction capture's exceptions and the capture last reviewed per close, with row-level security like 047. Apply after 049 with API/worker stopped; `/readyz` checks it. See [Work inbox](../../../docs/work-inbox.md#daily-books).

`051_client_profile.sql` stores each QuickBooks and Xero connection's home currency and first fiscal month, read at connect time (QuickBooks) or at each snapshot (Xero). Apply after 050 with API/worker stopped; existing connections stay unknown (USD only) until reconnected or snapshotted. `/readyz` checks it.

`052_settlement_posting.sql` lets a production grant cover Stripe settlement fee entries. Apply after 051 with API/worker stopped; `/readyz` checks it.

`053_production_corrections.sql` lets a production grant cover Bill expense corrections and supplier credits and records the grant on each attempt. Apply after 052 with API/worker stopped; `/readyz` checks it.

`054_client_aliases.sql` stores the other client names a reviewer confirmed from an identity-blocked source, with row-level security like 047. Apply after 053 with API/worker stopped; `/readyz` checks it.

`056_model_limits.sql` adds the firm's daily model call limit and its per-day count, the agent's retry time after a refusal, saved replay runs (row-level security like 055), and the claim time for scheduled Xero captures. Apply after 055 with API/worker stopped; `/readyz` checks it.

`057_link_passcodes.sql` adds the firm setting that makes new client request links need a passcode, records it in policy history, and stores each link's salted passcode digest and failed tries. Apply after 056 with API/worker stopped; `/readyz` checks it.

`058_review_minimum.sql` adds the firm's optional amount at or above which a draft's proposed entries need an independent reviewer (hosted firms only), recorded in policy history. Apply after 057 with API/worker stopped; `/readyz` checks it.

`059_idempotency_keys.sql` records, per firm, each `Idempotency-Key` used on a POST with a fingerprint of the request and its final status (never the response body), with row-level security like 055. Apply after 058 with API/worker stopped; `/readyz` checks it.

`060_xero_posting.sql` adds the per-client Xero posting grant and the saved manual journal intent/attempt, with row-level security like 048. Apply after 059 with API/worker stopped; `/readyz` checks it.

`061_stripe_reports.sql` adds a client's confirmed Stripe connection (restricted, report-scoped key, sealed) and each month's itemized payout reconciliation report run, with row-level security like 048. Apply after 060 with API/worker stopped; `/readyz` checks it.

`062_xero_daily.sql` records the findings of each Xero snapshot's transactions and lets a close's Daily books review point at a Xero snapshot instead of a QuickBooks capture (exactly one of the two). Apply after 061 with API/worker stopped; `/readyz` checks it.

`063_index_rates.sql` stores the SOFR and EFFR values a preparer looked up from the New York Fed, keyed by index and published date, so a loan's rate from an index is checked against them. Shared reference data with an open row-level security policy. Apply after 062 with API/worker stopped; `/readyz` checks it.

`064_line_recodes.sql` lets a correction recode one line of an expense (Purchase) or an income deposit as well as a Bill, keyed by kind, records the accountant's rule reference with each correction, and adds the `purchase_correction` and `deposit_correction` grant kinds. Apply after 063 with API/worker stopped; `/readyz` checks it.

`065_credit_applications.sql` records each unapplied customer payment applied to an open invoice and each vendor credit applied to an open bill (preview, approval, one attempt, readback), with row-level security like 050, and adds the `payment_application` and `credit_application` grant kinds. Apply after 064 with API/worker stopped; `/readyz` checks it.

`066_materiality.sql` adds a firm's optional materiality amount and records it with every firm policy change. Apply after 065 with API/worker stopped; `/readyz` checks it.

`067_xero_change_hints.sql` records the newest change time a signed Xero webhook reported for each connection, so a hinted close is captured again early. Apply after 066 with API/worker stopped; `/readyz` checks it.

`068_xero_corrections.sql` records each reviewed recode of one Xero bill line (preview, approval, one attempt, readback), with row-level security like 060. Apply after 067 with API/worker stopped; `/readyz` checks it.

`069_stripe_report_kinds.sql` lets a close capture Stripe's itemized Payouts and balance activity reports as well as the payout reconciliation, one of each in flight. Apply after 068 with API/worker stopped; `/readyz` checks it.

`070_reminder_rules.sql` adds a firm's reminder rule (days after delivery before a reminder is due, 0–6, default 0; reminders per request, 1–5, default 3) and records it with every firm policy change. Apply after 069 with API/worker stopped; `/readyz` checks it.

`071_agent_attribution.sql` records which MCP client made evidence, intake jobs, close preparations, request drafts and approval requests through a member's approved agent access, beside the member who remains their author. Apply after 070 with API/worker stopped; `/readyz` checks it.

`072_chat_messages.sql` stores each member's assistant conversation for one close or firm-wide, read only by its author, with row-level security like 047. Apply after 071 with API/worker stopped; `/readyz` checks it.

`094_workflow_engine.sql` creates the workflow engine's tables: runs, steps, events and plans, each with row-level security by firm (a `NULL` firm is platform work, visible to system work only). It is the only migration the engine needs; new workflows are directories, not schema. Apply it before deploying a build whose worker runs the engine; the deploy workflows check for it. See [workflow engine](../../../docs/workflow-engine.md).
