# Agent-first product completion plan

Status: build specification, not a release certification. Baseline: `cec0068` on main, reviewed October 2, 2026. Audience: product, design, engineering, accounting and founder.

## Outcome and scope

Deliver a complete recurring accounting service before a design partner operates the product: take over an existing client's books, keep ordinary work moving, prepare and review the close, verify approved corrections, publish useful management accounts, maintain cash decisions, and repeat next month. Partners improve fit after receiving value; they are not the test team for missing core workflows.

Accounting firms are the first market. The same facts and workflows serve a company's CEO/CFO and accounts team; an in-house company uses the existing organization boundary with one client, not a second tenant model. The initial proposed acceptance cohort is US, single-entity, USD service businesses on QuickBooks Online. Validate each client's actual accounts, transaction families and deliverables before enrollment. A scope that excludes ordinary messy work is insufficient. Xero code remains reusable, but equivalent commercial coverage requires its own provider acceptance.

This plan consolidates the October 2 technical-readiness and market/persona working reviews from this task into one implementation document. It extends the [initial PRD](product-requirements.md) and [delivery backlog](implementation-backlog.md). On launch scope, this document governs over narrower recommendations in those earlier working reviews and the discovery/shadow-access guidance in [maturity](maturity.md). Budget comparison, maintained cash forecasting and collection drafting move from deferred possibilities to required capabilities. A close-only demo, read-only shadow workflow or thin CEO dashboard is insufficient for partner-operated access. Internal milestones below are not partial launch products.

**Product promise to prove:** the agent does the preparation and coordination; people receive clear decisions; the business receives current, defensible financial information. Every amount is traceable, every consequential action is authorized, and incomplete work stays visible.

## What to preserve and connect

These foundations already exist in code. Presence is not proof of live activation or accountant acceptance; use [validation](validation.md) and [deployment](deployment.md) for evidence.

| Existing foundation | Extend it into | Reference |
| --- | --- | --- |
| Tray tokens, shared shell/components, agent composer, shared AskAgent entry and assistant panel; desktop/mobile layout checks | One continuous experience across preparation, review and business decisions; no second design system or parallel chat product | [Design system](design-system.md), [AskAgent](../apps/frontend/src/features/dashboard/AskAgent.tsx) |
| Durable close goals, intake/preparation jobs, checkpoints, recurring handover and bounded investigations | Automatically advance every supported recurring job; connect investigation results to a proposed draft or precise question | [Coordinator](close-coordinator.md), [Work inbox](work-inbox.md), [investigations](../services/backend/internal/transport/written_answers.go) |
| QBO/Xero captures, transaction indexes, account coverage, daily exceptions and evidence | Complete takeover, normal transaction resolution, account coverage and freshness | [Backlog](implementation-backlog.md), [QBO](quickbooks-onboarding.md), [Xero](xero-onboarding.md) |
| Exact calculations, approved procedures, versioned workpapers, review, change invalidation and carry-forward | Broad enough recurring preparation and correction handling for the admitted clients | [Methods](accounting-methods.md), [review](workpaper-review.md), [carry-forward](carry-forward.md) |
| Consolidated requests, secure client links, safe attachment intake, replies and delivery history | Low-effort collection with automatic continuation and a single outstanding-question history | [Requests](client-request-links.md), [email](email-delivery.md) |
| Native correction paths, grants, one-attempt writes and canonical readback | Verified real-provider outcomes for every enabled operation | [Posting](production-posting.md) |
| Brief, flux, aging, tie-out and portable binder | Approved financial reporting, budget comparison, cash planning and AR/AP decisions | [Brief](../services/backend/internal/transport/brief.go), [flux](../services/backend/internal/transport/flux.go), [aging](../services/backend/internal/transport/aging.go), [binder](close-review-package.md) |
| Firm/client permissions, row-level security, retention, operations and effort records | Executive access, dependable hosted operation and honest measured customer benefit | [Architecture](architecture.md), [operations](operations.md), [effort](../services/backend/internal/transport/effort.go) |

Recent merged work already added agent entry points to Attention and Client closes, an effort trend chart, and wider layout coverage. Do not rebuild them. Layout checks still do not establish complete real-provider journeys or usability. Every new screen uses Tray and the existing assistant context.

## What each user must receive

| User | Primary experience | Value to demonstrate |
| --- | --- | --- |
| Firm partner | Work: delivery portfolio, deadlines, capacity and escalations | More complete client work with less total human effort; a management/advisory service the firm owns |
| Manager/controller | Required accounts, owners, returns and release readiness | Nothing disappears between preparation, review, correction and publication |
| Accountant | Prepared work and a compact decision queue | Evidence, calculation, proposed effect and next action together; no repeated data entry |
| CEO | Business results and decisions | Understand performance, upcoming cash pressure and the few actions requiring attention |
| CFO/accounts lead | Performance versus plan, cash scenarios, AR/AP actions | Explain movements and coordinate operational decisions using approved facts |
| Client contact | One contextual request, usable on a phone | Supply the answer once, see receipt and what remains outstanding |
| Firm administrator | Existing firm/security settings, membership and client access | Invite/revoke the correct roles, inspect operations, export and recover without developer intervention |

## Build backlog: complete jobs, not isolated screens

All B01–B12 are required for the first cohort before partner-operated signup, including reporting, budgets and cash planning even when one client has not yet supplied a budget. Within each job, account and method applicability follows the agreed client scope; that is not permission to omit an entire build package. Owners below are responsible disciplines; assign named owners and estimates when scheduling. Each implementation PR must include the user journey, agent behavior, failure/recovery path and relevant acceptance evidence.

### B01 — Existing-client takeover and first useful result

**Extend:** setup, profiles, connector onboarding, imports, procedures and carry-forward. **Owner:** product + accounting + integrations.

Build a guided agent-led takeover: confirm company, period, accounting basis, timezone and permissions; inspect accounts and transaction families; import/validate prior schedules and opening balances; establish the last trusted close; identify inherited cleanup and recurring deliverables. Reuse approved firm templates, then show client-specific exceptions. The agent proposes scope and mappings; the accountant confirms them once.

Each imported schedule must tie its opening balance to the provider Trial Balance at the takeover date, with differences tracked as inherited cleanup. If there is no trusted prior close, establish an explicitly reviewed opening baseline before dependent results can be released.

**Done:** an unfamiliar accountant starts an existing eligible client without developer help, receives a source-backed useful draft, and sees every unresolved opening/history issue with an owner. The system cannot declare complete coverage by excluding accounts it could not understand. Signed engagement scope and exclusions are retained. Prerequisite missing evidence/access is explicit; assisted setup time is measured separately.

Here, a **firm template** is a reusable approved configuration: methods and account mappings (B01/B04), recurring obligations and assignments (B02), and report layout/mapping (B07). Reuse their existing records where present; changes remain versioned and client overrides explicit.

### B02 — Reliable portfolio and one decision queue

**Extend:** Work, Attention, Morning, Planning, review queues and effort. **Owner:** frontend + backend + accounting.

Show all required obligations, including undelegated clients, older unfinished periods and reopened closes. Align card counts with the exact list they open. Share a refresh/as-of policy across agent progress, source freshness, dependencies, review and brief; surface partial read failures. Keep owner, due date, client wait and reviewer return visible. Support reassignment and recurring firm templates.

Current code risks to resolve: [morningScope](../services/backend/internal/transport/morning.go) filters to current/prior month and last recorded sign-off, while readiness can subsequently reopen; [due-soon counts](../services/backend/internal/transport/work.go) include completed closes and their card opens a broader active list; [Work](../apps/frontend/src/features/closes/Work.tsx) reads sections on different refresh schedules. [Effort](../services/backend/internal/transport/effort.go) reports measured periods without requiring a completed comparable scope. Treat these as specific implementation follow-ups, not proof of a new runtime reproduction.

**Done:** a late change reintroduces the obligation automatically; each headline reconciles with its list; a failed read never appears as zero or complete. Automation and effort reports name their population/window and include unsupported work, manual fallbacks and recurring assistance. Report completed-scope comparisons separately from work in progress.

### B03 — Daily books and required account reconciliation

**Extend:** transaction capture, evidence matching, exception scans, native corrections, reconciliation and coverage. **Owner:** accounting + integrations.

Finish document-to-transaction linkage and ordinary coding/payee/dimension questions, duplicates, partial payments, transfers and clearing items. Provide a completion route for each admitted transaction family; a journal cannot substitute for correcting the source record when that is required.

The minimum account inventory includes bank/card, AR/AP, payroll and accrued liabilities, sales tax payable where applicable, clearing, prepaids, assets, deferred revenue, debt, equity and owner distributions. Ordinary cleanup explicitly includes Uncategorized/Ask My Accountant balances and nonzero Opening Balance Equity. Every other balance-sheet account needs a documented disposition: reconciled, supported roll-forward, or an accountant-approved engagement exclusion that prevents claims of full coverage. Inventory admitted transaction families including invoices, bills, purchases/checks, sales receipts, refunds/credits, payments, transfers and processor deposits; confirm each actual provider read/correction path. Each account has opening balance, activity, closing balance, source, differences and reviewer. When a provider lacks a required native operation, support a clearly recorded external completion and evidence check; verify its effort still meets the product target.

Daily books and request drafting need a per-client standing delegation with its allowed jobs, sources, period and stop conditions. Extend existing goals/grants; do not add a parallel delegation model. It does not authorize messages or ledger writes.

**Done:** no required account silently drops out; imperfect statements, outstanding items and unsupported native reconciliation have explicit tested outcomes. Oversize statements/transaction sets are safely processed in full or rejected as incomplete, never truncated into a successful result. Full statement/page totals and captured transaction counts reconcile.

### B04 — Recurring preparation and investigation-to-action

**Extend:** exact methods, firm procedures, durable preparation, investigations and schedules. **Owner:** agent engineering + accounting.

The launch automatic-draft targets are source-verified prepaid schedules, fixed incurred/recurring accruals, bank/card reconciliation candidates, simple asset/deferred-revenue/debt schedules and supported native correction candidates. Each work type needs its own eligibility checks, ambiguity stop and accountant acceptance; this is a build requirement, not current automation coverage. Existing simple deferred-revenue math and carry-forward in [accounting methods](accounting-methods.md#deferred-revenue-and-depreciation) must be reused, not built again. Add payroll recording preparation from an approved provider summary, tied to cash and liabilities, distinct from running payroll. Maintain relevant prepaids/accruals, assets, simple deferred revenue and debt schedules, including supported term changes, credits, cancellations and reversals. Connect a cited investigation to existing preparation capabilities or a precise unresolved question; do not leave a correct explanation at a dead end.

**Done:** the agent progresses under approved methods with the browser closed; missing evidence pauses only dependent work; an answer resumes it without a new prompt. Every enabled calculation version has accountant-approved examples. A rule learned from review remains a proposal until activated. Repeated jobs and restarts do not duplicate work.

### B05 — Client follow-through

**Extend:** request consolidation, safe intake, partial replies, outbox and reminders. **Owner:** collaboration + integrations.

Draft one contextual request covering unresolved questions, reuse already supplied evidence, select the authorized contact and show a send preview. Track partial answers and outstanding files; stop satisfied reminders; resume affected preparation after required checks. Escalate unanswered requests to the owner. All sends and reminders retain the existing human approval requirement.

**Done:** a contact responds on a phone without coaching; unsafe files remain isolated; the accountant sees what the answer resolved and what judgment still remains. Revoked/expired links, wrong contacts, failed delivery and duplicate replies recover visibly. Receipt, scan clearance, accounting resolution and delivery are separate states.

### B06 — Review, verified correction and late change

**Extend:** review canvas, returns, batch review, rehearsal, grants, native writes and source-change invalidation. **Owner:** accounting + backend.

Present proposal, source, deterministic calculation and ledger effect together. Allow amend, return, reject or approve and advance to the next decision while preserving context. Batch only compatible current items, with per-item exclusions and outcomes. Keep workpaper review, permission to write, verified write and close sign-off distinct.

Extend the existing Security center **Connector operations** into the firm-visible release matrix. Record each operation's implemented/local-tested, sandbox-verified, production-enabled, client-granted and accountant-accepted status, with dated evidence. Inventory QBO prepaid/accrual/loan/owner/settlement journals, bill updates, vendor credits, expense/deposit line recodes and payment/credit applications; Xero manual journals and bill recodes have separate entries. Deferred-revenue/depreciation entries are currently draft-only. Never infer activation from code or configuration defaults.

**Done:** human-approved operations pass provider-specific readback. Timeouts after a write enter reconciliation, never a blind retry. A late invoice, ledger update, mapping or policy change invalidates affected approvals and recalculates supported drafts; unchanged approvals survive. A posted result uses a separately approved correction, never an invisible rewrite. The same guarantees apply after sign-off and across the next period.

### B07 — Approved management accounts and executive access

**Build on:** ledger reports, brief, flux, aging, tie-out, binder and permissions. **Owner:** reporting + accounting + experience.

Build a firm-branded management pack with P&L, balance sheet, reconciled historical cash-flow statement, prior-period comparisons, material movements, working capital and next actions. Add reviewed, evidence-linked commentary, report mappings/templates, secure publication, PDF and spreadsheet export. Distinguish provisional actuals from reviewed/published results and unposted proposals. Historical cash flow requires validated provider output or an accountant-approved deterministic preparation method.

Add a client-scoped executive role enforced on APIs, agent capabilities, documents and exports. It reads permitted published information and permitted current views; separately scoped decision responses cannot grant preparation, review, posting or administration authority. Internal notes and sensitive payroll details stay private.

**Done:** each figure carries entity, period, basis, currency, as-of time and status, with authorized drill-down. Quantified variance drivers plus an unexplained remainder tie to the total movement. Observations are distinguished from causal hypotheses. Immutable published versions agree with exports; corrections identify superseded versions and preserve the original. Publication, notification and acknowledgment are recorded separately. Late source/mapping changes flag affected published results as superseded or requiring correction and prepare a new reviewed version without changing the historic report.

### B08 — Budget versus actual

**New workflow on:** report mappings and approved actuals. **Owner:** reporting + accounting.

Import or enter the agreed P&L budget; validate monthly totals and account mapping; retain approved baseline versions; show monthly/YTD actual, budget and prior period; investigate material differences. Provide a useful no-budget state and a mapping repair path. The agent may suggest a draft, but never silently invents an approved plan.

**Done:** zero/negative bases, sign conventions, remapping and budget revision have approved examples. The original plan remains reproducible; correcting an actual refreshes affected comparisons without overwriting the budget.

### B09 — Maintained 13-week cash decisions

**New workflow on:** reconciled cash, AR/AP and approved obligation inputs. **Owner:** planning + accounting.

Build dated receipts and disbursements for invoices, bills, payroll, taxes, debt and other commitments; weekly opening/closing cash; minimum daily balance within each week; editable collection/payment timing; and named scenarios such as a delayed receipt or hire. Accept approved external obligation schedules without implementing payroll or tax calculation. The agent prepares refreshes and flags missing inputs; a person approves assumptions before relying on a reviewed forecast.

**Done:** exact opening + receipts − disbursements = closing, carried to the next week. A Wednesday payroll shortfall is visible even if Thursday receipts make Friday positive. Avoid double-counting a bill and recurring estimate. Book cash is not labeled bank-available cash. Save sources, assumption changes, scenario and calculation version; compare prior forecasts to actual cash movement. B10 promised dates and current open-item amounts feed one base forecast; what-if timing overrides live only in a named scenario. A ledger/source or assumption change refreshes the affected draft forecast and invalidates its reviewed-current status while preserving the prior version. Scenarios never write to the ledger. Forecast arithmetic is verifiable; future receipts remain assumptions.

### B10 — Receivables and payables actions

**Extend:** aging, open items and selected payment/credit applications. **Owner:** accounts workflow + integrations.

Add invoice/bill-level owners, due dates, disputes, promised payment dates, notes and resolution status. Prepare collection messages using the client-authorized sender identity and recipient contacts, with explicit authority to contact the client's customers, per-message approval through the existing outbox, and visible bounce/suppression/contact-preference handling. Hand payment decisions to the client's external bill-pay tool; record the decision and handoff only, not execution. Promised dates feed the base forecast; speculative timing changes feed a named scenario, never a changed accounting fact or a duplicate cash item. A write-off is a proposed accounting correction through B06, not a manual paid/resolved status.

**Done:** the user can investigate, assign, follow up and resolve an item from its context; refresh reconciles status to the provider. Collection sends are approved and auditable. No payment execution or authority is inferred from a suggested action.

### B11 — Next month and maintained firm methods

**Extend:** recurring handover, carry-forward, procedures, standing approvals and history. **Owner:** agent engineering + accounting.

Carry current schedules, open questions, obligations and approved methods into the next period; apply client timezone/cutoff rules. Explain changed versus unchanged work. Existing standing approval operates only within its explicit human-granted bounds; the agent never expands it. Extend source-change handling to manual workpapers; B07/B09 own their later publication/forecast dependencies. Preserve the existing Year-End Handoff of Trial Balance, GL, schedules, adjustments and unresolved questions.

**Done:** two consecutive periods complete without rebuilding spreadsheets; changed terms reopen the affected work; approval bound to superseded source, method or mapping versions cannot cross periods; standing approvals remain valid only within their original explicit bounds. Removing access, changing a method or stopping work has explicit effects on queued and already executing jobs.

### B12 — Hosted operation and partner onboarding

**Extend:** current identity, isolation, monitoring, retention, export and recovery. **Owner:** platform + security + customer operations.

Finish live connector/model/scanner/email acceptance, read-only executive and staff access tests, MFA/revocation, an independent security review with no unresolved launch blockers, external worker monitoring, incident ownership, timed restore and provider reconciliation. Add a working contact/support route, truthful data/AI/subprocessor disclosures, agreed retention/export terms and clear workload/pricing terms. Use the existing commercial identity provider; validate enterprise federation/SSO and provisioning requirements before admitting an enterprise that depends on them.

Phase A must set numeric RTO/RPO and load/concurrency targets with named owners in [operations](operations.md) before testing; current default caps are not proven capacity. Hosted MCP is included for partner agent access: finish the existing edge routing/environment forwarding and verify the same OAuth, client scope and permission boundaries as the app.

**Done:** the configured release operates at representative admitted-firm load; a timed restore preserves evidence and reconciles external outcomes before work resumes; no routine recovery requires developer SQL. A partner receives done-for-you configuration, complete deliverables, a named support owner and an exit/export path. Record setup/service labor. No request for unpaid QA, training labels or positive testimonials.

## Integration contract

| Connection | What to finish/prove | Behavior when unavailable |
| --- | --- | --- |
| QBO, and separately Xero when admitted | Company identity, OAuth refresh/revocation, pagination, full populations, revisions, dimensions, attachments, report basis, operation-specific sandbox write/readback and production authority; dated capability outcomes for unposted bank feeds and native reconciliation | Preserve last snapshot with its timestamp, show failed refresh, pause dependent approval/publication. Never infer missing provider capability from marketing. |
| Stripe settlements, when included in the admitted client's workflow | Reuse existing settlement workpapers; validate source completeness, refunds/disputes, fees, clearing and payout reconciliation separately before enabling | Show unsettled/unsupported items and request support; an unverified processor path cannot silently enter the admitted scope. |
| Upload, approved Drive selection and forwarding | Preserve exact bytes/source identity, scan/OCR outcomes, duplicate versus replacement distinction and source lineage | Quarantine or ask for a usable source; never silently ignore a page, file or conflicting amount. Automatic Drive sync is not presumed present. |
| Email and client links | Real sender/routing, recipient authorization, approval, signed delivery events, safe replies and request correlation | Show failed/pending delivery; retain the draft and alternate follow-up action; no false claim that the client received or answered it. |
| Model/extraction provider | Authorized bounded context, redaction, source verification, model/version recording, timeout/cost limits and prompt-injection checks | Keep deterministic work progressing where possible; preserve pending work with a clear retry/escalation path; no fabricated fallback result. |
| Identity, business access and operations | Server-side role enforcement, client boundaries, session revocation, MFA, alert delivery, restore and deletion/export evidence | Fail closed on authority; keep operational errors visible without leaking client content. |

Reuse current API/MCP capability handlers and durable jobs. Add a capability only where a required job lacks one. Do not create a second scheduler, accounting engine, generic agent framework or parallel financial store. New domain records such as budget versions, cash scenarios and publication versions belong alongside current scoped records with normal migrations and permission checks.

## Seamless agent-first experience

Keep [Tray](design-system.md): existing tokens, light shell, Inter type, Hugeicons, PageHeader, shared rows/cards/charts, AgentComposer/AskAgent and AgentMark. These are implemented foundations, not proposed design work. Add shared components only after a genuine repeated pattern exists.

The primary navigation should express the user's jobs: existing Work/Attention for firm delivery and decisions, client context for accounting, and business results/planning for permitted executives. Executive navigation in the same Tray shell exposes Business overview, Reports, Cash plan and My decisions for the selected client; accountants reach these same views through client context. Firm operations and administration remain permission-scoped. Reuse existing routes/components where possible and add destinations only for these distinct jobs; do not create a parallel executive app.

Interaction requirements:

- Open to useful completed work, the next decision and actual ongoing progress. Offer natural language and precise controls; a prompt is never required for a routine continuation.
- Preserve firm/client/period, selected item and source context across agent panel, review and report drill-down. Show scope in the composer; ambiguous or cross-client intent requires explicit selection. Context switches cannot execute against the previous client.
- Show a compact summary first, with proposal, reason, source and next action one interaction away. Use progressive disclosure for detailed workpapers and logs; never hide unresolved scope or amounts.
- Automatically execute already delegated, permitted preparation. Ad hoc command suggestions retain a preview/confirmation before starting new work, matching the current design-system contract. No repeated confirmations for each already-authorized internal step.
- Group consequential decisions with an exact preview, current source/version and per-item outcome. External sending, ledger writes and release remain human-authorized. Fewer clicks cannot mean weaker authority.
- Use durable state language: queued, working, waiting for evidence, needs a decision, ready for review, verified or failed. Show last completed step and next action; no invented percentage, ETA or success animation before confirmation.
- Reuse supplied data and approved methods; preserve unsent text on recoverable failures. Keyboard navigation, visible focus, screen-reader status, accessible contrast, mobile requests and reduced motion are required. Do not stream private reasoning; expose evidence and concise action receipts.

Proposed performance acceptance targets, measured rather than advertised: visible input/navigation feedback within 100 ms; p95 interactive page/list readiness within 2 seconds under a documented representative dataset/concurrency profile; queued-command acknowledgment within 1 second; confirmed job-state changes visible within 5 seconds while online. Model and provider work runs asynchronously with real progress and failure state. Instrument timings and include slow provider, large list and poor connection cases; agreed load profiles must precede acceptance. Never make financial effects optimistic to meet a target.

## Accuracy as a release contract

“100% accurate” is the engineering objective for supported accounting output, not an assertion that a model or future event cannot fail. Enforce it through explicit invariants:

1. Exact decimal/money calculations; amounts cross APIs as decimal strings. Accountant-approved fixtures define rounding, dates, signs and treatments for every enabled version.
2. Verified evidence supports each accepted fact. Missing, contradictory, stale or ambiguous inputs block dependent approval rather than being guessed. Model confidence does not confer accounting authority.
3. Complete input populations, page counts and reconciliations are checked. Missing data is not zero; proposed entries are not posted actuals; recorded book cash is not available bank cash.
4. Inputs, procedures, mapping and source revisions are pinned through preparation, review and external effect. Changed inputs invalidate affected approval and published-current status; historical versions remain reproducible.
5. Permissions are enforced in every API, worker and agent capability. Duplicate requests are safe; uncertain external writes reconcile before any further attempt. A tenant or client boundary cannot be overridden through chat or a document.
6. Unexplained differences and excluded obligations are conspicuous. No general “complete” badge from a partial signed scope. Every released figure has a reproducible calculation and authorized source path.

Required checks include wrong client, OCR decimal error, missing pages, conflicting totals, duplicate events, partial replies, stale approval, late entries, changed policies, locked periods, timeout after write, revoked access, hidden payroll shortfall and worker restart. Use held-out accountant-reviewed cases as well as unit/integration checks. Start with roughly 20 varied client-months and 100 documents, including at least two periods; this is a planning floor, not statistical proof. Report false accepted facts, incorrect drafts, missed material exceptions, false-ready states, unauthorized/duplicate actions, abstentions and false exceptions by work type and denominator. Review acceptance alone is not a correctness label. A uselessly high abstention rate cannot pass the coverage or effort gates. All database development and checks use local PostgreSQL; never create a Neon branch.

## The connected demonstration

Target an 8–10 minute narrative using realistic resettable synthetic or authorized data, clearly labeled with connector/environment and feature activation. Maintain a documented reset procedure for the isolated demo company and recorded expected outcomes. Clearly labeled pre-staged before/after transitions are allowed for slow scanning, provider/model work and month changes; keep the real run receipts and timings available. The demo follows production paths; no hardcoded success, edited-away manual preparation or silent privilege shortcuts. This is the target acceptance script, not a claim the current product can perform every step.

| Moment | What the viewer experiences | What must be real |
| --- | --- | --- |
| 1. Arrive after the agent worked | Work shows prepared client work, one blocked request and the few decisions due | Persisted preparation, correct population/counts, source freshness |
| 2. Review a result | Open the proposal beside the cited source, arithmetic and ledger effect; approve eligible work | Current version, permission checks, deterministic result |
| 3. Resolve the ordinary mess | Show a missing page or conflicting amount caught, then a precise approved client request | A true blocker and a working resolution path |
| 4. Supply the answer | Client replies on a phone; agent resumes and returns the completed draft | Scanned/validated evidence, correlated reply, durable resumption |
| 5. Deliver the business result | Preview/release the approved management pack; CEO sees performance versus budget and actions | Reconciled statements, reviewed commentary, scoped role, identical export |
| 6. Make a cash decision | Delay a receipt and see the daily payroll shortfall and changed weekly outlook | Deterministic dated scenario with stated assumptions, no ledger write |
| 7. Handle change without losing control | Correct an invoice; only affected review/report state reopens with a clear difference | Source lineage, invalidation, preserved publication history |
| 8. Show continuity | Open a separately prepared next-period run with carried methods and unresolved items | Real persisted month-two execution; label prepared time transitions |

The “wow” is observable completion and recovery with almost no coordination effort. Measure it through unaided completion, time to decision and total work saved. Visual polish reinforces those outcomes; animation and fluent chat cannot replace them.

## Delivery sequence and launch gates

| Internal phase | Build IDs and dependency | Exit evidence |
| --- | --- | --- |
| A. Truthful operating base | B01/B02 foundations, provider capability inventory, acceptance corpus and B12 controls | Reproducible baseline, agreed client coverage, trustworthy state and working identity/source paths; fixed RTO/RPO/load targets; full PDF/OCR and local-PostgreSQL checks reproduced for the release revision |
| B. Complete accounting work | B03–B06 and B11; consumes A | Every required recurring job completes, including client waits, correction and next month |
| C. Business deliverables | B07/B08; consumes validated accounting and scoped access | Approved statements, budget comparison, commentary, publication and exports |
| D. Cash and actions | B09/B10; consumes reliable cash/open items and confirmed obligations | Reconstructible scenarios and actionable AR/AP, tested for timing/duplicates |
| E. Whole-product acceptance | All B01–B12 | Independent accounting, usability, hosted operation and complete-service acceptance |

Design and domain acceptance work for C/D can run alongside B; do not ship incomplete phases as the customer promise. No dates are asserted without implementation sizing and named owners.

Baseline note: the isolated `cec0068` branch has no Git conflicts; the earlier conflicts remain in a separate original working checkout and were not imported. Local `pdftotext` and `tesseract` were unavailable during this documentation work. Phase A must reproduce full PDF/OCR and database checks in the designated release environment, not mistake document validation or older green CI for acceptance of a new build.

Before partner-operated signup: approve every enabled accounting method; exercise at least three varied eligible clients across two consecutive periods plus a broader held-out corpus; pass every critical journey unaided; demonstrate real configured provider paths and recovery; and have zero critical correctness/security/false-completion failures in the exercised suite. Critical failures include accepted wrong amounts/treatments, unauthorized or duplicate external effects, cross-client disclosure, false complete/verified states, and evidence/history loss. All other mandatory functional, usability and operational gates still must pass; a noncritical label cannot waive a missing workflow. These sample sizes are minimum exercises, not proof of universal reliability.

Before signup, independent paid accountants complete the same synthetic or authorized client-period scopes with their normal tools and with this product; use comparable or counterbalanced assignments to limit learning bias and record the baseline before evaluation. Capture each partner firm's own baseline during onboarding before making any commercial savings claim.

Proposed value gate: at least 40% lower median total human effort versus a baseline fixed in advance for comparable completed scopes. Count preparation, chasing, review, corrections, off-platform work and recurring builder help; report every case, cohort size and setup effort separately. Proposed usability gates: first useful workpaper within 30 minutes after usable access/evidence, prepared short client reply within 3 minutes, source of a report figure within 60 seconds, and publishing an already approved pack within 5 minutes. Measure interface overhead, not professional judgment speed. Fix task scripts, load and evaluators before testing.

Inventory/manufacturing, consolidation, FX, complex revenue recognition, payroll execution, tax filing and payment rails require separate acceptance before dependent customers join. This does not exclude ordinary payroll-liability support, tax obligations in cash planning or year-end schedules. For this proposed first release, W-9 collection and 1099 preparation/filing remain with the firm's existing tax workflow and are explicitly excluded from the sold engagement; provide year-end accounting exports for the handoff. If the engagement includes these jobs, build and accept their privacy, collection and filing-system handoff before admitting that customer. No blanket promise of “all accounting.”

## Market basis and review record

Primary sources checked during the October 2 product analysis: [Basis small-firm workflow](https://www.getbasis.ai/solutions/small-firms), [Double accruals](https://doublehq.com/accruals/), [client portal](https://doublehq.com/custom-branded-client-portal/), [management reports](https://doublehq.com/custom-management-reports/), [Fathom reporting/planning](https://www.fathomhq.com/features), [Float cash forecasting](https://floatapp.com/), and [Numeric variance analysis](https://www.numeric.io/product/variance-analysis). These establish advertised competitive coverage, not independently measured quality. The resulting product requirements are our decisions, not customer interview findings.

The opportunity to prove is one connected experience from evidence to reviewed accounts to business decisions, with less total effort and dependable change handling. Do not claim competitors lack it or that savings have already been demonstrated. Firm methods, branded deliverables, complete setup, named support, clear commercial terms and portability make the founding-partner offer tangible.

### Independent review and disposition

Fable 5.1 (`claude-fable-5-1`) reviewed the full draft, both prior working analyses, current design system, architecture and delivery backlog on October 2, 2026, before this document was added. The CLI response confirmed that exact model. This was a documentation review, not accountant acceptance or a live product test. A second Fable 5.1 pass on October 2 verified the dispositions, confirmed the deferred-revenue correction and found no blocking contradiction. Its final editorial requests—recording that pass and including PDF/OCR/database validation in the Phase A exit row—are incorporated.

| Finding group | Disposition |
| --- | --- |
| Optional build-package loophole; scope provenance | All twelve packages are mandatory. Explicit precedence and newly required planning/collection scope are stated; earlier working analyses are consolidated here rather than added as competing plans. |
| B11 depended on later reporting/cash phases | Moved output invalidation to B07/B09; B11 retains accounting continuity. |
| Pre-signup value baseline; undefined criticality | Added independent paid comparison, later partner baseline, explicit critical failures and preservation of all other mandatory gates. |
| Takeover, templates, account/transaction completeness, daily delegation | Added opening-balance ties, no-trusted-close path, shared template definition, ordinary cleanup/account inventory and bounded standing delegation. |
| Automatic preparation and enabled operations | Enumerated target work types and a dated operation matrix extending the existing Security center. Payroll-summary preparation is explicit. |
| Suggested deferred revenue was a new method | Rejected after checking accounting-methods.md and the prepaid engine: calculations/carry-forward already exist and are draft-only. Reuse them; complete automation and acceptance. |
| Collections, payment handoff and cash timing ownership | Explicit customer-contact authority, sender/outbox handling, external bill-pay handoff, governed write-offs and one base timing source. |
| Standing approval, year-end and W-9/1099 scope | Clarified version-bound approval and preserved year-end exports; tax collection/filing workflow is explicitly outside the initial sold engagement. |
| Security, RTO/RPO/load, MCP and optional processor coverage | Added independent security review, numeric-target prerequisite, hosted MCP acceptance and separately validated Stripe scope. |
| Executive navigation, quality metrics, asynchronous demo and baseline defects | Added same-shell role destinations, per-work-type metrics/corpus floor, labeled transitions/reset procedure and current checkout/environment evidence. |

This specification adds no application feature, production activation or accuracy certification.
