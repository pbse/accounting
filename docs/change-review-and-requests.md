# Change-focused review and consolidated request drafts

Scope: PRD REV-03's draft comparison and COM-01's R1 request drafting. No email, recipient approval, secure client links, replies, reminders, accounting changes or cloud activation are included. Existing tenant/authentication and posting boundaries remain unchanged.

Open **Review decision and history** on a recalculated workpaper to see its predecessor and changed fields. The API compares exact persisted inputs, sources, schedules, results and proposals. Decimal amounts remain strings; no floating-point delta is inferred. Unchanged fields are omitted; changed schedules and transaction lists remain fully inspectable. Original workpapers, source downloads and review history remain available. Comparison is informational and does not approve the revision.

Each client close has a **Consolidated client request** editor. Select up to 20 saved workpapers and generate a draft. Deterministic templates use linked-source presence, reconciliation blockers and requested-change review notes; exact duplicate questions are removed. No model is called and document meaning is not inferred. The reviewer must verify whether the requested information is already available, remove internal notes and edit client-facing wording before any external use.

Every save retains an immutable revision with the actor, selected workpaper IDs and a context digest. Identical retries reuse the latest matching revision. Concurrent edits cannot overwrite newer wording. New evidence, extraction status, source replacement, selected workpaper review or supersession changes mark the current draft stale. A scheduled ledger capture (the daily QuickBooks transaction sync or period refresh) does not on its own, as it never answers a client question; one that replaces a source a selected workpaper cites still does, through that workpaper's review. Generated wording carries no trailing whitespace, and a saved draft is approved from Work exactly as saved. Edited text cannot be saved against stale context; inspect current work and generate a fresh revision first. Earlier wording remains visible for reference. Drafts never represent sent messages or client responses.

The UI shows the latest 20 saved revisions. All revisions remain in PostgreSQL. Close export includes the latest 20 in `manifest.json` under `request_drafts_latest_20`, explicitly scoped as unsent drafts. No recipient/contact is guessed.

API, all through the existing authenticated close boundary:
- `GET /v1/closes/{id}/workpapers/{paper}/changes`: same-close predecessor and exact changed fields.
- `GET /v1/closes/{id}/requests`: latest 20 draft revisions, with current draft staleness.
- `POST /v1/closes/{id}/requests`: `expected_id`, `workpaper_ids`; omit `body` to generate. Editing additionally supplies `body` and the returned `context_digest`. Server enforces scope, size, snapshot and concurrent-edit checks.

Apply migration `006_request_drafts.sql` to existing databases. Fresh Compose databases apply every migration. API readiness and staging deployment now require that table. Local native API must be restarted after the migration/build.

Acceptance: exact before/after amounts and multi-row schedules; consolidated questions, same-close access, immutable revisions, idempotent retry, concurrent-edit rejection and stale-context rejection. Focused transport tests plus browser verification cover this slice. Delivery/reply automation remains a separate feature.

Minimalism: reuse workpaper lineage/auth/SQL transactions (rung 2), Go JSON and deterministic templates (rung 3), native tables/checkboxes/textareas (rung 4). No request engine, messaging provider, new dependency or model framework. Codex authored this continuation of the initial unborn repository; no origin/main exists for a separate worktree. CommitPR review and cloud activation have not run.

The [Work inbox](work-inbox.md) now adds approval, staff-recorded delivery, and original replies linked to the same durable preparation job. Draft revisions themselves remain separate from delivery records.
