# Live Work inbox and evidence coordinator

The dashboard reads real preparation jobs across client closes, prioritizing decisions and failures, then client requests and running work. It polls every three seconds and stops on an access/network error until refreshed. The close page shows the same inbox scoped to that client/month. Client/month search and state filters run on the server before the 100-job cap. Blockers include failed jobs, unsupported proposals, replaced sources and resulting workpaper/review blockers; decisions, running work and outstanding requests have separate views.

## Experience without model credentials

1. Upload evidence in a client close. The upload transaction creates or reuses one durable preparation job alongside the evidence. Existing evidence can be re-uploaded to start its first intake; migration does not silently prepare old files.
2. The worker waits for PDF extraction, then proposes labeled invoice facts with source IDs, exact quotes, and line locations. Missing or conflicting fields produce specific questions. No document content can confirm policy or trigger posting.
3. Inspect originals, correct values, confirm the entity and accounting assumptions, then submit. The same job becomes queued; the existing deterministic worker saves an exact-cent workpaper. Preparation remains distinct from workpaper approval, posting, and sign-off.
4. Without an AI provider, an uploaded `id,date,amount` CSV is recognized from its header. (With one configured, the agent reads and pairs statement and ledger files itself.) Work shows **Recognized reconciliation CSV** with its row count, date range and net total. The header cannot tell a bank statement from a ledger export, so the reviewer chooses its role on the reconciliation page; a file with a row outside the month is shown as unsupported with the reason. For other statement/ledger CSVs, follow the reconciliation link, confirm the existing reconciliation form and pair source files, then use **Confirm linked facts and continue this job**. That source-linked save queues the original intake job automatically. An unused companion-file intake can be cancelled.
5. If clarification is needed, edit a specific request or load an existing request draft, enter its exact recipient, and approve. It remains explicitly **not sent**. Staff may record delivery already performed through their own secure channel, then record the original reply and its reported sender. The app sends nothing and does not verify the sender's identity.
6. A recorded reply is immutable evidence. The same job returns to proposal review. Contradictory values remain unresolved; method/booking confirmations must be supplied again. The original reply, approver, delivery reference, facts, final inputs, confirmation actor and resulting workpaper remain linked.

## Deliberate limits

With both OpenAI settings absent, no AI provider is called. The proposal reader recognizes only `amount` / `invoice amount`, `service start`, `service end`, `currency`, `expense account`, and `prepaid account`, followed by a colon. Underscore variants are accepted. It does not infer invoice meaning, completeness, eligibility, account mappings or dates from prose. Blank/unsupported documents require manual facts or the reconciliation form. At most ten candidate quotes per field are displayed, while every encountered value participates in conflict detection. Full originals remain inspectable.

Recipient approval and reply capture are workspace operations, not an email integration or client portal. [Private client request links](client-request-links.md) now accept scoped text replies. Inbox polling, reminders and automated sending remain separate. Current accounting methods, hosted organization boundary and posting controls are unchanged. One database still serves one configured firm; multi-firm and per-client staff grants are separate work.

Every mutating inbox action requires the existing hosted `workpapers:review` permission (or the explicitly labeled local development actor), the correct close and the current revision. Requests must belong to that job; only the latest approved request can receive a delivery record or reply. Source replacement or stale proposals block confirmation. A retry cannot silently overwrite newer decisions. Cancelled jobs are terminal; failed extraction must first be repaired using the evidence controls, then rechecked in Work.

## Persistence and activation

Apply migrations `007_work_inbox.sql` and `008_agent_preparation.sql` in order after 001–006, and restart both API and worker. Fresh Compose databases apply all migrations. Readiness and staging deployment require the new schema. No additional dependency is required; model interpretation is optional and requires the worker settings below. Native PDF extraction still needs Poppler/Tesseract, as described in the PDF guide.

The existing `preparation_jobs` row owns the lifecycle:

```text
upload → waiting_evidence → interpreting* → needs_confirmation → queued → completed / failed
                              ↓          ↑
                         waiting_client ─┘ (recorded reply → waiting_evidence)
```

`preparation_intakes` stores proposals, their revision and confirmation actor. `work_requests` references immutable `request_drafts` and records approval/delivery separately. `work_replies` links the original evidence to the approved request. The queue remains PostgreSQL-backed; no second queue or workflow engine was introduced. Local calculations and their completion are committed atomically. Workers resume waiting/queued jobs after a restart.

Authenticated API additions:

- `GET /v1/closes/work` and `GET /v1/closes/{id}/work` — actual job states, proposals and request/reply history.
- `POST /v1/closes/{id}/work/{job}/{action}` — `confirm`, `answer`, `request`, `delivered`, `reply`, `retry`, `cancel`; every action supplies `revision`.
- Existing `POST /v1/closes/{id}/preparations` additionally accepts `work_id` and `work_revision` to continue an intake from the existing calculator forms.

Close exports include `work_history` in the manifest: jobs, intake proposals/confirmation, approved request wording, staff answers and reply provenance. Reply originals are normal evidence entries. Export refuses work histories over 16 MiB rather than silently omitting them.

Acceptance: automatic upload intake, linked candidate facts and missing/conflicting questions; confirmed prepaid/reconciliation preparation on the original job; exact approved request wording; original reply retention; stale/cross-close rejection; untrusted content cannot authorize calculations or posting; visible saved result and retained history. Focused coverage is `TestWorkInboxCoordinator`, with existing upload/request/export checks as direct consumers.

Minimalism: reuse authentication, evidence, draft requests, domain validation and the durable queue (rung 2); Go strings/JSON/mail and SQL transactions (rung 3); native forms and disclosures (rung 4). No agent framework or speculative provider abstraction. Codex authored this continuation of the initial uncommitted repository; it has no origin/main for worktree isolation. No commit, deployment or provider activation is implied.


## OpenAI interpretation (opt-in locally)

Add `OPENAI_API_KEY` and `OPENAI_MODEL` privately to the root `.env`. Choose a model supporting strict structured outputs; no model is assumed. Restart the worker (`docker compose up -d --build worker` with Compose). Native worker launches must export both settings. Never put either key in a frontend `VITE_` variable or commit it. With only one setting supplied, interpretation fails visibly rather than silently using local proposals.

The worker uses OpenAI's [Responses structured-output contract](https://developers.openai.com/api/docs/guides/structured-outputs), `store: false`, and no executable tools. It sends current extracted text from this close only; replies and staff answers from other jobs are excluded. Configuring credentials enables this transfer of evidence to OpenAI. Documents remain untrusted input. The response proposes a supported kind, relevant evidence, quoted candidate facts and specific questions. The server validates source IDs and exact quotes; citations do not prove that the model's interpretation is correct.

The current methods remain USD equal-full-month prepaids and exact date/amount reconciliation of two original CSVs. The model cannot generate transactions, authorize policy, approve workpapers or post journals. A preparer must review facts and confirm the existing accounting assumptions. Unsupported or ambiguous work stays a question, not a calculation.

Use **Save answer and resume agent** to retain a staff answer as immutable evidence and resume the same job. Approved client requests still require staff-recorded delivery and original replies; replies resume the same interpreter. New uploads wake unresolved OpenAI proposals. Fresh facts require another confirmation, after which the existing worker prepares automatically. Concurrent evidence changes discard model output; changes after review block execution until rechecked.

Each call covers at most 20 current ready files / 128 KiB extracted text, has a 45-second HTTP timeout and a 6,000-output-token ceiling. Claims expire after two minutes and allow at most three automatic attempts; a human retry or answer resets the budget. Provider errors, refusals, malformed output and invalid citations fail visibly. Oversized closes use manual preparation. A companion-file intake can be cancelled when another job uses it as evidence.

`preparation_intakes` stores the claim, proposal revision, model/response identifiers and token counts. `preparation_answers` links staff answers to immutable evidence. The existing queue and calculators remain the execution path (Ponytail rung 2); the provider call uses Go's HTTP/JSON standard library (rung 3). No SDK, provider framework or second queue was added.

Validation uses a mocked provider with the real local database. It covers quoted prose, missing information, same-job answers, cross-client exclusion, inventory changes during calls, and both supported preparation kinds. It does **not** establish live model accuracy or credential access. Cloud deployment checks require migration 008, but the staging workflow does not forward OpenAI credentials; hosted AI activation remains separate.

## Planning, ranking and bulk approval

Each close can carry a due date, a client priority (high, normal, low) and an owner. Taking a close assigns it to you; only the owner can release it, and nobody can take a close someone else holds. Planning is scheduling metadata: it never grants review, posting or sign-off authority.

Work ranks jobs by what they need first (decisions and failures, then blocked or changed-dependency work, then client requests, then running work, then the rest), and within each group puts your closes first, then the earliest due date, then higher priority, then the newest. These are the only ranking signals today; there is no policy-defined risk model, and no score is invented. **Mine** shows your active work; **Due in 7 days** counts closes by date only, including overdue ones, so a changed sign-off never hides a date.

The review queue can approve up to 20 drafts at once, but only drafts that change nothing in the ledger and carry no reviewer judgment: no proposed adjustment, no open questions, no reviewer-chosen or in-transit payouts and no reviewer classifications. Everything else says why it needs individual review. The confirmation lists every draft, its effect and exceptions, the total effect and your authority. Each draft is rechecked inside its own transaction with fresh firm, membership and client-access checks, so a stale or inaccessible draft is refused while the others proceed. Apply migration 038 with the API and worker stopped; `/readyz` checks it.

## Exceptions the agent found

The close Overview scans every row of the latest completed QuickBooks transaction capture, with no model call. It raises three kinds of question:

- **Possible duplicate:** same transaction type, counterparty, currency and amount within three days. Monthly recurring payments are not flagged.
- **Holding account:** a line posted to an Uncategorized, Ask My Accountant or Suspense account.
- **No payee:** a Purchase with no payee.

Each item names the QuickBooks transaction IDs, the evidence still missing and a plain client question. **Copy client questions** puts them on the clipboard for a client request. Nothing is recategorized, deleted, merged or posted. Owner or personal purpose is never inferred. Without a capture, the card asks for one rather than reporting "no exceptions".

## Daily books

Work lists **Daily books**: for each accessible client's unsigned current- or prior-month close, the exceptions the latest QuickBooks transaction capture found that were not in the capture last marked reviewed. Captures run daily and soon after QuickBooks reports a change, so new duplicates, holding-account postings, payments without a payee and inconsistent coding surface during the month instead of at close. Each capture records its findings when it finishes; a capture with unchanged evidence carries the previous findings. A finding is the same when its type, transaction kind and QuickBooks IDs match; after reconnecting to a different company, everything counts as new. Clients with the most new findings come first, up to 100.

**Mark reviewed** sets the baseline to the latest capture. The list reloads with Work's Refresh. It is refused if a newer capture finished in the meantime, and it is a review marker only: it approves nothing and changes nothing in QuickBooks. Open the close's exceptions to answer or ask the client. Balance checks still use month-end snapshots captured on demand, and Xero data is not refreshed daily.
