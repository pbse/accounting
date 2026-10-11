# Workpaper review and evidence changes

Open a saved close, expand a draft and choose **Review decision and history**. Inspect the source and calculation, then approve the workpaper (a note is optional) or request changes (a note saying what needs changing is required). A close's Overview opens drafts ready for approval in place, one after another: what the agent did, the effect on the books and whether the balance then ties, the sources, then Approve (one click) or Return with a note. Payroll is approved on its own page, with its checks and its two confirmations together. Filter saved drafts by awaiting review, approved, changes requested or evidence changed. Filters apply to the displayed 100 most recent drafts.

Approval records the exact workpaper digest, reviewer identity, note and time. Concurrent decisions use the last review ID: a stale browser must reload rather than overwrite another decision. Decisions append to history. Workpaper review does not grant posting authority or firm close sign-off.

Approval requires current linked evidence and no unresolved calculation blockers. Older drafts without uploaded evidence must be prepared through source-linked instructions first. Accepting unresolved exceptions is not implemented. Hosted users need the WorkOS `workpapers:review` permission; the shared local token records decisions as `local-workspace (development)`, not as a named accountant.

## When evidence changes

1. Upload the corrected text or CSV into the same close.
2. Open **Replace or compare source evidence**, select the original and newer source, and inspect the before/after text.
3. Explain what changed and choose **Replace source and invalidate affected review**.

Both originals remain immutable. The replacement is explicit; merely uploading a file does not supersede anything. A source can be superseded only once, by a newer current source in the same close; this prevents cycles and cross-close replacement.

Affected workpapers become `source_changed` immediately, while unrelated reviews remain valid. Earlier approval decisions stay in history but are no longer current approval. New instructions cannot use a superseded source, and queued jobs fail visibly instead of calculating from replaced evidence. Previously completed jobs remain historical execution records, not evidence of present readiness.

Use current evidence to enter/review the corrected terms, save new instructions and prepare a new draft. The product does not infer corrected accounting values or auto-approve the new result. Comparison currently shows source text side by side; field-level calculation diffs and automatic dependent recalculation remain future work.

## Posting boundary

Source replacement and requested changes invalidate affected unexecuted sandbox approvals. Approve/execute checks the current review state while holding the same close lock used for replacement; it commits the durable attempt before making the provider call. The lock is not held across the external request.

Replacement and requested changes are blocked while an affected posting is executing or its outcome is unknown. Resolve that existing attempt first. Verified receipts remain in history even after evidence changes; no entry is automatically reversed. Workpaper approval alone never posts anything, and the sandbox still requires its separate explicit approval and execution confirmation.

## Setup and API

Apply `infra/postgres/migrations/004_workpaper_review.sql` to existing databases before starting the updated API/worker. Fresh Compose volumes apply it automatically. Staging readiness now checks the review view; actual hosted deployment remains unverified.

- `GET /v1/closes/{id}/workpapers/{paper}/reviews`: current state, digest, last review ID and decision history.
- `POST` to that route: `{decision,note,digest,expected_review_id}`. Decisions are `approved` or `changes_requested`. A request for changes needs a note (1–2,000 bytes); an approval's note may be empty (up to 2,000 bytes; migration 164).
- `POST /v1/closes/{id}/evidence/{source}/replace`: `{replacement_id,reason}`.

The existing close read and ZIP manifest include current review state. The manifest also preserves review history and evidence replacement relationships/reasons. The HTML export states each draft's current review state. Firm sign-off remains `not signed off`.

The implementation reuses native Postgres constraints, one computed view and short close-level locks. No graph engine, approval framework, model call, provider integration or additional dependency is introduced. Single-workspace access remains the current boundary; this is not a multi-firm permissions release.
