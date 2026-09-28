# Product-state migrations

Apply every numbered SQL file in filename order for a fresh database. These migrations collectively implement firm isolation, durable jobs, review and posting; the notes below describe individual historical additions, not the full current schema.

## Fresh hosted database

Use a dedicated database and its owning role for migration. Set `DATABASE_URL` privately to that database's **direct** TLS endpoint. From the repository root, the following applies every migration with the runner, which records each in its ledger (see below), only after verifying that `public` has no application tables:

```sh
bash <<'SH'
set -euo pipefail
: "${DATABASE_URL:?Set the approved fresh database owner URL privately}"
test "$(psql "$DATABASE_URL" -XAt -v ON_ERROR_STOP=1 -c "SELECT count(*) FROM pg_tables WHERE schemaname='public'")" = 0
infra/postgres/migrate.sh
SH
```

Create a separate login role, for example `close_runtime`, with no superuser, role creation, database creation or BYPASSRLS, and no membership in the owner role. Set its password through the provider's secure role UI or interactive `psql` `\password close_runtime`, never a committed command. As the migration owner, grant it `CONNECT` to this database, `USAGE` on schema `public`, `SELECT, INSERT, UPDATE, DELETE` on all tables in `public`, and `USAGE, SELECT` on all sequences in `public`. Apply matching `ALTER DEFAULT PRIVILEGES` **for the migration owner** so future tables/sequences receive those grants. Views are included in PostgreSQL's table grants. Do not grant schema creation or ownership.

Use this runtime role's direct TLS URL for both API and worker; never use Neon's transaction-pooling endpoint with the current session-context adapter. Hosted `/readyz` rejects runtime RLS bypass and missing policies. Local Compose uses a superuser and cannot prove hosted isolation; test two firms with the restricted hosted role before client use. See [operations](../../../docs/operations.md#row-level-security).

## Existing database upgrades

`infra/postgres/migrate.sh` applies the files a database does not have yet, in numeric order, as the migration owner (`DATABASE_URL`). It records each file in a ledger, `migrations.applied`, in the same transaction as the file itself, so a file is either applied and recorded or neither; a failing file stops the run with nothing of it kept. The ledger sits outside the `public` schema, which hosted readiness requires to be entirely under row-level security. Runs wait for each other on an advisory lock, and a file changed after it was applied stops the run: add a new migration instead. With `MIGRATION_GRANT_TO` set to the app's role, it then grants that role the app's privileges on every table and sequence in `public`. CI runs it on every change, twice, and the second run must apply nothing.

A migration runs while the previous code may still be serving, and stays if the deploy that needs it fails, so it must work with the code already running: add tables and columns, and drop or rename only in a later release, once no running code uses them.

A database migrated before the ledger (it has tables, but no `migrations.applied`) is refused until it is told once what it already has. Set `MIGRATION_ADOPT` to a file of `migration|SQL condition` lines. Each file whose condition holds is recorded as adopted without running, and every other file is then applied. For example, `001_saved_closes.sql|true` records a file known to be in place, and `095_chat_every_month.sql|EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name='chat_messages' AND column_name='every_month')` records one only if what it adds exists. After that first run the ledger decides, and `MIGRATION_ADOPT` is ignored.

Back up before a hosted run. The script applies each file while the API and worker keep running, so they need no stop for migrations that follow the rule above. Afterwards, check `/readyz` plus a durable job.

Fresh Compose volumes run every migration automatically, without the ledger; to use the script on such a volume, adopt everything it already has first. Both deploy workflows run the script before deploying, as the owner in the `MIGRATION_DATABASE_URL` secret, granting to the runtime role named in `DATABASE_URL`. Deploy production runs on every push to `main`, so a merged migration reaches production with its code. Production was migrated by hand through 090, so its workflow adopts with `infra/postgres/adopt-before-ledger.txt`.

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

`094_workflow_engine.sql` creates the workflow engine's tables: runs, steps, events and plans, each with row-level security by firm (a `NULL` firm is platform work, visible to system work only). It is the only migration the engine needs; new workflows are directories, not schema. Apply it before deploying a build whose worker runs the engine. See [workflow engine](../../../docs/workflow-engine.md).

`097_shared_plan_templates.sql` lets every firm read a plan template that belongs to no firm, so a firm can copy a shared template into a draft of its own. It adds reading only.

`101_content_grants.sql` adds `content_grants`: a staff member's time-boxed access to one firm's client content in the admin panel (visible to system work and the firm). See [admin panel](../../../docs/admin-panel.md).

`102_audit_started.sql` lets a staff audit entry be `started`: an action with an effect outside the database (a WorkOS session revoke or invitation resend) records that before the effect, and its outcome after. Apply it before deploying a build that records one.

`100_platform_alerts.sql` adds `platform_alerts` (system work only): the platform's own problems, one open alert per kind and item, delivered to the operations webhook. See [operations](../../../docs/operations.md).

`098_admin.sql` adds the staff audit log (`platform_audit`, append-only: a trigger refuses updates, deletes and truncation for every role; visible to system work and the affected firm) and `worker_heartbeats` (system work only) for the admin panel. See [admin panel](../../../docs/admin-panel.md).
