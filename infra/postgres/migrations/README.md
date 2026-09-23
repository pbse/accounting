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
