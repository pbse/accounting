# Change-focused review and consolidated request drafts

Scope: PRD REV-03's draft comparison and COM-01's R1 request drafting. No email, recipient approval, secure client links, replies, reminders, accounting changes or cloud activation are included. Existing tenant/authentication and posting boundaries remain unchanged.

Open **Review decision and history** on a recalculated workpaper to see its predecessor and changed fields. The API compares exact persisted inputs, sources, schedules, results and proposals. Decimal amounts remain strings; no floating-point delta is inferred. Unchanged fields are omitted; changed schedules and transaction lists remain fully inspectable. Original workpapers, source downloads and review history remain available. Comparison is informational and does not approve the revision.

## The close's client request

A close has one client request, drafted by the agent from everything the month needs from the client (`draftRequest`): its documents' client questions, the records behind accounts no document or draft covers, and the pages its drafts lack, each in the client's words with what answering it unblocks. What needs the firm's own judgment (a draft's other blockers, an account to review or exclude, a reviewer's note) is listed as kept with the firm and never asked. The close's goal drafts it as it works; chat's request card, an agent's `draft_client_request` and the Requests tab draft the same request on demand (`POST /v1/closes/{id}/requests/proposed`), so every entry point shows one draft: unchanged for the client, it stands, and with nothing left to ask it is withdrawn. The panel that shows it (`ProposedRequest`) lets a person reword each question and add questions of their own, asked after the drafted ones and kept when the agent drafts again, then approve it, with its email or alone. With nothing for the client, the Requests tab still lets a person draft a request from a question of their own. The earlier editor that worded requests from selected workpapers, and could carry a reviewer's note to the client, is gone.

Request drafts are kept: the Requests history and the Work inbox (which can approve a single document's request in a saved draft's wording) read them, each flagged stale when the evidence or reviews it was drafted from changed. A scheduled ledger capture (the daily QuickBooks transaction sync or period refresh) does not on its own, as it never answers a client question; one that replaces a source a selected workpaper cites still does, through that workpaper's review. Drafts never represent sent messages or client responses.

The close export includes the latest 20 drafts in `manifest.json` under `request_drafts_latest_20`, explicitly scoped as unsent drafts.

API, all through the existing authenticated close boundary:
- `GET /v1/closes/{id}/workpapers/{paper}/changes`: same-close predecessor and exact changed fields.
- `GET /v1/closes/{id}/requests`: latest 20 draft revisions, with current draft staleness.
- `GET /v1/closes/{id}/requests/proposed`: the close's current drafted request, if any.
- `POST /v1/closes/{id}/requests/proposed`: draft it now; optional `questions`, the firm's own (each 1–1,000 characters, at most 50 in all). Answers the draft, or none, with what stays with the firm.

Apply migration `006_request_drafts.sql` to existing databases. Fresh Compose databases apply every migration. API readiness and staging deployment now require that table. Local native API must be restarted after the migration/build.

Acceptance: exact before/after amounts and multi-row schedules; consolidated questions, same-close access, immutable revisions, idempotent retry, concurrent-edit rejection and stale-context rejection. Focused transport tests plus browser verification cover this slice. Delivery/reply automation remains a separate feature.

Minimalism: reuse workpaper lineage/auth/SQL transactions (rung 2), Go JSON and deterministic templates (rung 3), native tables/checkboxes/textareas (rung 4). No request engine, messaging provider, new dependency or model framework. Codex authored this continuation of the initial unborn repository; no origin/main exists for a separate worktree. CommitPR review and cloud activation have not run.

The [Work inbox](work-inbox.md) now adds approval, staff-recorded delivery, and original replies linked to the same durable preparation job. Draft revisions themselves remain separate from delivery records.
