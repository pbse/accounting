# AI Close Preparation Team for Accounting Firms

**Product Requirements Document · v2.0 · September 21, 2026**  
**Mission:** Give accounting firms more prepared work, with less effort and no loss of control.  
**Customer promise:** **Your clients’ books, prepared for your review.**

## 1. The product in one page

### The job we take on

Prepare the supported month-end work for each client: gather evidence, reconcile records, build schedules, investigate exceptions, prepare adjustments, and assemble a review-ready close. The firm makes the accounting judgments and approves consequential actions. QuickBooks Online or another connected accounting platform remains the ledger of record.

> **“Prepare this month for these clients. Follow our policies. Bring me the decisions—not another list of tasks.”**

The complete product loop is **collect → reconcile → prepare → review → post approved changes → verify → carry forward**. A proposed entry, a posted entry, and a signed-off close are different results.

### What makes this a product rather than a chatbot

The system owns durable work. It finds permitted evidence, asks a consolidated question when necessary, incorporates the answer, completes the workpaper, and returns for approval. It does not stop at an explanation. Every calculation and proposed posting has traceable inputs, a policy version, and an execution history.

The central product design is the **living workpaper**. It links the source documents, ledger records, accounting rule, calculation, proposed action, and review. When a source changes, the system identifies the affected work, updates the draft, and asks the reviewer to inspect the difference rather than repeat the entire close.

### The first customer and commercial release

Start with US bookkeeping and client-accounting-services firms serving ordinary, single-entity USD service businesses on QuickBooks Online. Deliver a complete supported close-preparation workflow—not a replacement ledger, a generic error checker, or an autonomous accounting practice. The commercial release includes controlled client communication and selected, explicitly approved ledger writes.

### The experience

**Work** shows decisions and blockers. **Client close** shows one entity’s work and evidence. **Review** presents the source, calculation, proposed change, and approval together. Email and secure requests meet clients where they work; APIs and MCP provide additional access without becoming adoption requirements.

**Success means lower total human preparation, review, correction, and setup effort over repeated closes.** It does not mean more AI activity.

*Document status: build specification. Features and targets are planned, not claims of shipped performance. [U1] identifies the prior plan; [U2] identifies Gemini’s feedback. Numbered sources support external facts. Product decisions are our proposed design.*

<!-- pagebreak -->

## 2. Decisions, priorities, and how to read this document

### Decisions to lock

| Decision | Direction |
| --- | --- |
| Product boundary | Complete close preparation with review and approved execution. |
| Buyer | The firm owner or partner accountable for recurring client accounting work. |
| Initial ledger | QuickBooks Online; canonical model supports later Xero and other ledgers. |
| First end-to-end work | Statement reconciliation workpapers and straight-line prepaid schedules, developed in parallel. |
| First commercial release | Evidence collection, supported schedules and exceptions, client requests, review, selected approved posting, verification, and close package. |
| Agent-first meaning | The product performs and resumes work; people supply judgment, authority, and exceptions. |
| Differentiation | Living workpapers, change-focused review, completed evidence follow-up, and reusable firm methods. |
| Architecture | React, Go, Postgres, and Cloudflare delivery/storage/orchestration; one shared capability API. |

### Build order—not a series of unrelated MVPs

**R0 · Foundation and reference work.** Establish access, accounting procedures, representative fixtures, evidence lineage, and the review interaction.

**R1 · Useful shadow preparation.** Prepare real work from authorized evidence; request missing information in draft; review and export without production writes.

**R2 · First commercial close.** Complete the preparation-to-approved-posting loop, secure client communication, portfolio operation, and follow-up.

**R3 · Continuous preparation and differentiation.** Add mature change propagation, broader schedules, approved recurring procedures, Xero, and settlement reconciliation.

**R4 · Adjacent accounting products.** Daily Books, Year-End Handoff, and Cash Operations, with separate accounting and permission boundaries.

Engineering streams run concurrently. Safety gates determine whether a capability may act on live data; they do not require postponing the platform until one small checker proves a separate business.

### Navigation

**Product and experience:** Sections 1–9.  
**Functional requirements:** Sections 10–15.  
**Build, validation, and operating model:** Sections 16–20.  
**API, MCP, architecture, controls, and tests:** Appendices A–F.  
**Changes and external evidence:** Appendices G–H.

### The first useful result

A customer should be able to connect or import one client, approve the relevant assumptions, and receive a source-linked workpaper. An import count is not value. A missing statement is a specific blocker—not a reason to generate a convincing but incomplete reconciliation.

<!-- pagebreak -->

## 3. Evidence behind the direction

### What the research supports

There is direct competitive overlap. Basis markets small-firm accounting preparation, reconciliations, source-linked entries, and firm-specific procedures. Double markets transactional work, schedules, reconciliation, and review. Xenett markets accounting review and close workflows. These products establish an existing category; their public pages do not prove our superiority or our willingness-to-pay assumptions. [1–3]

The integration path is feasible but not frictionless. Intuit publishes company-scoped authorization and accounting API tooling; its SDK documents entity operations, reporting, and change detection. Xero publishes accounting APIs and an MCP reference server, while restricting some data access by tier and approval. [4–7]

### What we must win in actual use

| Customer burden | Proposed advantage | Evidence we will collect |
| --- | --- | --- |
| Rebuilding context across files, ledger, and conversations | One living workpaper with sources, calculations, and replies attached | Time and navigation needed to review the same item |
| Rechecking a whole package after a change | Dependency-aware refresh and a focused review diff | Reviewer effort after a late document or ledger edit |
| Chasing clients and restarting unfinished work | Consolidated requests that resume the relevant preparation automatically | Requests, follow-up effort, and time until usable evidence |
| Repeating setup and interpretation every month | Approved firm methods, client overrides, and maintained schedules | Setup and correction effort in the second and third close |

These are product bets and benchmark requirements—not claims that incumbents lack these features.

### How Gemini’s feedback changes the operating plan

Adopt the useful point: engineering progress and customer work must proceed together. A sandbox demonstrates mechanics; authorized real files reveal missing evidence, ambiguous accounting, and review effort. Shadow preparation belongs in the build sequence, not after fundraising.

Reject unsupported fundraising arithmetic. The quoted probabilities, assumed outreach conversion, and claims that two files lead to competitive term sheets have no supplied evidentiary basis. A shadow pilot also has data and confidentiality risk even when it cannot write to production. Do not call it an audit, a zero-risk exercise, or guaranteed investor validation.

### Strategic posture

We are building a substantial product in a competitive market. The first commercial promise is prepared work that requires less total effort. Competitor funding does not prohibit entry; a polished interface does not establish advantage. The working comparison must include setup, review, corrections, and any hidden labor our team supplies.

<!-- pagebreak -->

## 4. Customer, accounting scope, and outcomes

### Initial targeting choices

| Dimension | Initial position |
| --- | --- |
| Firm | US outsourced bookkeeping or client-accounting-services practice |
| Size | Approximately 5–30 staff and 30–200 recurring client entities |
| Primary buyer | Owner or partner responsible for the recurring accounting service |
| Daily users | Preparer, senior bookkeeper, and reviewing accountant |
| Client profile | Single-entity USD service business, ordinary bank/card activity and simple schedules |
| Accounting basis | Explicit per client; simple accrual clients first, cash-basis procedures separately approved |
| Existing systems | QuickBooks Online plus statements, documents, and client correspondence |

These ranges guide initial outreach; they are not market-size estimates or hard architectural limits. A firm can begin with one eligible entity and expand without a migration project.

### Roles and accountability

The **preparer** reviews matches and proposed work. The **accounting reviewer** approves treatments and ledger actions within firm policy. The **client contact** supplies explanations and documents, but cannot approve accounting changes merely by replying to an email. The **firm administrator** manages entity access, procedures, communication authority, and staff roles. Final close sign-off belongs to the firm.

### Supported first-release treatment

Statement matching and reconciliation workpapers; straight-line prepaid schedules; approved simple accrual schedules; supported exception investigation; selected adjustment preparation; source-linked review; and approved posting for tested operations. Loan schedules require separately reviewed terms and calculation methods before enablement.

Do not initially promise inventory valuation, consolidated reporting, foreign-currency remeasurement, complex revenue recognition, trust or fund accounting, tax filing, payroll execution, or autonomous payments. These require specific procedures and permissions—not merely another prompt.

### Product outcomes

**Firm outcome:** more completed preparation with less total staff effort.  
**Reviewer outcome:** a smaller set of meaningful decisions with reproducible evidence.  
**Client outcome:** fewer fragmented requests and clearer questions.  
**Technical outcome:** approved changes are applied once, checked, and reflected in dependent work.

An account with unresolved evidence can be explicitly accepted as an exception by an authorized reviewer; it cannot silently become “reconciled” or “correct.” Supported scope, source freshness, and uncompleted work remain visible in the close package.

<!-- pagebreak -->

## 5. Differentiating capabilities to build deliberately

### 5.1 Living workpapers

Store a dependency graph from evidence and source versions through calculations, proposals, approvals, and outputs. New evidence invalidates only affected nodes. A changed document can reopen the relevant review while leaving unrelated completed work intact.

**Why it matters:** the product remains useful after its first draft. **Build:** R0 data model; R1 source-linked workpaper; R2 invalidation; R3 richer dependency-based refresh.

### 5.2 Review the change, not the whole file again

Show what changed in the source, calculation, treatment, and proposed ledger effect since the last review. Group items only when they share a clear procedure and authority; allow inspection and rejection of any item. A source edit must never inherit approval from an old version.

**Why it matters:** reduces repeated review rather than merely faster preparation. **Build:** R1 basic diff; R2 dependency-aware review; R3 portfolio-scale review groups.

### 5.3 Evidence collection that finishes the job

Before contacting a client, inspect authorized evidence. Combine remaining questions into one clear request. Tie each reply to the relevant item, extract proposed facts, validate them, and resume preparation. Do not treat a client’s unverified explanation as an accounting rule.

**Why it matters:** removes the handoff between missing-information lists and completed work. **Build:** R1 drafts and uploads; R2 approved sending and reply handling; R3 governed reminders.

### 5.4 Close rehearsal before posting

Run proposed adjustments against a versioned read model and show their projected effect on supported balances and workpapers. Identify conflicting proposals and dependencies before approval. Label this a **proposed-state preview**, not an official financial statement or prediction. Confirm actual effects after posting.

**Why it matters:** lets reviewers evaluate the complete proposed change instead of isolated entries. **Build:** R2 item-level preview; R3 portfolio and dependency-aware rehearsal.

### 5.5 The firm’s methods become executable procedures

Read the firm’s checklist or policy document, draft typed steps and decision boundaries, test them on approved examples, and ask an accountant to activate a version. Client overrides are explicit. Reviewer edits can propose a rule change; they never silently train a new firm-wide policy.

**Why it matters:** preserves institutional knowledge without making the firm build workflows. **Build:** R1 approved templates; R2 reusable client procedures; R3 policy drafting and regression testing.

**Shared principle:** every ambitious feature must reduce a recurring customer task or improve a decision. None requires a separate console or onboarding journey.

<!-- pagebreak -->

## 6. First-session experience

### Two ways to begin

**Try a historical period:** upload authorized exports and source files. Clearly label their date, completeness, and whether they already contain final close adjustments. This mode prepares work and demonstrates review; it does not pretend to be a live ledger connection.

**Connect a live client:** authorize one QuickBooks company, select the period, and provide permitted documents. The product discovers accessible records and proposes a short scope. Intuit’s published authorization flow selects a company/realm; do not promise that one authorization connects an entire firm. [4]

### Three decisions—not a configuration maze

**Choose the work.** “Prepare bank reconciliation and prepaid schedules for September.” Offer supported defaults and make excluded work explicit.

**Confirm the rules.** Present accounting basis, date boundaries, relevant accounts, approved policy, and reviewer. Ask only about missing meaning. Historical entries are context, not proof of correct treatment.

**Confirm authority.** Default to preparation only. Show what the provider credential permits separately from what our application will allow. Sending external requests, posting entries, and sign-off have distinct permissions.

### What happens automatically

The agent builds an evidence inventory, identifies missing prerequisites, retrieves permitted context, and prepares the first usable item. Results appear progressively. It should not wait for every client or the full historical import to finish.

A first-session response could be:

> **Your subscription schedule is prepared.** The invoice and ledger entry are linked. Two assumptions need confirmation before review. The bank reconciliation is waiting on page 3 of the statement; a request is drafted.

This is illustrative. The system must never invent a missing document or an error to create a dramatic first impression.

### Experience targets

First useful workpaper within **30 minutes after required access and usable evidence are available**. Record end-to-end elapsed time, active customer time, and staff assistance separately. One incomplete upload does not satisfy onboarding success; the product must offer a specific next step.

Required recovery states include expired connection, missing statement page, inaccessible attachment, unsupported accounting method, mismatched entity, and ambiguous period. A failed connector is not a failed business record, and an incomplete source is not a clean close.

<!-- pagebreak -->

## 7. The action-first workspace

### Work: a short list of meaningful decisions

The home screen answers **“What needs me, what is the system handling, and what can finish next?”** It should not be a grid of charts or a large chat transcript.

Illustrative content, not actual customer results:

> **Ready for you**  
> Acme Services · September  
> Prepaid schedule and adjustment are ready. Source invoice and policy are attached.  
> **Review work**
>
> **One decision unlocks the next step**  
> Northstar Studio · September  
> The owner’s explanation is attached. Confirm the treatment before the draft can proceed.  
> **Review decision**
>
> **Working without you**  
> The remaining statement lines are being matched. No action is needed.  
> **Inspect progress**
>
> **Waiting on the client**  
> One consolidated request is awaiting a statement. Reminder follows your approved policy.  
> **View request**

### Ranking and density

Rank items using due date, dependency impact, client priority, reviewer assignment, and policy-defined risk. Ranking is not an invented probability of accounting correctness. Show a small initial set, group routine work, and keep search and filters accessible without making them mandatory.

The main action should be specific: **Review schedule**, **Confirm treatment**, **Approve request**, or **Check posting**. Avoid generic “Resolve” buttons whose consequences are unclear.

### Natural language is a shortcut, not a requirement

A persistent command surface accepts “Prepare my three clients due Friday” or “Why is this close waiting?” It creates or resumes durable work. The same jobs must also be available through direct controls, API, and MCP.

Users can stop work, narrow the scope, provide evidence, or change a deadline. The product must state which actions have already occurred and which remain cancelable.

### UI quality bar

Use clear client/period identity, absolute dates and local time zones, accessible contrast, keyboard navigation, reduced motion, and screen-reader labels. Display actual completed operations—not animated fictional thinking. Financial records and sensitive amounts must not appear in notifications visible to unauthorized staff.

**A chart is permitted only when it answers the current accounting question better than a sentence or table.**

<!-- pagebreak -->

## 8. Client close, review, and client requests

### Client close: the work, not a project-management board

Show scope, due date, source coverage, completed workpapers, pending decisions, and external completion steps. Separate “prepared,” “approved,” “posted and verified,” and “firm signed off.” No unexplained composite completion score.

A readiness sentence should be precise: **“Prepared work is ready for review. One reconciliation still needs a statement, and the firm has not signed off.”** It must not say “Books are correct” because a model finished running.

### Review: the product’s most important surface

Use one contextual canvas. The selected evidence and relevant ledger records sit beside the calculation, applicable policy, and proposed accounting effect. A compact history shows previous review, edits, and source changes. The reviewer should not need to leave the workpaper to verify basic facts.

Provide **approve**, **edit**, **reject**, **ask for information**, and **rerun**. Approval labels state what will happen: “Approve this proposal” is distinct from “Post this approved adjustment.” Bulk approval shows the complete item set, total effect, exceptions, and authority; high-risk or ambiguous items cannot hide inside it.

Review explanations must cite document page/region or transaction line, not merely a document filename. Calculations can be expanded and exported. Model interpretations remain distinct from directly observed source facts.

### The client’s experience

One consolidated request per appropriate client/period, sent in the firm’s voice after approval. Each question explains what is needed and why. Offer a secure upload and a simple structured reply; do not require the client to understand accounting workflows or create an unnecessary account.

Access links are scoped, expiring, revocable, and subject to authentication appropriate to the data. Replies containing multiple entities or contradictory information go to review rather than automatic routing. A changed bank destination is never accepted as payment authority.

### Continuity

A reply resumes the same work. A new statement revision triggers a focused recheck. An edited policy identifies dependent work before activation. A posted item is never silently undone because a model changed its mind.

**Acceptance:** a reviewer can understand the proposal, inspect its sources, and make a decision without rebuilding the evidence in another application.

<!-- pagebreak -->

## 9. Reference journey: one complete, testable example

### Scenario and approved assumptions

Use a labeled synthetic service-company fixture. An annual software invoice is **$12,000**, covers **September 1 through August 31**, and has been booked entirely as software expense. The firm has explicitly approved equal monthly allocation for this client and contract. There is no prior adjustment, tax complication, credit, or other contradictory fact in the fixture. This is a test policy—not universal accounting advice.

### Prepare

The agent links the invoice to the ledger operation, confirms dates and currency, reads the approved policy, and calls the deterministic schedule service. Under these fixture assumptions, September expense is **$1,000** and the prepaid balance at September month-end is **$11,000**.

The proposal is to debit prepaid expense and credit software expense by **$11,000**, with the correct entity, accounts, and posting period. It is a proposal only. A subsequent monthly release is separately prepared from the approved schedule and the then-current ledger.

### Review and act

The reviewer sees the invoice, source entry, schedule, policy, proposed adjustment, and projected balance. The application verifies current permissions and source versions. After explicit approval, the execution service creates the supported entry, records its external identity, reads it back, and checks that the expected fields were posted.

A timeout is an unknown execution state. Reconcile the external result before any retry; never create a second entry merely because the first response was lost.

### React to new evidence

A later credit note is received. The system identifies the schedule and dependent proposals that may change. It prepares a revised calculation and highlights the difference. Already posted work remains in history. Any corrective entry needs fresh accounting review; the system does not delete or reverse it automatically.

### Carry forward

The next close starts from the approved policy, remaining schedule, prior postings, and current source state. It does not rerun a transcript of the last conversation and assume it is still correct.

### The demonstration passes only when

The calculation is reproducible; a wrong entity, changed invoice, missing page, stale approval, or already-posted adjustment prevents unsafe execution; the actual posting is verified; and the reviewer can inspect only the changed work after new evidence arrives.

This reference tests the complete platform. Statement reconciliation and other work types reuse the same evidence, review, and execution foundations rather than becoming unrelated products.

<!-- pagebreak -->

## 10. Functional requirements: context and evidence

### CTX-01 · Company and period model — R0/R1

Ingest approved accounts, counterparties, relevant transactions, reports, and company settings through the connector’s declared capabilities. Preserve provider IDs, revisions, accounting basis, period, currency, and data completeness. Normalize them into a read model; do not create a second authoritative ledger.

**Acceptance:** a reviewer can trace every prepared amount to the source and data revision. A partial sync or missing entity type is visible and cannot silently produce an apparently complete close.

### EVD-01 · Document intake — R1

Accept secure uploads and controlled forwarded documents. Add selected-file cloud-drive access as its permission path is ready. Check file type and safety, preserve the original, and extract structured facts with their source locations. Use text extraction before image processing where possible; low-quality scans and missing pages require explicit handling.

Deduplicate document copies without deleting distinct invoices or revised versions. Record content hashes, source owner, entity, period, and retention policy. A repeated filename is neither a unique ID nor proof of identical content.

**Acceptance:** missing statement pages, inconsistent totals, unsupported currency, and contradictory invoice dates are surfaced rather than silently repaired by the model.

### EVD-02 · Entity-safe matching — R1

Prefer explicit transaction, invoice, document, and account relationships. Use bounded candidate matching to propose links when identifiers are absent. Matching scores do not become accounting truth. Ambiguous links block dependent proposals until reviewed.

**Acceptance:** two clients with identical vendor names, or two genuine equal-amount payments, remain distinct. No cross-entity record can enter an agent’s context or a client request.

### POL-01 · Approved procedures — R1/R2

A procedure defines eligible work, accounting basis, account mapping, calculation method, evidence requirements, period rules, tolerance/materiality settings, approval authority, and exclusions. Propose templates; allow firm defaults and explicit client overrides. Record the version and effective date.

**Acceptance:** changing a procedure shows affected work and requires approval. A reviewer edit suggests a future rule; it never mutates every client’s treatment automatically.

### Source-readiness rule

No upstream source should have an unexplained green “connected” indicator. Show latest successful sync, covered dates, required objects, document completeness, permission limitations, and the work these limitations prevent. “No exceptions detected” is not a health claim when required evidence is absent.

<!-- pagebreak -->

## 11. Functional requirements: reconciliation

### REC-01 · Independent-statement reconciliation — R1

Inputs are the relevant ledger detail, an independent bank/card statement, approved date boundaries, opening balances, and reconciliation policy. Prepare matches, unmatched items, outstanding transactions, timing differences, and a reproducible roll-forward.

Support exact and approved tolerance-based candidates, one-to-many and many-to-one groupings, split settlements, and transfer pairs. Preserve why a match was selected. Matching all transactions is not proof that the source population was complete.

**Acceptance:** statement totals and page continuity are checked; amount/date/sign conventions are explicit; opening and closing differences reconcile or remain unresolved. No balancing “plug” entry is manufactured.

### REC-02 · Investigate differences — R1/R2

Gather missing evidence, compare adjacent periods, inspect relevant operations, and propose a concise question or accounting treatment. An unexplained item is not automatically a bank error, duplicate, expense, or fraud.

**Acceptance:** a transfer recorded in two accounts is not labeled a duplicate solely from amount and date. Outstanding checks are not deleted because they have not cleared. Missing evidence produces a targeted blocker.

### REC-03 · Distinguish the workpaper from native reconciliation — R1 onward

Our workpaper can be prepared and approved independently of the ledger application’s native reconciliation flag. Record both statuses where available. Do not mark a native account reconciled without a documented, tested permissioned operation.

Current accessible Intuit references substantiate accounting entities, reports, and change detection; they do not establish all required native bank-feed or reconciliation-state capabilities. A connector spike must prove those operations before they are promised. [4,5]

Provide an export and a clear manual completion step when necessary. Do not use hidden browser automation or shared passwords to imitate an unsupported API.

### REC-04 · Settlement reconciliation — R3

Extend the model to processor settlements: charges, fees, refunds, disputes, and payouts. Use source-provided batch relationships where they exist. Stripe documents payout-level reconciliation for automatic payouts, with different treatment for manual or instant payouts; do not manufacture a universal transaction-to-payout relationship. [15]

**Shared completion rule:** prepared matching, reviewer approval, native ledger completion, and source-backed settlement are separate claims. The close package must say which were performed.

<!-- pagebreak -->

## 12. Functional requirements: schedules and exceptions

### SCH-01 · Straight-line prepaids — R1

From source terms and approved policy, generate the service-period schedule, compare it with booked amounts, and prepare supported adjustments. Handle date boundaries, partial periods, rounding residuals, prior entries, cancellations, and credits through explicit procedures.

**Acceptance:** cumulative scheduled amounts equal the allocable total under the approved method; prior postings are not duplicated; a changed term invalidates the affected calculation. No generic accrual treatment is applied to cash-basis clients.

### SCH-02 · Simple accruals — R2

Prepare accruals only where the firm has defined sufficient evidence, estimation method, reversals if applicable, and materiality. Examples may include a known service period with an approved estimate and documented basis. Missing an invoice alone does not justify inventing an expense.

**Acceptance:** estimate status is visible; the method and authorizing policy are included; later actuals link to the estimate and proposed correction. No automatic reversal is assumed for every entry.

### SCH-03 · Loan and recurring schedules — R3

Parse reviewed agreements and statements into principal, interest, fees, balances, and periods. Only supported interest conventions and payment structures run automatically. Variable rates, modifications, ambiguous fees, and specialized treatment require separate approved procedures.

**Acceptance:** roll-forward ties to evidence; principal is not treated as expense; a lender total mismatch remains explicit.

### EXC-01 · Supported exceptions — R1/R2

Investigate suspected duplicates, uncategorized items, unusual balances, missing documents, inconsistent coding, and clearing-account differences. Evidence can establish a fact; accounting treatment still depends on context and policy.

The product should complete the evidence gathering and prepare a proposed action, not stop at “anomaly found.” Use the appropriate native operation when correcting a subledger record. A journal entry is not a substitute for every bill, credit, payment, or vendor workflow.

**Acceptance:** genuine recurring transactions are not deleted; owner-related transactions remain questions when purpose is ambiguous; posted prior-period changes require the firm’s approval and permitted period. Every supported correction is included in the procedure registry and test corpus.

### Feature expansion gate

An additional accounting procedure ships with an accountable domain owner, defined inputs and exclusions, independently reviewed fixtures, supported connector operations, and reviewer UX. It does not become supported merely because an LLM can describe it.

<!-- pagebreak -->

## 13. Functional requirements: client follow-up and review

### COM-01 · Consolidated client requests — R1 drafts; R2 sending

Inspect permitted evidence before asking. Group related questions by client, contact, and period; remove duplicates; explain the exact missing fact or file. The firm approves initial messages, recipients, and any attachment. Delivery uses a scoped request identifier so replies return to the correct work.

**Acceptance:** one ambiguous inbox reply cannot update several clients. A reply from an unauthorized sender remains pending review. A thread containing bank credentials is redacted and restricted rather than forwarded to every reviewer.

### COM-02 · Resume from replies — R2

Extract candidate answers with source references, validate them against existing evidence, and continue dependent tasks. Contradictions remain visible. Store the original reply and approved interpretation separately.

**Acceptance:** no repeated request after sufficient evidence arrives; reminder policies are bounded; a client’s answer cannot override firm policy or approve posting. Stop or escalation behavior is explicit when requests remain unanswered.

### REV-01 · Source-linked review — R1

Every workpaper includes source evidence, relevant ledger versions, calculation, approved procedure, proposal, before/after effect, open questions, and review history. The accountant can change assumptions and rerun deterministically.

**Acceptance:** every critical extracted date and amount can be inspected in its source; a reviewer need not reconstruct arithmetic in an external sheet. Uncertain facts are not hidden behind a single confidence score.

### REV-02 · Approvals and sign-off — R2

Keep approval of a workpaper, authorization to post, authority to send a client message, and firm close sign-off separate. Approval is bound to a content hash, evidence revisions, client, period, target system, operations, actor, and expiry.

**Acceptance:** edits, evidence changes, account remapping, revoked access, or a different target invalidate the relevant approval. An agent cannot approve its own write. Role-based separation can require a different reviewer for specified risk levels.

### REV-03 · Changes after review — R2/R3

A new source revision identifies the dependent work, displays a meaningful diff, and requests renewed review. Unchanged independent items retain valid approval. New evidence after sign-off creates a proposed restatement or new work cycle under firm policy; it does not erase history.

**Acceptance:** the system states what changed and why review is needed. Re-review burden is measured, not assumed smaller.

<!-- pagebreak -->

## 14. Functional requirements: approved posting and close package

### ACT-01 · Execute approved changes — R2

The first production write is a selected, accountant-approved operation such as a supported prepaid adjustment. Verify source version, policy, account mapping, entity, date, authorization, and expected effects immediately before execution. Use the narrowest tested native transaction operation.

Intuit’s SDK documents JournalEntry and other entity operations, source version fields, and sparse updates. Exact current schemas, production access, and operation-specific behavior still require connector tests. Sparse updates are not permission to assume nested line arrays merge as intended. [5]

**Acceptance:** stale approvals are rejected; no transaction is duplicated on replay; a changed ledger record cannot be silently overwritten. Application-level serialization controls our writes, not every independent writer to the customer’s ledger.

### ACT-02 · Uncertain outcome and readback — R2

Persist the intended operation and execution attempt before the outbound call. Record external IDs and response provenance. Re-read the result and check fields and relevant supported balances. Where provider idempotency exists, test and use it; an arbitrary note or request tag alone is not an exactly-once guarantee.

If a response is lost, use supported lookup and reconciliation. Ambiguous results stay **execution unknown** and require resolution before retry. Compensating entries or reversals are separate approved accounting operations—not a generic rollback button.

### PKG-01 · Close binder — R1 export; R2 complete package

Assemble workpapers, schedules, source/report references, approved adjustments, posted receipts, unresolved items, manual steps, and sign-offs. Include entity, basis, period, procedure versions, data-cut time, and scope.

Export evidence and tables in ordinary formats. PDF workpapers, CSV tables, and a machine-readable manifest must remain useful outside the platform. The customer retains access to its work even if a connector is disconnected or the product is discontinued, subject to agreed retention and rights.

### PKG-02 · Next-period preparation — R2/R3

Carry forward valid schedules, approved client rules, prior unresolved items, and source links. Refresh current facts and evidence before using them. Reopen only dependent work. Never copy last month’s answers as proof that this month is correct.

**Completion:** the system can prepare and verify supported work; the firm owns final sign-off. A balanced trial balance, zero visible alerts, or completed agent jobs cannot independently authorize that sign-off.

<!-- pagebreak -->

## 15. Agent-first execution and learning

### What the agents actually do

A durable coordinator receives a client, period, supported scope, and authority. It plans work, delegates evidence gathering and preparation, monitors dependencies, and resumes after new information. Specialized capabilities may interpret documents, reconcile records, draft requests, or prepare schedules. Customers do not manage a cast of agent personas.

**The system chooses useful next steps. Deterministic services and external permissions constrain what can happen.**

### Separation of responsibility

| Responsibility | Implementation |
| --- | --- |
| Read meaning, select evidence, draft questions, propose treatment | Model-driven reasoning with source references |
| Amounts, dates, allocation, matching constraints, roll-forwards | Typed calculation and verification services |
| Scope, credentials, spending limits, approvals, posting rules | Application policy enforced outside the model |
| Accounting judgment and final sign-off | Authorized firm personnel |

Use schema-valid outputs, but never treat valid JSON as proof of correct accounting. Independent checks and human review target the substantive conclusion.

### Durable work, not transient chat

Each job carries a goal, client/period scope, evidence references, allowed tools, budget, checkpoints, and cancel state. A lost browser session or process restart must not lose work or repeat a posting. The interface exposes completed operations, remaining blockers, and available next actions rather than hidden model reasoning.

### Learning with approval

Reviewer edits become proposed improvements to a client procedure, matching rule, or preparation template. Show affected clients and examples; run regression tests before activation. Reuse approved preferences through tenant-scoped retrieval and configuration, not undisclosed model training.

Xero explicitly prohibits using API data to train AI/ML models. Provider contracts and customer permissions must govern model transmission and any learning mechanism. [6]

### Security against agent misuse

Documents, emails, ledger memos, repository content, and MCP output are untrusted data. Embedded text cannot change approval policy, request secrets, or choose an unauthorized destination. Models do not receive raw OAuth refresh tokens or unrestricted database credentials. External tool catalogs are filtered to the task, not exposed wholesale.

### End state

The long-term product becomes progressively more capable at preparing routine work and involving people only where judgment or authority is required. This is not a plan to let the model silently assume professional responsibility.

<!-- pagebreak -->

## 16. Release sequence and feature dependencies

| Release | Customer-visible capability | Engineering prerequisites | Readiness evidence |
| --- | --- | --- | --- |
| R0 · Reference foundation | Review prototype and labeled example close | Accounting lead; entity/period model; evidence and procedure registry; API spike | Reference calculation, source linkage, scope and access gaps documented |
| R1 · Shadow preparation | Source-linked reconciliations and prepaid schedules; draft questions; review and export | QBO reads or documented exports; secure upload; typed math; read-only enforcement | Real authorized files; held-out fixtures; reviewer can inspect and correct work |
| R2 · Commercial close | Approved client requests, selected posting, readback, binder, portfolio work, API/MCP | Revocation-safe approvals; replay-safe execution; supported live API access; incident and data controls | Complete preparation-to-verified-action path on repeated closes |
| R3 · Continuous preparation | Delta review, deeper dependency refresh, close rehearsal, richer schedules, Xero and Stripe support | Reliable source revisions; procedure tests; connector approval; measurement of review savings | New evidence changes the right work without stale approvals or duplicate effects |
| R4 · Product family | Daily Books, Year-End Handoff, Cash Operations | Shared core plus separate domain procedures, permissions, and buyer evidence | Adjacent work is completed without degrading the core close experience |

### Why this order

R1 establishes whether we can prepare trustworthy work and make review easier without production-write risk. R2 completes the paid recurring job rather than stopping at an anomaly report. R3 builds the compounding advantages: less repeat setup and review, more continuous preparation, and broader ledger coverage.

Prepaids and statement workpapers share evidence, decimal calculations, review, and source reconciliation. They are parallel reference workflows—not a demand that the company remain a prepaid checker. More accounting treatments can be developed concurrently once their domain owner and evidence requirements are defined.

### What should not enter the critical path

A marketplace listing, full-mailbox access, a provider-preview MCP server, every ledger connector, or a perfect automatic understanding of historical books must not prevent secure uploaded-file preparation. Fallbacks must state what they cannot verify.

### First commercial release boundary

It must perform supported work, execute appropriate tests, request real approvals, perform at least one supported live accounting operation, and verify the result. An attractive dashboard or generated journal-entry draft alone does not satisfy the commercial product definition.

<!-- pagebreak -->

## 17. Execution plan and ownership

**Planning assumption:** three parallel engineering streams, one product designer, an engaged accounting product lead, and a founder responsible for distribution and partnerships. The windows below are planning estimates, not vendor-approval promises.

| Window | Platform and accounting | Product and customer work |
| --- | --- | --- |
| Weeks 1–2 | Verify QBO capabilities/scopes; create evidence and period model; build decimal services and reference workpapers | Design Review first; appoint domain owner; secure authorized examples and pilot data terms |
| Weeks 3–5 | Read sync, uploads, reconciliation candidates, prepaids, procedure templates, job recovery | Deliver Work/Client close/Review; run historical shadow preparation; capture total review effort |
| Weeks 6–8 | Proposed changes, stale-source checks, approval receipts, tested selected writes, readback | Approved client requests; controlled live preparation; export useful close packages |
| Weeks 9–12 | Portfolio operation, dependency invalidation, API/MCP parity, observability, cost controls | Repeated paid usage; second-cycle review; first defensible customer evidence |
| Weeks 13–16 | R3 work in parallel where ready: Xero, settlement, deeper change-aware review | Refine packaging, partner distribution, and documented implementation patterns |

External access review, security requirements, and actual data complexity may move dates. When access is delayed, continue with consented exports and clear mode labels; do not masquerade a shadow import as a production integration.

### Accountable owners

**Product/founder:** customer job, prioritization, permissions strategy, onboarding, sales, and pilot evidence.

**Accounting lead:** supported procedures, policy boundaries, reviewer behavior, exception judgments, and material-error response. A second practitioner reviews evaluation labels and contentious cases.

**Platform lead:** tenant isolation, connectors, source revisions, durability, execution receipts, and operational controls.

**Agent/accounting engineering lead:** evidence preparation, deterministic calculations, bounded reasoning, regression corpus, and procedural correctness.

**Experience lead:** review efficiency, action clarity, client communication, accessible interfaces, and coherent API/MCP behavior.

### Immediate deliverables

A verified connector capability map; the golden prepaid fixture and independent-statement fixture; a usable Review screen; authorized pilot files and agreements; and a baseline of the firm’s existing preparation/review process. Product, engineering, and customer work start together.

Quality gates enable capabilities; they do not become an excuse to postpone all commercial contact or to require months of interviews before building.

<!-- pagebreak -->

## 18. Shadow pilots, actual use, and fundraising evidence

### The offer

> **Bring a few ordinary client files into the next close cycle. Keep your existing ledger. We prepare the supported work, your accountant reviews it, and we measure the total effort.**

Use a short product demonstration and real work instead of hypothetical “would you use this?” interviews. Do not tell firms “we are not selling anything” if the purpose is eventual product adoption. Be clear about the evaluation, data handling, scope, cost, and next decision.

### Three operating modes

**Historical shadow:** no production writes or client messages. Use authorized records and an as-of data manifest. If the ledger already contains final adjustments, either reconstruct a valid earlier snapshot or explicitly limit the evaluation; do not let the agent see the answer and claim it saved preparation time.

**Live preparation:** the product reads permitted current data and prepares work beside the existing process. Reviewer comparison includes setup and correction effort. Secure client requests require separate authority.

**Approved execution:** enable selected operations only after connector and accounting tests pass. The firm can revoke write authority while retaining preparation and exports.

### Evidence to collect

Start with a proposed cohort of five independent firms and roughly three eligible entities per firm. Include ordinary clean files, incomplete evidence, ambiguous transactions, and exceptions—not only dramatic failures.

Record work accepted, substantive edits, unresolved items, active reviewer minutes, total firm effort, assistance from our team, and repeated use over at least two close cycles where possible. A file processed is not a customer retained; a favorable quote is not a purchase.

### Investor readiness

The useful evidence package is a working end-to-end demonstration, consented real-work results, source-linked examples, measured reviewer effort, paid or committed pilot terms where they exist, and a clear account of remaining risk and service labor. Do not fabricate a waiting list, customer quote, usage result, or anticipated fundraising outcome.

Gemini’s percentage odds and automatic term-sheet conclusions are not supported by supplied evidence. The plan does not require delaying all investor contact, nor does a prototype justify claiming market fit. Build and engage customers in parallel; state exactly what has been demonstrated.

**Shadow mode reduces execution risk, not confidentiality risk.** Obtain the right authority, minimize data, enforce retention, and describe the evaluation as preparation—not an audit or assurance service.

<!-- pagebreak -->

## 19. Success metrics and commercial operation

### North star

**Total human minutes per supported client close**, including preparation, review, corrections, and recurring configuration. Separate active labor from waiting on the client and from system processing time.

### Proposed operating targets

| Measure | Target or decision rule |
| --- | --- |
| First useful workpaper | Median ≤30 minutes after required evidence/access; publish prerequisites and assistance |
| Human effort | Aim for ≥40% reduction on the declared supported work over comparable repeated closes |
| Reviewer burden | Track substantive edits, rejected proposals, and re-review after source changes |
| Source completeness | Required evidence is accounted for or explicitly missing; no hidden green status |
| Repeat use | Several independent firms complete the next cycle and expand supported work when useful |
| Payment | Aim for at least three of the first five pilots choosing ongoing paid use; investigate reasons either way |
| Execution safety | No unauthorized, cross-client, duplicate-on-replay, or stale-approved actions in acceptance tests |
| Unit economics | Track API, models, storage, support, and internal accounting help per active entity/month |

These are proposed management thresholds, not guarantees or formal proof of product-market fit. A high acceptance rate never excuses a material unsupported entry. Acceptance-test success does not imply production incidents are impossible.

### Packaging hypothesis

Sell one preparation workspace, priced by active client entities and an explicit workload band. Include evidence collection, supported preparation, review, controlled actions, and exports. Do not bill by errors found, agent personas, or tokens. Separately scope historical cleanup beyond the agreed starting condition.

No unlimited unpaid custom accounting work hidden behind a software pilot. Measure our intervention time so favorable customer savings do not conceal equivalent labor on our side.

### Distribution work

Founder-led demonstrations, product-based pilots, accounting-system consultants, and bookkeeping educators are initial routes. Platform directories become additional channels when review and eligibility requirements are satisfied, not an assumption that customers arrive automatically.

### Risks that change priorities

If review cost exceeds preparation saved, improve evidence and task scope before adding automation. If most work needs bespoke procedures, improve template reuse and focus client eligibility. If users trust preparation but not posting, retain useful read-only modes while fixing the action controls. These are product decisions, not automatic reasons to shrink into a generic error checker.

<!-- pagebreak -->

## 20. Ambitious expansion, one coherent accounting system

### Close Preparation — starting product

Own the complete recurring preparation loop with evidence, review, selected approved actions, and a portable binder. The firm keeps its ledger and professional judgment.

### Daily Books — prepare continuously

Run approved evidence collection, schedule maintenance, matching, and exception preparation during the month. Material new facts trigger work instead of waiting until close week. Human authority remains defined per operation. The close becomes a review of maintained work rather than a fresh reconstruction.

### Year-End Handoff — preserve the work already done

Produce organized supporting schedules, selected roll-forwards, reconciled balances, adjustment history, and unresolved questions for the firm’s tax and audit processes. Do not claim to issue an audit opinion or file a tax return merely because the package is exportable.

### Cash Operations — extend carefully

Prepare receivables and payables exceptions, settlement reconciliation, supporting records, and approved operational follow-up. Payment execution, bank changes, and collection communications each require separate policies, legal review where applicable, and controls. Start with preparation rather than money movement.

### The longer-term advantage

The platform maintains the relationship among client facts, firm methods, evidence, proposed work, actual postings, and review history. A source change can identify exactly what needs redoing. A reviewer’s approved procedure can improve the next period without exposing another client’s information.

This can become a highly capable accounting preparation team—not just a collection of automations. It should remain portable and understandable: customers can inspect calculations, export workpapers, and revoke authority.

### Rules that protect the brand

**Prepared work, not mysterious scores.**  
**Source evidence, not persuasive accounting stories.**  
**Specific authority, not blanket autonomy.**  
**Less work next month, not just a fast demo this month.**

The brand promise stays the same as the product expands:

> **Your clients’ books, prepared for your review.**

<!-- pagebreak -->

## Appendix A. Ledger integrations and capability gates

### QuickBooks Online — first native connector

| Capability | Implementation direction | Required evidence before enablement |
| --- | --- | --- |
| Authorization | OAuth per company/realm; server-side secret storage and rotation | Correct realm binding, revocation, refresh, production enrollment and current scope review |
| Read model | Approved accounts, transaction types, counterparties, and relevant reports | Current API schema/version, pagination, dates, currency, basis, completeness and rate-limit tests |
| Updates | Webhook-supported refresh plus bounded change polling and periodic reconciliation | Missing/late events and coverage windows tested; no assumption that webhooks are a full audit log |
| Reporting | Record report parameters and accounting basis explicitly | Reconcile against the same provider report cut; report output is not an atomic global ledger snapshot |
| Selected writes | Test native operations, including an appropriate approved JournalEntry | Correct account/period, SyncToken behavior where applicable, partial update semantics, duplicate protection and readback |
| Bank reconciliation | Prepare statement workpapers; native state separate | Native completion and bank-feed capabilities unverified until a successful provider-specific test |

Official Intuit references document company-scoped access, entity operations, sparse updates, reports, and change detection. These support the integration direction, not a claim that our connector or every requested action already works. The accounting scope may be broader than our read-only product mode; enforce application restrictions and disclose that difference. [4,5]

Do not use the Intuit CLI as a production Go backend. It is an official integration reference and useful development aid. No verified first-party hosted QBO MCP is assumed in this design.

### Xero — planned second ledger

Use native APIs for scheduled sync and approved execution. Preserve Xero-specific account, invoice, manual-journal, report, and tenant semantics rather than forcing them into QBO assumptions.

Xero’s complete Journals access is tied to Advanced tier and additional approval; this is different from ManualJournals. Its published developer policies also prohibit using API data for AI/ML training. Confirm the current access tier, granular scopes, and terms before promising coverage or enabling models on that data. [6]

### Capability status in the product

Every connector operation is **documented**, **sandbox tested**, **production approved**, or **enabled for this client**. These are not synonyms. Record its owner, evidence, test date, permission, fallback, and known limitation. “Connected” never means all operations are available.

<!-- pagebreak -->

## Appendix B. Evidence, communication, and operational connectors

| System | Intended use | Readiness and limitation |
| --- | --- | --- |
| Secure upload / scoped inbound email | Statements, invoices, explanations, pilot exports | R1 baseline; quarantine files, bind recipient to entity, preserve originals and revisions |
| Google Drive API | User-selected documents and approved exports | Prefer `drive.file` with a picker where suitable. It does not imply automatic access to all folder descendants. Broader access needs the corresponding review. [9] |
| Gmail API | Authorized evidence search, reply matching, approved sending | Full message-read scopes are restricted. Stored/transmitted restricted data can trigger security assessment. Keep this off the launch critical path; `gmail.send` is a distinct scope. [8] |
| Microsoft Graph | Approved Outlook mail and OneDrive/SharePoint evidence | Choose delegated or narrowly governed application permissions. Organizational policy and administrator approval can limit access. Read authority does not imply send authority. [11] |
| Stripe API | Processor balances, fees, refunds, disputes, payout records | R3 settlement workflow; preserve source currency/units and payout semantics. A processor feed is not the entire ledger. [14,15] |
| Plaid Transactions | Optional permitted bank-transaction evidence | Later; consent, production eligibility, data revisions and completeness must be tested. Not a substitute for every bank statement or native QBO reconciliation. [17] |
| Slack API | Optional staff notifications and approved deep links | Minimal `chat.write`-based notification path; financial details stay behind authorization. An interactive click rechecks the actor’s permission. [12] |

### Native APIs versus MCP

Use native APIs for high-volume, scheduled, completeness-sensitive ingestion and verified writes. MCP can expose bounded investigative capabilities and interactive access. Neither route grants accounting meaning, completeness, or permission automatically.

Do not install third-party connector code with unrestricted production secrets because its description claims to be safe. Pin versions, review scopes and licenses, and test network and output boundaries. An embedded integration vendor can reduce OAuth plumbing; it does not remove accounting-semantic work or our data obligations.

### Source and privacy requirements

For every integration, declare available identifiers, allowed entities, date coverage, update/deletion semantics, known freshness, rate limits, scopes, retention terms, and permitted model use. Credential failure suspends dependent claims and actions, not unrelated clients.

Source documents and messages may contain unrelated clients or sensitive personal information. Minimize retrieval, require explicit entity routing, and avoid full-mailbox indexing by default. Export and pilot modes are usable fallbacks—not permission to bypass access restrictions.

<!-- pagebreak -->

## Appendix C. MCP and public API contracts

### Third-party MCP routes

| Provider | Verified public surface | Product decision |
| --- | --- | --- |
| Xero | Official `XeroAPI/xero-mcp-server` reference implementation includes read and write tools | Optional isolated integration/reference. Expose only approved capabilities; do not grant payment or journal writes by default. Native API remains the bulk path. [7] |
| Stripe | Official remote server at `https://mcp.stripe.com` | Optional approved read tools with restricted permissions. Not a blanket accounting or payment authority. [14] |
| Google Drive | Official remote MCP in Developer Preview at `https://drivemcp.googleapis.com/mcp/v1` | Not a launch dependency. Eligibility and permission review required; native selected-file API remains primary. [10] |
| QuickBooks Online | Official API and CLI references verified; hosted first-party MCP not established here | Build a native connector and expose our own typed capabilities. Do not advertise an unverified official MCP. [4,5] |

### Our MCP server and REST API

Use the same capability and authorization layer for React, API, CLI, and MCP. Pin a supported MCP protocol version and negotiate optional capabilities; the current specification is a transport/tool contract, not a safety guarantee. [13]

| Capability | Proposed API | MCP tool |
| --- | --- | --- |
| Create or inspect close | `POST /v1/closes`; `GET /v1/closes/{id}` | `start_close`, `get_close` |
| Prepare work | `POST /v1/workpapers:prepare` | `prepare_workpaper` |
| Explain / inspect sources | `GET /v1/workpapers/{id}/evidence` | `explain_workpaper` |
| Propose treatment | `POST /v1/proposals:preview` | `preview_adjustment` |
| Prepare client request | `POST /v1/client-requests:draft` | `draft_client_request` |
| Request approval | `POST /v1/approval-requests` | `request_approval` |
| Execute approved operation | `POST /v1/actions` | `execute_approved_action` |
| Refresh changed work | `POST /v1/workpapers/{id}:refresh` | `refresh_workpaper` |
| Export package | `POST /v1/closes/{id}:export` | `export_close_package` |

All long operations return a durable job ID, scoped status, cancellation, and resumable progress. Approval requests are not approvals. The user’s authenticated review creates the grant; an agent cannot create its own authority. `execute_approved_action` accepts an approved plan reference, not arbitrary accounting instructions.

Version schemas, paginate reads, constrain filters, accept idempotency keys, and return explicit errors such as `SOURCE_STALE`, `POLICY_REQUIRED`, `SCOPE_DENIED`, `APPROVAL_EXPIRED`, and `EXECUTION_UNKNOWN`.

<!-- pagebreak -->

## Appendix D. Shared data and system architecture

### Core data model

| Object | Purpose |
| --- | --- |
| Firm / entity / period | Client identity, ledger realm, currency, basis, scope and deadlines |
| Connection / source snapshot | Permission, provider identity, sync watermark, revision and coverage |
| Evidence / extracted fact | Original artifact, source location, proposed interpretation and review status |
| Procedure / client override | Versioned accounting method, conditions, accounts, tolerances and authority |
| Workpaper / dependency | Inputs, calculation, result, affected records and freshness |
| Proposal / approval | Exact intended change, content hash, authorized actor, scope and expiry |
| Action attempt / receipt | Execution status, provider identity, response, readback and unresolved result |
| Request / reply | Client communication tied to specific missing evidence |
| Close package / sign-off | Export manifest, open items, approved scope and firm decision |

Amounts use exact decimal representations with explicit currency precision. Dates include accounting period and applicable time-zone semantics. Source observation time, source event time, and version are recorded separately. No generic “AI confidence” replaces evidence completeness or review state.

### Deployment

```text
React workspace / optional Slack / API / MCP
                    |
             Go capability API
                    |
      Durable coordinator + typed tools
       /             |               \
Postgres         R2 evidence       Model gateway
product state    and exports       approved models
       \             |               /
          Go background worker roles
                    |
       Restricted approved-action executor
                    |
        QuickBooks / later Xero and APIs

Cloudflare Workers: ingress and binding adapters
Cloudflare Queues: durable deliveries to workers
Cloudflare Workflows: retries, waiting and resumable work
```

One Go repository supports independently scaled API, connector, preparation, and execution roles. Small TypeScript Workers handle Cloudflare bindings; no Node.js business backend is required. Postgres remains product state, not a replacement general ledger. Models never directly receive posting credentials.

Cloudflare’s documented Containers integration reaches Worker bindings through outbound handlers; do not assume native Go binding libraries. Hyperdrive is optional for specific Worker-side reads, not a mandatory remote-SQL bridge. Cache-disabled reads are required for current approvals and accounting decisions because Hyperdrive does not invalidate cached reads on writes. [18,21]

Keep the Go image portable. Verify product-specific location, networking, and security requirements before storing real accounting evidence. Credits lower cost; they do not establish compliance or infinite capacity.

<!-- pagebreak -->

## Appendix E. Durability, security, and control design

### Reliable execution

Use tenant-scoped inbox/outbox records and stable operation identities. Queue consumers acknowledge only after durable processing or job handoff. Cloudflare Queues is at-least-once and does not guarantee message order; projections must handle duplicates, revisions, and late events. Workflows provides resumable orchestration, not exactly-once external effects. [19,20]

Persist large artifacts in R2 and reference them from jobs. Database, object store, and queue operations are not one transaction; reconcile incomplete publications. A job timeout or container restart does not prove an upstream posting failed.

Bound per-firm concurrency, API budgets, token costs, document size, scan volume, and retries. Separate live posting from backfill and document extraction. Start with indexed Postgres; add tenant-partitioned Parquet/DuckDB for measured historical workloads. Redis, Kafka, ClickHouse, and Kubernetes are not launch requirements.

### The approval envelope

An approval binds **firm, entity, period, operation list, accounts, amounts, source revisions, procedure version, proposal hash, reviewer, target, and expiry**. Immediately before execution, verify active authority and preconditions. A changed source or plan requires a new approval. Never hold a database transaction open while a person reviews.

Record before-state, intended change, operation attempt, response, and authoritative readback. If other users changed unrelated balances, do not claim a global balance movement was entirely caused by our operation. Record snapshots and relevant transaction lineage.

### Data controls

MFA, role-based access, per-entity authorization, encryption, managed secret rotation, redaction, audit trails, backups, incident response, export, revocation, and deletion are production requirements. Sensitive data is omitted from notification previews and model logs by default. No cross-firm context or training reuse without an explicit permissible basis.

Production data and test execution are separated. Untrusted document text and MCP outputs cannot authorize actions. Default-deny outbound tool destinations and schemas; never execute model-generated code beside production credentials.

Retention follows the firm’s agreement and applicable obligations; do not impose an invented universal duration. Deletion must cover derived artifacts and job context under our control, with documented backup handling. The FTC’s Safeguards guidance is relevant to certain financial-service businesses and their service providers; legal review must establish applicability rather than assuming every firm or vendor has identical obligations. [16]

**Certification language is exact:** planned controls, completed assessments, and independently obtained certifications must never be conflated.

<!-- pagebreak -->

## Appendix F. Evaluation and mandatory acceptance tests

### Evaluation method

Use consented or synthetic, accountant-reviewed cases. Maintain development and held-out sets split by client and period. Record what evidence was available at task time. Do not leak final adjustments, reviewer answers, or later documents into a historical preparation comparison.

A domain lead defines supported procedures; a second practitioner reviews contested labels. Measure factual extraction, calculation, proposed treatment, uncertainty handling, review effort, and execution separately. Calibrate routing thresholds against real errors; do not let model self-ratings serve as the evaluation.

| Test | Required behavior |
| --- | --- |
| Two genuine recurring payments have the same amount | Do not declare a duplicate without supporting evidence |
| Identical invoice appears with a new filename | Preserve one economic item and document-copy provenance |
| Statement page is missing or totals do not tie | Mark evidence incomplete; do not fabricate a balancing item |
| Payment splits across multiple invoices | Support reviewed grouping; do not assume one-to-one matching |
| Transfer appears in both bank accounts | Keep transfer relationship; do not invent expense or duplicate deletion |
| Owner-related payment has ambiguous purpose | Ask a specific question; do not silently choose tax/accounting treatment |
| Cash-basis client encounters prepaid template | Block unsupported treatment pending an approved method |
| Invoice service dates or credit change | Recalculate dependent work; invalidate stale review only where affected |
| Transaction is already posted or period is locked | Prevent duplicate or unauthorized adjustment |
| Source changes after approval | Reject stale execution and show a useful diff |
| API times out after a successful create | Reconcile external result before retry; preserve unknown if ambiguous |
| Queue redelivers or worker restarts | Resume durably without repeating an external effect blindly |
| Client email contains malicious instructions | Treat as data; do not change policy, recipient, or credentials |
| Same IDs/names occur in different clients | Deny cross-entity evidence or posting |
| Bank feed and statement disagree | Show scope and source discrepancy; neither is blindly authoritative for every fact |
| An approved recurring method becomes inapplicable | Stop that procedure and request renewed review |

### Full-cycle release demonstration

Run the reference prepaid and statement workflows from intake through review, approved posting where supported, readback, a later source change, and next-period carry-forward. Include a candidate treatment the system must reject.

Zero critical failures in this acceptance suite is a release condition, not a claim of zero production risk. Every production material error requires containment, accounting review, affected-scope assessment, and a documented recovery path.

<!-- pagebreak -->

## Appendix G. Decisions, open gates, and changes from v1

### Decisions preserved from the original plan

The mission, firm-centric buyer, supported single-entity scope, QBO-first strategy, exact arithmetic, approval-based write path, independent evidence, React/Go/Postgres/Cloudflare direction, repeated-close evaluation, and the Close Preparation / Daily Books / Year-End Handoff / Cash Operations family remain intact. [U1]

### Substantive refinements

| Earlier emphasis or gap | v2 decision |
| --- | --- |
| Broad preparation capabilities without detailed interaction contracts | Added requirement IDs, inputs, acceptance behavior, state boundaries, and release dependencies |
| Source-linked workpapers | Made dependency tracking and change-focused review a central product mechanism |
| Client request drafts | Defined secure request delivery, entity routing, validated reply handling, and work resumption |
| General API/MCP ambition | Added a provider capability register, official versus preview MCP status, and typed tool/API contracts |
| Selected posting as a future step | Specified approval envelope, uncertain execution, stale-source handling, and readback |
| Generic 12-week sequence | Preserved parallel development, separated readiness gates and external permission lead times, added R3 ambitions |
| Gemini’s MVP/fundraising claims | Kept real-data shadow engagement; rejected unsupported probabilities, zero-risk language, and invented demand |
| A dashboard described mostly by exclusions | Specified Work, Client close, Review, actionable examples, and measurable interaction quality |

### Decisions that require implementation evidence

**Accounting lead:** sign off initial procedures, materiality boundaries, and supported operations.  
**Connector lead:** confirm current QBO production scopes, reports, source coverage, native reconciliation limits, write semantics, and access process.  
**Security owner:** approve data route, customer authority, model use, retention, incident process, and privileged posting design.  
**Experience lead:** demonstrate that one reviewer can complete the work without reconstructing it elsewhere.  
**Founder:** obtain actual usage, permissions, baseline effort, and recurring commercial feedback.

### Product commitment

Build the ambitious preparation system. Do not downgrade it to a read-only anomaly checker because checks are easier to ship. Equally, do not use ambition to postpone source correctness, professional review, or the user’s actual work.

**The product earns trust when it performs useful work, makes its evidence easy to inspect, and remains controlled when it cannot finish.**

<!-- pagebreak -->

## Appendix H. Sources and verification notes

**Source policy.** Public references were reviewed on September 21, 2026. They establish documented or marketed capabilities, not independent comparative performance or our connector readiness. The detailed feature designs, roadmap, targets, and examples are proposals. Provider terms and API behavior must be checked again during implementation.

**[U1] Prior product source.** *AI Close Preparation Team for Accounting Firms — Product and action plan v1.0*, September 20, 2026; supplied as `accounting_product_and_action_plan_v1.md`. Basis for preserved mission, scope, workflow, architecture, and operating targets.

**[U2] Founder-supplied Gemini feedback.** Quoted in the request for this revision. Basis for addressing real-data shadow engagement and fundraising narrative. Its probability estimates, assumed response rates, and guaranteed financing conclusions are not independent evidence.

### Product and ledger references

**[1] Basis — Small Firms.** Preparation, reconciliations, source-linked work and firm methods.  
https://www.getbasis.ai/solutions/small-firms

**[2] Double — Scale.** Transactional accounting, schedules, reconciliation and close workflows.  
https://doublehq.com/scale/

**[3] Xenett.** Review checks and accounting workflow offering.  
https://www.xenett.com/

**[4] Intuit — official QuickBooks CLI.** Company-scoped OAuth, entity access and developer tooling. This is not an official hosted MCP commitment.  
https://github.com/intuit/intuit-cli-for-quickbooks

**[5] Intuit — QuickBooks V3 SDK quickstart.** Entity operations, sparse/full updates, SyncToken examples, reporting and change data capture. Current production behavior and required operations need separate tests.  
https://intuit.github.io/QuickBooks-V3-PHP-SDK/quickstart.html

**[6] Xero — Developer pricing and policy.** Access tiers, complete Journals requirements and API-data AI/ML training restriction.  
https://developer.xero.com/pricing

**[7] Xero — official MCP reference server.** Read and write capability surface; grants must be restricted.  
https://github.com/XeroAPI/xero-mcp-server

<!-- pagebreak -->

## Appendix H. Sources — continued

### Integration and protocol references

**[8] Google — Gmail scopes.** Read, send, restricted-scope verification and data-handling review.  
https://developers.google.com/workspace/gmail/api/auth/scopes

**[9] Google — Drive API scopes.** Selected-file access and broader permission implications.  
https://developers.google.com/workspace/drive/api/guides/api-specific-auth

**[10] Google — Drive MCP.** Developer-preview server, eligibility, and OAuth configuration.  
https://developers.google.com/workspace/drive/api/guides/configure-mcp-server

**[11] Microsoft — Graph permission reference.** Delegated/application distinctions and separate mail permissions.  
https://learn.microsoft.com/en-us/graph/permissions-reference

**[12] Slack — chat.write.** App notification permission reference.  
https://docs.slack.dev/reference/scopes/chat.write/

**[13] Model Context Protocol — specification.** Pin and negotiate actual protocol support; tool descriptions are not permissions.  
https://modelcontextprotocol.io/specification/latest

**[14] Stripe — official MCP.** Remote server and restricted access options.  
https://docs.stripe.com/mcp

**[15] Stripe — payout reconciliation.** Automatic payout grouping and manual/instant payout limitations.  
https://docs.stripe.com/reports/payout-reconciliation

**[16] FTC — Safeguards Rule business guidance.** Applicability, information safeguards and service-provider oversight; not a finding that every customer has identical obligations.  
https://www.ftc.gov/business-guidance/resources/ftc-safeguards-rule-what-your-business-needs-know

**[17] Plaid — Transactions API.** Candidate transaction evidence source; production and reconciliation behavior must be tested.  
https://plaid.com/docs/api/products/transactions/

<!-- pagebreak -->

## Appendix H. Sources — continued

### Architecture references

**[18] Cloudflare — Containers and Worker bindings.** Outbound-handler boundary for accessing Worker services from containers.  
https://developers.cloudflare.com/containers/configuration/workers-connections/

**[19] Cloudflare — Queues.** At-least-once delivery, duplicate handling, and non-guaranteed ordering.  
https://developers.cloudflare.com/queues/reference/delivery-guarantees/  
https://developers.cloudflare.com/queues/reference/how-queues-works/

**[20] Cloudflare — Workflows.** Durable execution, waiting and recovery primitives.  
https://developers.cloudflare.com/workflows/

**[21] Cloudflare — Hyperdrive FAQ.** Query caching and absence of write-triggered cache invalidation.  
https://developers.cloudflare.com/hyperdrive/reference/faq/

### How these sources should be used during the build

A documented feature enters the connector register as **documented**, not “implemented.” Add the exact request/response fixture, required scope, provider/version, test environment, failure modes, and approval status before enabling it for a client.

A marketed competitor feature establishes overlap, not equivalence. Compare completed work with the same customer evidence and measure preparation, review, corrections, and implementation labor. Do not withhold data from an incumbent to make our benchmark look better.

A product target is not an observed result. Keep actual pilot evidence in a separate, dated record: participating firm, authorized work, supported scope, baseline, measured result, open issues, assistance, and permission to publish. Fundraising claims must use that record rather than the roadmap.

### The standard for the next revision

Update this PRD when real accounting evidence, tested APIs, product usage, or controls change a decision. Preserve the mission and shared architecture. Avoid adding speculative scope merely to strengthen a pitch.

> **Give the firm more prepared work with less effort—and make every consequential step clear.**
