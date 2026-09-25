# Product-state migrations

`001_saved_closes.sql` creates single-workspace client/month closes and immutable workpaper snapshots. PostgreSQL uniqueness protects repeated creation and repeated saves. Calculation results are JSONB snapshots; money remains exact decimal strings from Go integer arithmetic.

Apply with `psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f infra/postgres/migrations/001_saved_closes.sql`. Fresh Compose volumes apply it automatically; existing volumes need an explicit migration. Do not apply production migrations as part of container startup. Migration and runtime roles must be separated before hosted use.

This schema does not claim tenant isolation, durable jobs, approvals or posting. Add those with their owning features. See `docs/saved-closes-and-reconciliation.md`.

`003_evidence_preparation.sql` adds immutable bounded text evidence, preparation instructions and durable close jobs. Existing databases must apply it explicitly before running the new API and worker. See `docs/evidence-and-preparation.md`. These later migrations extend the original single-workspace schema; they do not add multi-firm isolation.

`002_sandbox_posting.sql` adds digest-bound sandbox intents, expiring human approvals, durable execution attempts and readback status. Apply it explicitly to existing databases before enabling sandbox posting. See `docs/auth-sandbox-staging.md`; no production ledger writer is enabled.

`004_workpaper_review.sql` adds append-only review decisions, explicit evidence replacement metadata and the derived workpaper review-state view. Apply before starting the updated API and worker. Hosted reviewers need `workpapers:review`; development decisions remain attributed to the shared workspace. See `docs/workpaper-review.md`.

`019_email_followup.sql` extends the existing outbox with uniquely linked reminders and signed, deduplicated delivery events. Apply after 018 with API/worker stopped, then restart both. Fresh volumes apply it automatically. See `docs/email-delivery.md` for per-firm webhook setup and synthetic local checks.

`020_accruals.sql` permits accrued-expense drafts and procedure versions. Apply after 019 with API/worker stopped. No existing workpaper or policy is rewritten. See `docs/accounting-methods.md`.

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
