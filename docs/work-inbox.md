# Live Work inbox and evidence coordinator

The dashboard reads real preparation jobs across client closes, prioritizing decisions and failures, then client requests and running work. It polls every three seconds and stops on an access/network error until refreshed. The close page shows the same inbox scoped to that client/month. Client/month search and state filters run on the server before the 100-job cap. Blockers include failed jobs, unsupported proposals, replaced sources and resulting workpaper/review blockers; decisions, running work and outstanding requests have separate views.

## Experience without model credentials

1. Upload evidence in a client close. The upload transaction creates or reuses one durable preparation job alongside the evidence. Existing evidence can be re-uploaded to start its first intake; migration does not silently prepare old files.
2. The worker waits for PDF extraction, then proposes labeled invoice facts with source IDs, exact quotes, and line locations. Missing or conflicting fields produce specific questions. No document content can confirm policy or trigger posting.
3. Inspect originals, correct values, confirm the entity and accounting assumptions, then submit. The same job becomes queued; the existing deterministic worker saves an exact-cent workpaper. Preparation remains distinct from workpaper approval, posting, and sign-off.
4. Without an AI provider, an uploaded `id,date,amount` CSV is recognized from its header. (With one configured, the agent reads and pairs statement and ledger files itself.) Work shows **Recognized reconciliation CSV** with its row count, date range and net total. The header cannot tell a bank statement from a ledger export, so the reviewer chooses its role on the reconciliation page; a file with a row outside the month is shown as unsupported with the reason. For other statement/ledger CSVs, follow the reconciliation link, confirm the existing reconciliation form and pair source files, then use **Confirm linked facts and continue this job**. That source-linked save queues the original intake job automatically. An unused companion-file intake can be cancelled.
5. If clarification is needed, edit a specific request or load an existing request draft, enter its exact recipient, and approve. It remains explicitly **not sent**. Staff may record delivery already performed through their own secure channel, then record the original reply and its reported sender. The app sends nothing and does not verify the sender's identity.

When a source names the client differently (its `Client entity:` line reads, say, "Acme Services LLC" for the close "Acme Services"), preparation stops and the work item shows that name. If it is the same client, choose **Same client as …**: the name is remembered as a confirmed alias for this client and the work is rechecked; later documents using it are accepted. Only the one name the blocked source gives can be confirmed. A source naming two different entities stays blocked; replace it with the correct document.
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

**Straight-through prepaid drafts.** An agent-read prepaid invoice skips `needs_confirmation` and is queued as a draft when every fact the calculation needs (amount, service start and end, currency) is one verified candidate cited from the invoice itself, whose quote prints it, the agent has read who issued the invoice, who it bills and its number (three cited facts the confirmation form leaves out, each the whole of what its quoted line ends with as whole words, the number on a line that calls it an invoice rather than an order, reference or date, the recipient on a line that says who is billed rather than shipped to) and it bills the client or a confirmed alias (names compared ignoring case, punctuation and spacing), there is no open question and no evidence in the close the agent left unused or could not read, the client's enabled prepaid procedure names equal full months and both accounts (a cited account that differs from the procedure stops it), the invoice is in the client's home currency, and the close month's newest completed QuickBooks capture on the reading connection shows the invoice expensed: exactly one bill or purchase dated in the close month, from the cited vendor and bearing the cited invoice number as its document number, books exactly that amount to the procedure's expense account and nothing to any other account, its total being that amount, no journal in the month posts to either account, no vendor credit, card credit or deposit in the month comes from that vendor or touches either account, and every line of a journal, credit or that vendor's record names its account, by name or by an ID the same connection's newest chart of accounts resolves; a journal or credit in another currency counts, and a journal, a credit from the vendor or that bill without readable lines stops it. The client's home currency must be detected from a connected ledger, and the firm's basis and procedure check that the preparation worker applies runs first, so a draft they would refuse waits for a person. Replay and the track record count only confirmations by people. A bill on the prepaid account, credits, cancellations, booked balances read from the invoice, actual-day procedures, a pending or failed newer capture, and every other kind still wait for a person. The draft cites the capture as a `period_ledger` source, so a later capture reopens its review like any other source change; the intake records `agent` as its confirmation, which the track record's confirmed-proposal figures leave out, and the checkpoint says either that the draft was prepared without confirmation or why it waits for a person. The draft is reviewed and approved like any other, and nothing posts.

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

The model cannot generate transactions, authorize policy, approve workpapers or post journals. A preparer must review facts and confirm the existing accounting assumptions. Unsupported or ambiguous work stays a question, not a calculation.

It proposes one of these kinds of work, each finished by an existing calculator:

| Kind | Primary source | Other sources | Confirmed in |
| --- | --- | --- | --- |
| Prepaid, accrual | the invoice | support | Work |
| Reconciliation | the statement or ledger CSV | the other CSV, support | Work |
| Deferred revenue, depreciation | the customer or purchase invoice | support | the Deferred revenue or Depreciation form |
| Loan | the lender statement | the note or agreement and amortization schedule (support), one ledger page | the Loan schedule form |
| Owner item | the receipt or transaction record (support) | one ledger page | the Owner items form |

For the form kinds, Work lists the cited facts and opens the form with them filled in (fields named as the form's, each only when the sources agree on one value; the source reference is the job's own document) and the proposal's sources linked, one per role with the job's own source first, located at its first citation or left for the preparer to locate. Saving continues the same job. The model never chooses a method, day-count, owner direction or treatment, business-use share, useful life, salvage value, convention or disposal; loan payment rows are entered from the cited statement, and a field the form shows only after another choice is not filled. A note, agreement or schedule uploaded alone is support for its statement's job. A loan without its note or ledger page, an owner item without its ledger page, depreciation without support for the booked accumulated depreciation, or deferred revenue citing a booked balance without its support, asks for it, since the form saves one source for each role; Work opens the form only once the agent has no questions left. Stripe payout exports are recognized without a model and go to the settlement form; native corrections start from the exceptions below.

Use **Save answer and resume agent** to retain a staff answer as immutable evidence and resume the same job. Approved client requests still require staff-recorded delivery and original replies; replies resume the same interpreter. New uploads wake unresolved OpenAI proposals. Fresh facts require another confirmation, after which the existing worker prepares automatically. Concurrent evidence changes discard model output; changes after review block execution until rechecked.

Each call covers at most 20 current ready files / 128 KiB extracted text, has a 45-second HTTP timeout and a 6,000-output-token ceiling. Claims expire after two minutes and allow at most three automatic attempts; a human retry or answer resets the budget. Provider errors, refusals, malformed output and invalid citations fail visibly. Oversized closes use manual preparation. A companion-file intake can be cancelled when another job uses it as evidence.

`preparation_intakes` stores the claim, proposal revision, model/response identifiers and token counts. `preparation_answers` links staff answers to immutable evidence. The existing queue and calculators remain the execution path (Ponytail rung 2); the provider call uses Go's HTTP/JSON standard library (rung 3). No SDK, provider framework or second queue was added.

Validation uses a mocked provider with the real local database. It covers quoted prose, missing information, same-job answers, cross-client exclusion, inventory changes during calls, and both supported preparation kinds. It does **not** establish live model accuracy or credential access. Cloud deployment checks require migration 008, but the staging workflow does not forward OpenAI credentials; hosted AI activation remains separate.

## Planning, ranking and bulk approval

Each close can carry a due date, a client priority (high, normal, low) and an owner. Taking a close assigns it to you; only the owner can release it, and nobody can take a close someone else holds. Planning is scheduling metadata: it never grants review, posting or sign-off authority.

Work ranks jobs by what they need first (decisions and failures, then blocked or changed-dependency work, then client requests, then running work, then the rest), and within each group puts your closes first, then the earliest due date, then higher priority, then the newest. These are the only ranking signals today; there is no policy-defined risk model, and no score is invented. **Mine** shows your active work; **Due in 7 days** counts closes by date only, including overdue ones, so a changed sign-off never hides a date.

The review queue can approve up to 20 drafts at once, but only drafts that change nothing in the ledger and carry no reviewer judgment: no proposed adjustment, no open questions, no reviewer-chosen or in-transit payouts and no reviewer classifications. Everything else says why it needs individual review. The confirmation lists every draft, its effect and exceptions, the total effect and your authority. Each draft is rechecked inside its own transaction with fresh firm, membership and client-access checks, so a stale or inaccessible draft is refused while the others proceed. Apply migration 038 with the API and worker stopped; `/readyz` checks it.

## Work needing re-review

**Work needing re-review** lists current drafts whose review was reopened through an explicit link: a cited source that changed or was replaced, or a changed predecessor in a carried schedule or recurring accrual. Below it, **May need re-review** predicts drafts that no link reaches: an approved, un-revised draft in an open (not signed-off) close whose balance-sheet account (a prepaid, liability, loan, owner, settlement bank or reconciled account, matched by name) has a transaction in the latest QuickBooks capture that QuickBooks updated after the approval. It names the transaction, the account and both times, and counts such transactions. The update may not change the balance, so the approval stands; the draft leaves the list once it is approved again after the update or revised. Expense and revenue accounts, Xero captures and procedure changes are not predicted.

## Exceptions the agent found

The close Overview scans every row of the latest completed QuickBooks transaction capture, with no model call. It raises three kinds of question:

- **Possible duplicate:** same transaction type, counterparty, currency and amount within three days. Monthly recurring payments are not flagged.
- **Holding account:** a line posted to an Uncategorized, Ask My Accountant or Suspense account.
- **No payee:** a Purchase with no payee.

**Needs a schedule:** a Bill, Purchase, journal entry or Deposit that adds to a prepaid account (subtype `PrepaidExpenses`), a fixed-asset account (not accumulated depreciation or amortization) or a deferred-revenue account (subtype `DeferredRevenue`, unverified against live companies), with no schedule in the close that cites it.

- Additions are netted per transaction. A transfer between two prepaid accounts, an amortization or recognition entry, and a card refund are not new additions.
- Each account the transaction adds to is its own item, so a split bill can be covered account by account.
- A current schedule (the latest version) covers a transaction only when its source or booking reference contains the transaction's document number as a whole word (INV-88 does not match INV-88-1), with the same amount and currency. For prepaids and deferred revenue the account must also match; for depreciation, the cost.
- A transaction without a document number or a captured currency keeps asking.
- The question asks the client for the invoice, agreement or contract and the service period, or the in-service date. The agent then prepares the schedule from that evidence; nothing is posted.
- Account types come from the newest QuickBooks period snapshot. Without one, the scan says it is waiting. An account missing from the snapshot's list, or a bill line posted through an item (whose account is not captured), makes the scan incomplete, and the Exceptions panel and the close brief both say so.
- These items are computed when the close is opened, so Daily Books does not list them.

Each item names the QuickBooks transaction IDs, the evidence still missing and a plain client question. **Copy client questions** puts them on the clipboard for a client request. A holding-account or inconsistently coded QuickBooks Bill, expense or Deposit, or Xero bill, links each transaction (up to five) to **Prepare a correction**, which opens its reviewed correction with that transaction chosen; the preparer picks the line, the new account and the accountant's rule, and previews, approves and runs it as before. Nothing is recategorized, deleted, merged or posted by the scan. Owner or personal purpose is never inferred. Without a capture, the card asks for one rather than reporting "no exceptions".

## Where this close stands

The top of the close Overview narrates the close in the order an accountant walks it (`GET /v1/closes/{id}/brief`, MCP `close_brief`), with no provider or model call:

1. **Ledger:** the month-end Trial Balance in use, or a request to capture one.
2. **Preparation:** documents prepared, in progress, needing confirmation, waiting on the client, or failed.
3. **Review:** current drafts (the latest version of each) that are approved, ready for approval, or need attention.
4. **Balance sheet:** the tie-out summary, naming up to three accounts per problem.
5. **Exceptions:** the client questions found.
6. **Sign-off:** signed off, ready to sign off (an action for the person), or what it waits on.

Each line is marked done, needs you, waiting, or not checked, and links to its tab. The card shows when it was read; **Refresh** reads it again after work elsewhere on the page. The headline lists what needs a person, such as "3 things need you: approve 2 drafts, support 1 balance, send 4 client questions." It reads the same tie-out, exceptions and readiness endpoints as their own panels. A source that cannot be read is reported as not checked and named in the headline, never shown as clear. Review counts cover current drafts only, so they can be lower than the Overview's counts, which include earlier versions.

## Balance sheet tie-out

The close Overview compares every asset and liability account in the newest month-end QuickBooks or Xero Trial Balance with the balance the close's workpapers support (`GET /v1/closes/{id}/tie-out`), with no provider or model call. Only the latest version of each workpaper counts, as in the year-end handoff. Support is the unexpired prepaid, minus unearned deferred revenue, minus depreciation to date on the accumulated account, minus the accrual's target on its liability, the loan's principal split between the loan and current-portion accounts (the current portion capped at principal, as the loan calculator does) plus accrued interest, and the reconciled ledger balance for banks (credit for cards). A workpaper's account is matched to exactly one Trial Balance account by name or full name, ignoring case.

Each account **ties**, **ties after proposed entries** (the month-end entries still to post close the gap), **differs by less than the firm's materiality** (a nonzero difference below the materiality amount set in Firm settings, with no proposed entry pending; per account, in the Trial Balance's currency; a matter to note, not to explain, and never an approval or a waived entry), **differs**, waits on a **posting outcome unknown** (checked first, since a write in flight may change the balance), or has **no support yet** with a hint on what support to add. A match against a workpaper that is not approved or has blockers is marked provisional. An entry already posted to the snapshot's company before the capture began (for Xero, before the Trial Balance was requested, a time each capture now records) is treated as part of the balance; one posted later, or a Xero journal against a capture made before that time was recorded, still counts as pending. QuickBooks and Xero journals count only against a snapshot of their own company or organisation. Support that names no account, several accounts, or an account that is not an asset or liability is listed rather than counted, as is a workpaper prepared in a currency other than the Trial Balance's, and so are Trial Balance accounts with no class, because the board may then be incomplete. Zero-balance accounts with no support are left off. Equity is not tied and receivables and payables are not compared with aging. An account that **differs** blocks sign-off (close readiness names it with both balances and the difference) until the support or the ledger changes; nothing else on the board blocks it, and the board never changes the ledger.

## Changes from last month

The close Overview explains the largest Profit and Loss movements from last month's close to this one (`GET /v1/closes/{id}/flux`, MCP `explain_changes`). It uses each month's newest QuickBooks period snapshot on the client's reading connection, or its newest Xero capture ([Xero](xero-onboarding.md#changes-and-client-memory)), and makes no model or provider call.

- **Changes shown:** accounts whose reported amount moved, largest movement first, up to eight. The two months' snapshots and capture times are shown; figures are as captured, so a posting made after last month's capture is not included. No materiality threshold is implied.
- **Possible drivers:** each change lists the payees behind it from the General Ledger's Name column. That means new this month, not in this month's capture, or billed a different amount, plus any amount with no payee name. The ledger's sign for an account is turned to match the P&L when its month total is exactly opposite (as income can be). Up to three payees per group are shown, with a count of the rest. When those payees do not add up to the whole movement, they are labelled possible drivers.
- **Missing payees:** a payee billed last month and not in this month's capture raises a question written for the client: was there a bill or invoice for the month, and if so, please send a copy. The claim is never made when any of this month's rows carries no payee name, even if they net to zero. **Copy client questions** copies them.
- **Asking the client:** for a QuickBooks close, the panel drafts the questions into one client request for a person to approve. It includes each account that is still open in [account coverage](coverage-requests.md) for the same capture and not already in a request whose link is live or answered, and drafts nothing while either month's capture is out of date. The person checks the books (a bill recorded after the capture is not in these figures), edits the wording and confirms the recipient, which defaults to the close's latest consolidated request. Approving reads both months and coverage again, and stops for a fresh review if the draft would change. It then creates the consolidated request, bound to those accounts, with its private link; the email is approved separately, in the same place. An account already asked shows its request. An unanswered request made on an earlier capture can no longer be answered, so the panel says to withdraw it in Coverage before asking again. Xero closes have no account coverage, so their questions are copied.
- **Wording:** the panel says expense or income only when the account's type is known.
- **Waiting:** it waits instead of comparing when there is no snapshot for either month, when the two months came from different companies or currencies, or when one month's newest books come from QuickBooks and the other's from Xero. Only a single-period P&L (account and total columns) is read.

## Client memory when confirming

When a person confirms a model reading, each account field (prepaid: expense and prepaid account; accrual: expense and liability account; reconciliation: account) can carry a note (`GET /v1/closes/{id}/work/{job}/hints`):

- **Not in this QuickBooks snapshot:** the proposed account matches no single account in the close's newest period snapshot on its reading connection. A stale snapshot, or none, says nothing.
- **Your approved procedure names X:** the client's current enabled procedure sets that account and the proposal differs.
- **Approved before for this client:** the accounts in this client's approved workpapers of the same kind over the 12 close months ending with this one, with counts and the last month. It uses the latest recorded review decision, ignores revised versions, and shows the top three. The note is hidden when the proposal already matches the most common choice; otherwise the other choices are shown. Notes are read again when Work is refreshed.

**Use** fills in the input, but only for an account that is a single active account in the current snapshot and agrees with the approved procedure. Nothing is filled in by itself; the person still confirms, and every existing check still applies. No model is called. Changing treatment for good stays with learned procedures.

### What we remember about a client

The top of a close's **Settings** tab (`GET /v1/closes/{id}/memory`) shows what the system remembers about the close's client, with who decided it and when:

- **Other names its documents use:** each confirmed alias, who confirmed it and when. A reviewer (`workpapers:review`) can retire one with a reason (`POST /v1/closes/{id}/memory`, `retire_alias`); it stays listed with who retired it, when and why. Later documents using that name stop for a person again, work already done is unchanged, and the name can be confirmed again from Work. Deleting one of the client's closes for contract termination or at the client's request deletes all of its names, retired ones too.
- **Standing approvals:** each unrevoked grant for the client, with the draft and close it was granted on, who granted it, when and why. A grant is marked not in force when its approval changed or the granter left the firm (the same check approval uses). Each links to its draft, where the existing **Revoke** control (with its confirmation) withdraws it.
- **Accounts approved before:** the accounts named in the client's currently approved drafts (not revised, and no source changed since approval) over the 12 close months ending with this close (the same top three the confirm hints use), each linked to the latest approved draft that used it and who approved it when. This is a record, not a rule: **Use** while confirming fills in that one reading, while a rule for future preparation is the client's procedure, shown right below with its scope, who approved it, when and the version this close uses.

Migration `080_alias_retirement.sql` adds who retired an alias, when and why, and lets a retired name be confirmed again.

## Agent track record

**Measure** shows, per client, how the model's work has held up over the last 12 close months, including this one (`GET /v1/closes/track-record`, MCP `agent_track_record`). It uses only recorded history, with no model call.

**Proposals kept as proposed.** For each client document the model read and a person confirmed, it compares the model's kind and values with what the person confirmed. Case and spacing are ignored. Amounts compare by value (1,200 = 1200.00), but identifiers compare as text (0012 is not 12). A blank the person filled in counts as filled, not as a change. The fields people changed most are listed.

**Drafts approved without revision.** For the drafts those confirmations produced, it counts the latest recorded review decision (approved or sent back) and whether a person's revision replaced the draft. A draft that was sent back and then revised counts as both; "approved without revision" means approved and never replaced. Replaced evidence does not rewrite an earlier judgment. A queued revision that produced nothing is not a revision.

Not counted: rule-based and bank-export readings, carried-forward and recurring work, and drafts people saved directly; the page states how many of those there are. It is a record of what happened, not a promise of future accuracy. It grants the agent no authority.

## Check the current agent

On a close's **Workpapers** tab, **Replay confirmed readings** reads up to 3 of the close's most recently confirmed model readings again with the current model (`POST /v1/closes/{id}/replay`, one model call each). It uses the evidence the agent originally selected, recorded in its checkpoint, and compares each new reading with what the person confirmed. Each document then shows "same as confirmed" or the fields that differ. A value the person confirmed that the new reading leaves out counts as a difference.

A document is not replayed when evidence in the close has changed since it was confirmed, when it names another client, or when its original selection was not recorded. A reading is left unscored if evidence changes while the model is reading. Only the comparison is saved (migration 056): the panel shows the latest run for the close (`GET /v1/closes/{id}/replay`), and no reading, job or workpaper changes.

The worker runs the same check once an hour for each enabled firm not yet checked on the current `OPENAI_MODEL`: it replays the close with the firm's newest confirmed model reading, as `scheduled-replay`. A run counts as the firm's check only once it scores at least one reading, so a run where every document was skipped is retried the next hour. Changing `OPENAI_MODEL` makes every firm due again. The morning brief shows the latest automatic check: how many scored readings came out the same, with a link to its close.

This is a check of interpretation, not of evidence selection. It needs `OPENAI_API_KEY` and `OPENAI_MODEL` and a person's session, and it is not exposed to agents. Its calls, like every other model call, count against the firm's daily limit (see operations.md). Each person can run one replay at a time. It works best on past closes, whose evidence no longer changes.

## Good morning

The firm's Work page opens with a morning brief (`GET /v1/closes/morning`, MCP `morning_brief`). It covers the unsigned current and prior-month closes the person can access, with no model call.

- **In the past 24 hours:** month-end snapshots the schedule captured, new drafts prepared (a person's revisions and hand-saved drafts are not counted), and carried schedules approved under standing approvals (matched exactly to their grant and granter). It is a rolling day, not "since you last looked".
- **Needs a person today:** each close's drafts to approve or fix, documents to confirm, failed documents to retry, a missing month-end snapshot for an ended month (QuickBooks or Xero), and Daily Books questions new since the close was last reviewed. Closes are ranked by how much is waiting and the top 20 are shown, with the total. The brief says how many other closes have none of these waiting; tie-out and changes stay on each close.

Below it, **Handed to the agent** lists the delegated closes you can see and where each stands ([close goals](close-coordinator.md#hand-the-close-to-the-agent)).

## Attention inbox

**Attention** in the sidebar (`GET /v1/closes/attention`) lists what needs the signed-in person across the unsigned closes they can access (all of the firm's with firm access, otherwise their assigned clients), grouped by close and newest first. Nothing is stored as a task: each item is read from current state and disappears when that state changes.

- **Items:** a client's latest link reply to a request in the past 14 days (marked partial when questions are outstanding), facts to confirm, failed preparation, each current draft ready for approval, changes requested on a draft, a replaced source on a current draft (saying "after it was approved" when its last review approved it), a scanned client file or held email reply waiting for release, an approved request for work still waiting on the client with no delivery recorded, a request email that was not delivered, and, for the person who handed a close to the agent, that it is ready for review or that someone else stopped the agent (for 14 days), and a document of a delegated close [handed to the person](close-coordinator.md#hand-a-document-to-a-teammate) while it is with them, linked to the document in Work. Running, queued, waiting and finished work is never an item.
- **Read and snooze:** each person marks items read or unread and snoozes them for a day or a week (up to 30 days); snoozed items move to their own view and come back when the time passes. An item's key changes when its situation does (the client replies again, a job is rechecked, a draft's sources change), so it comes back unread. Marks can be set only on items the person can currently see, and marks for items that no longer exist are dropped.
- **Assignment:** a close's owner is the existing take/release assignment, shown on each group with a **Take** or **Release** button and an **Assigned to me** view; there is no separate assignment model for closes.
- **Follow-ups on delegated closes:** a hand-off still open after two days adds a follow-up for the teammate under **Documents handed to you**. After four days, or sooner for a close that is due, the goal's owner gets one under **Delegated closes that need you** (which also holds closes the agent has ready for review, and goals someone else stopped). See [the close coordinator](close-coordinator.md#hand-the-close-to-the-agent).
- **Preferences:** each person chooses which kinds count toward their unread badge (default: all; a kind added later, such as documents handed to you, counts for someone who saved a choice once they add it), and whether to see a browser notification for newly seen items while the workspace is open. The notification says only how many items are new, never a client, amount or document; clicking it opens Attention, which reads with the person's current access. Nothing is sent by email, chat or push service.
- Items are shown 200 at a time with **Older items** and **Newer items** (the All, Assigned to me, Unread and Snoozed views filter before paging, and a page past the end shows the last one); the unread count and notifications cover every item. Jobs are dated when they last changed state and requested changes when they were requested. Removing a member deletes their marks and preferences. Migration `079_attention.sql` adds `attention_marks`, `attention_preferences`, the time of each link reply (earlier replies take the later of their reply document's time and their request's newest attachment) and each job's last state change (kept by a trigger).

## Daily books

Work lists **Daily books**: for each accessible client's unsigned current- or prior-month close, the exceptions the latest QuickBooks or Xero transaction capture found that were not in the capture last marked reviewed. Captures run daily, QuickBooks sandbox ones also soon after QuickBooks reports a change (production companies are picked up by the daily capture), and Xero ones soon after Xero reports an invoice or credit note change when its webhook is set up, so new duplicates, holding-account postings, payments without a payee and inconsistent coding surface during the month instead of at close. Each capture records its findings when it finishes; a capture with unchanged transactions recomputes them from the earlier transactions and its freshly read open items. A finding is the same when its type, transaction kind and ledger IDs match; after reconnecting to a different company or organisation, or when the client's books move between QuickBooks and Xero, everything counts as new. Clients with the most new findings come first, up to 100.

**Mark reviewed** sets the baseline to the latest capture. The list reloads with Work's Refresh. It is refused if a newer capture finished in the meantime, and it is a review marker only: it approves nothing and changes nothing in QuickBooks. Open the close's exceptions to answer or ask the client. Balance checks still use month-end snapshots captured on demand.

A Xero client is listed from its newest Xero capture for the month when that is newer than its latest QuickBooks capture. Each Xero capture records its findings when it is saved; one without transactions records none, and the client is then listed as **check incomplete** with the reason (for example, a connection made before transactions could be read, or an older capture that recorded no findings), never as clear; it cannot be marked reviewed. **Mark reviewed** then points at that Xero capture, and only the capture shown can be marked, from either ledger. Captures saved before this change record no findings.

**Documents and ledger transactions** on each close sets the uploaded prepaid invoices beside the bills and purchases of the latest transaction capture, so a missing bill or a missing invoice shows during the month. A document counts only when it was uploaded to this close, read as prepaid work, and its total was quoted from the invoice itself; one whose total came from another source, was conflicting, or is not a plain decimal such as `1200.00` is listed as without a total. A document and a transaction are a possible match only when they are the only document and the only transaction with that currency and exact amount; two or more on either side are listed together for a person. For a Xero client the bills and purchases come from the newest Xero capture ([Xero](xero-onboarding.md#transactions)). There is no date, name or fuzzy matching. The rest are listed as documents not found in this month's captured bills and purchases, and bills and purchases without an uploaded document in this close (largest first, up to 200 with the total). A transaction without a currency takes the connection's home currency, and one with an unreadable total is counted. The list is read when the panel opens, so each capture updates it. Nothing is stored, linked or changed in the ledger.

## Cash operations: overdue receivables and payables

Each transaction capture also reads the invoices and bills still open in QuickBooks, dated on or before month-end. The close's exceptions (and Daily books) then ask about invoices more than 60 days past due at month-end, whether they are still collectible and whether anything was agreed, adding a pointer to the firm's allowance or write-off policy past 90 days, and about bills more than 30 days past due, whether they are still owed or were paid outside QuickBooks. Balances are as of the capture, so an item paid after month-end raises no question; days past due are counted at month-end. Items without a due date are skipped. If QuickBooks refuses an open-item query, the transaction capture still completes and its completeness note says which open items were not read. Nothing is written off, paid or sent to a customer or vendor; collections, payment runs and statements need their own authority.

The capture also reads a year of customer payments, credit memos and supplier credits up to month-end and keeps those with money still unused (QuickBooks does not document unused amounts as a query filter, so they are selected after reading). An unapplied payment of any age asks what it settles, an open invoice or an advance; a customer credit memo or supplier credit unused for more than 30 days asks whether it will be applied or was refunded. Nothing is applied or changed. A refused or over-limit read is named in the capture's completeness note on the QuickBooks panel.

**Receivables and payables aging** on the close uses the same open items from the latest completed transaction capture. Receivables are grouped by customer and payables by vendor (by QuickBooks ID and currency), and open invoices and bills are aged against the close's month-end by due date: current, 1–30, 31–60, 61–90 and over 90 days. An item without a due date is shown as unaged. An unreadable balance is listed but left out of the totals. A Xero client's aging uses the open items of the newest Xero capture, which reads every open item dated on or before month-end, with no one-year limit.

Unapplied payments, unused credit memos and supplier credits are listed apart. What they settle is not known, so they are never netted into an invoice. The balances are the ones at capture time: a document paid after month-end but before the capture is not shown, and, for QuickBooks, unused credits older than a year are not read. The page says so, and flags a capture whose open items were not fully read as incomplete.

**Download statement** gives a customer's CSV with this basis stated, safe to open in a spreadsheet, for the firm to use through its own channel. Nothing is sent, collected, paid or written off; statements to customers and payment runs need their own authority.

Open items are kept out of the transaction evidence digest, so a change that touches only them leaves the transaction evidence and its approvals as they were and records new questions. A payment can still change the balance on an invoice or bill dated in the close month; that record is part of the month's transactions and follows the existing evidence rules.
