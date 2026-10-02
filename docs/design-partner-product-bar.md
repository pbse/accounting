# Complete product requirements before design partner access

**Status: supporting historical analysis, consolidated into the [agent-first build plan](agent-first-build-plan.md) in PR #223.** Follow that plan’s B01–B12, phases A–E and launch gates. Its scope overrides narrower recommendations or deferrals below. Dated code/test findings retain their original baseline and require rechecking; owner disciplines do not require an accounting-lead hire before development. See the [repository index](../INDEX.md) for document roles.

October 2, 2026. Market and product analysis for the founder, product team and accounting lead.

**The first design partner must receive a working recurring accounting service through the product.** They should be able to take an existing eligible client from its current books through preparation, review, approved correction, management reporting, cash planning and the next month. Their contribution is refinement of methods, fit and priorities after receiving value. Discovering missing basic workflows is our responsibility before access.

This raises the product bar beyond the earlier [enterprise readiness review](enterprise-readiness-review.md). In particular, a thin CEO summary is insufficient, budget and short-term cash planning belong in the proposed day-one offer, and a demonstration or shadow comparison does not qualify the product for customer-operated signup. The earlier review remains useful as a technical inventory; this document supersedes its narrower launch-scope recommendations.

## The product to build

**An agent-run accounting workspace that keeps client work moving, prepares the close, returns the decisions requiring an accountant, and produces the financial information business owners actually use.** The firm's methods govern the work. QuickBooks remains the ledger. People authorize external effects and release financial results.

The first market remains US accounting/CAS firms with ordinary single-entity USD service-business clients on QBO. The same client workspace supports an in-house controller/accounts team. Within that cohort, completeness includes common operational mess: missing receipts, opening discrepancies, partial payments, unclear transactions, stale connections, late invoices, changed schedules and staff handoffs.

“Supported scope” must describe a genuinely useful recurring job. It must not become a way to exclude every inconvenient account and then declare the rest complete. Before enrolling a client, inspect the actual chart of accounts, transaction families and recurring deliverables. If their ordinary work requires unavailable capability, finish that capability or do not admit that client yet.

## What the market now makes necessary

The sources below were opened and checked on October 2, 2026. **High confidence** means the cited public material supports the stated vendor capability or survey finding. It does not mean we independently tested a competitor. **Medium confidence** applies to the resulting market interpretation. All requirements later in this document are our proposed product decisions; they are not customer interview findings.

| Evidence | Consequence for our product |
| --- | --- |
| **High:** Basis describes GL cleanup, reviewed source-document entries, reconciliations, financial statements, variance explanation, 13-week cash forecasts, reusable procedures and source citations. [Basis small firms](https://www.getbasis.ai/solutions/small-firms) | **Medium:** agents, citations and reusable methods are insufficient as standalone differentiation. We need to demonstrate superior completion and review effort. |
| **High:** Double describes recurring schedules and entries for ordinary accrual accounting, transaction-linked client collaboration, and interactive management reporting. [Accruals](https://doublehq.com/accruals/), [client portal](https://doublehq.com/custom-branded-client-portal/), [reports](https://doublehq.com/custom-management-reports/) | **Medium:** client communication, maintained schedules and professional deliverables belong in the core buying comparison. |
| **High:** Double's review tools cover miscoding, payees, missing dimensions and documents; its tasks cover recurring templates, owners and handoffs. [File review](https://doublehq.com/integrated-file-review-tools/), [tasks](https://doublehq.com/task-management/) | **Medium:** routine bookkeeping cleanup and collaboration matter as much as a polished month-end calculation. |
| **High:** Intuit documents Books Close templates, roles, bank/card and other reconciliation tasks, and recurring close work. The dashboard documentation labels Books Close beta. [Templates](https://quickbooks.intuit.com/learn-support/en-us/help-article/close-books/set-books-close-templates-intuit-accountant-suite/L2rWOu1mn_US_en_US), [dashboard](https://quickbooks.intuit.com/learn-support/en-us/help-article/accounting-bookkeeping/understanding-books-close-dashboard/L99ArQkrW_US_en_US) | **Medium:** a portfolio checklist by itself competes with functionality offered by the underlying ledger vendor. |
| **High:** Fathom offers financial reporting, variance analysis, forecasts, scenarios, permissions and reusable report templates. Its budget documentation separates a retained budget from changing forecasts. [Features](https://www.fathomhq.com/features), [budgets](https://support.fathomhq.com/en/articles/15545682-build-a-budget-in-fathom) | **Medium:** the CEO proposition must deliver understandable performance and planning, not merely close-operation counts. |
| **High:** Float offers weekly/13-week cash views, invoice/bill timing and scenarios. Its FAQ says accounting data syncs every 24 hours with manual refresh, despite broader real-time marketing wording. [Float](https://floatapp.com/) | **Medium:** useful cash decisions require obligations, timing and visible freshness. A bank balance card is insufficient. |
| **High:** Numeric describes source-backed variance drafts with accountant editing/approval. [Variance analysis](https://www.numeric.io/product/variance-analysis) | **Medium:** explanations need evidence and a review path; percentage-change narration alone is weak value. |

CPA.com/AICPA PCPS's 2024 CAS survey associates higher-level advisory offerings and defined niches with stronger practice economics. It collected 206 self-selected responses, with most questions answered by fewer participants. Its findings are directional, self-reported associations, not evidence that our software will increase a firm's revenue. The implication for our strategy is to help firms deliver both efficient accounting and useful advisory output. [Survey and methodology](https://www.cpa.com/sites/cpa/files/2024-12/2024-CAS-Benchmark-Survey.pdf)

One important contradiction: broad integration marketing should not determine our connector promise. Double's setup guide instructs users to code QBO bank-feed transactions before review, while other pages market bank-feed features. Availability and workflow may differ. Verify our actual access to posted transactions, unposted feeds, attachments, revisions and reconciliation state directly. Do not infer those capabilities from another vendor's headline. [Setup guide](https://help.doublehq.com/en/articles/6623897-getting-started-guide)

The competitor sources establish expectations. They do not establish that all buyers need every feature, that competing implementations work equally well, or that our proposed combination is unique.

## Why each person would choose the product

| Person | Job and stake | What makes participation worthwhile | What would make them leave |
| --- | --- | --- | --- |
| Firm owner or CAS partner | Deliver consistently, retain clients, grow without proportional staffing effort. | More completed client work, clear review capacity, fewer escalations, and an advisory package the firm can sell under its own relationship. | Additional software administration, hidden manual service labor, unreliable completion counts or damage to client trust. |
| Accounting manager or controller | Know every required account is addressed and release defensible accounts on time. | Full portfolio obligations, dependable assignments, reconciliations, review returns, versioned sign-off and reliable changes after close. | Findings without a resolution path, approval bottlenecks, incomplete populations, stale reports and rework. |
| Working accountant or bookkeeper | Finish today's workload and preserve accounting judgment. | Prepared work with source support; one compact decision queue; reusable methods; client answers attached to the issue; useful bulk review. | Re-keying values, rebuilding schedules in Excel, excessive false alarms, chat prompting for every step or unexplained AI output. |
| Business CEO | Understand performance, cash pressure and the few decisions requiring their attention. | Current business picture, an approved monthly pack, meaningful cash scenarios and clear actions discussed with the accountant. | A technical accounting dashboard, old numbers presented as current, invented explanations or a second system to learn. |
| CFO or in-house finance lead | Explain variances, allocate resources and coordinate accounting with operating decisions. | Approved actuals versus plan, controllable forecast assumptions, AR/AP priorities and an evidence trail from report to ledger. | No budget baseline, unreliable working-capital data, unexplained forecast changes or inability to export. |
| Client contact or operations staff | Supply the needed information with little interruption. | Recognizable transaction context, mobile reply/upload, one consolidated request and a clear acknowledgment. | Repeated questions, confusing login requirements, requests for documents already supplied or exposure of unrelated private data. |

These are buying hypotheses derived from the workflows and evidence above. Their commercial strength still needs real buying decisions; no analysis can substitute for those. That does not justify admitting users to unfinished workflows.

## Required day one workflows

Each row is a complete customer job and a release requirement for the admitted cohort. “Present” describes code from the repository review, not live operational acceptance.

| Workflow | What the customer must be able to finish | Current position and remaining work |
| --- | --- | --- |
| **1 Take over an existing client** | Connect the correct company; confirm basis, period and access; import prior schedules and opening balances; map the chart; identify inherited cleanup; establish the last trusted close and recurring obligations. | Setup, profiles, imports, procedures and carry-forward provide parts. An integrated takeover journey with validated opening schedules and explicit unresolved history is not yet established. |
| **2 Keep everyday books moving** | Collect receipts/invoices, link evidence to transactions, resolve ordinary coding/payee questions, review duplicates, track missing data and perform supported native corrections. | Daily Books, exceptions, matching and selected recodes exist. Complete the relevant transaction families, document-to-transaction gaps and contextual resolution flow. A proposed journal must not substitute for a required source-record correction. |
| **3 Reconcile required accounts** | Finish bank/card reconciliation and supporting balance-sheet work: opening, activity, closing, outstanding items, source and reviewer. Include AR/AP, clearing, payroll liabilities, prepaids, fixed assets, deferred revenue and debt where applicable. | Statement matching, tie-out, coverage and several schedules exist. Establish the full account inventory and complete workflows for every ordinary required account, including reviewed external work where the ledger API lacks an operation. |
| **4 Maintain recurring schedules** | Create and maintain prepaid/accrual, simple fixed-asset/deferred-revenue and loan schedules as relevant; handle ordinary term changes, credits and supported reversals; tie them to the ledger; prepare the next month. | Significant calculations exist. Automatic intake is narrower, and some methods/entries remain manual or draft-only. Complete the recurring journey for enabled methods, rather than treating a calculator as the whole feature. |
| **5 Obtain missing answers** | Consolidate questions; assign the correct contact; approve sending; accept partial replies and safe attachments; stop satisfied requests; resume affected work; escalate unanswered items. | Much is implemented. Prove live delivery/scanning and remove avoidable manual restarts and duplicate requests. An answer can resolve evidence without resolving the accounting treatment. |
| **6 Review and correct work** | Inspect evidence and calculation together; amend, reject, return or approve; batch eligible routine decisions; preview ledger effect; authorize the exact operation; verify it externally. | Review and controlled writes exist. Finish operation-specific activation and a coherent exception-to-verified-result experience. Required corrections cannot end at a dead-end warning. |
| **7 Manage the firm's portfolio** | See all client obligations, deadlines, owners, unresolved decisions and client waits; rebalance work; manage preparer/reviewer handoffs and recurring templates. | Planning, handoffs, goals and portfolio reads exist. Resolve inconsistent counts/freshness and include all obligations, not only delegated or recently active closes. |
| **8 Deliver monthly management accounts** | Produce a firm-branded, accountant-approved pack: P&L, balance sheet, reconciled historical cash-flow statement, comparatives, material movements, working capital and next actions. Publish securely and export useful PDF and spreadsheet data. | Binder, flux, aging and exports are foundations. A close binder is not this management product. Build report mapping, assembly, commentary review, publication versions, access and correction handling. Validate the historical cash-flow statement from the ledger provider or an approved preparation method; a forecast is not its substitute. |
| **9 Compare performance with plan** | Import or enter the agreed P&L budget, validate mapping/totals, retain its version, compare monthly/YTD actuals and prior periods, investigate material variances. | No dedicated budget workflow was located. This is new product work, including the no-budget path and zero/negative comparison handling. Never silently create a business plan from history. |
| **10 Plan the next 13 weeks of cash** | Maintain opening cash, expected receipts, bills, payroll, taxes, debt and other commitments; model delayed collection, a hire or spending changes; approve assumptions and refresh weekly. | No cash-forecast workflow was located. Build deterministic calculations, dated source inputs, editable assumptions, scenarios and missing-obligation handling. Payroll/tax amounts can come from approved external schedules; this does not require running payroll or calculating tax. |
| **11 Act on receivables and payables** | Work invoice/bill-level queues with owners, due dates, disputes, promised payment dates and notes; prepare collection messages for human approval; hand payment decisions to the authorized payment system; refresh resolved items. | Aging, statements and selected applications exist. Ongoing action management and collection correspondence are missing extensions requiring explicit communication authority. Nothing here grants payment execution. |
| **12 Handle late change and repeat** | A new transaction/document reopens the relevant work, updates drafts and reports, preserves earlier published versions, and carries current methods/schedules/open items into the next period. | Some source-change and second-month paths are proven locally. Generalize to the admitted workflow, including manual workpapers and management/cash outputs, without inheriting stale approval. |

The client CEO enters through business results and decisions. The accountant enters through prepared work and exceptions. The partner enters through portfolio delivery. All three use the same underlying accounting facts, source versions and permission checks.

## Requirements that make those features genuinely useful

### Financial reports and explanation

Every displayed amount has a defined entity, period, basis, currency, source date and status: provisional, reviewed or published. Financial statements tie to the approved ledger/report snapshot. Do not add workpaper proposals to actuals as though they have posted. Comparative periods use compatible mappings; changes in mapping are reviewable.

Variance explanations reconcile their quantified contributors to the total change, with an explicit unexplained remainder. A larger vendor expense is an observed driver, not proof of the business cause. The agent can draft a question when it cannot establish why. The accountant approves client-facing commentary.

Published reports are immutable versions. A correction creates a new version and identifies the superseded report. The client's web view and export agree. Publication, notification delivery and client acknowledgment are distinct recorded events. Internal notes, individual payroll details and unrelated clients' evidence stay outside the client pack.

### Cash and planning

For every forecast week, opening cash plus receipts minus disbursements equals closing cash, which becomes the following week's opening cash. Distinguish source-backed actuals, known obligations and assumptions. Avoid counting the same cost as both an open bill and a recurring estimate, or treating an overdue invoice as guaranteed cash.

Calculate dated cash events within each week and show the minimum projected daily balance. A positive Friday balance must not conceal payroll due on Wednesday before a large Thursday collection. Test that case explicitly, including a receipt arriving later than assumed.

Opening book cash has reconciliation and freshness labels; it is not automatically bank-available cash. Expected receipt dates can differ from invoice due dates. Missing payroll, tax, debt or owner-distribution inputs require acknowledgment before a forecast is described as complete. Scenarios never write to the ledger. Budget revisions do not overwrite the original approved budget.

Save the as-of sources, assumptions, scenario and calculation version used for a reviewed cash decision. Weekly refresh produces a new version. Compare prior forecasts with actual receipts/disbursements and distinguish timing differences, changed assumptions and calculation revisions. A user must be able to reconstruct why a decision looked reasonable at the time.

A forecast is accurate arithmetic on stated assumptions, not certainty about the future. An accountant's review does not transform an unsupported prediction into a fact.

### Agent behavior

An agent should initiate approved recurring preparation, investigate permitted evidence, prepare supported work, request a decision and resume when that decision arrives. It should not require a person to rediscover which command to run at each handoff.

The unit of progress is a completed work item, not a model call, token count or chat answer. Work must survive refresh, sign-out, worker restart, duplicate delivery and provider delays. Every unfinished item has a comprehensible state, owner, reason and next action. Show what was actually completed and what remains cancelable.

The existing read-only investigation loop needs a governed path into existing draft-preparation capabilities. Source-verified automatic prepaid preparation must expand to the ordinary launch work, with no weakening of ambiguity checks. Firm learning produces proposed procedure changes; activation remains explicit. Human approval remains required before client sending, ledger writes and final release.

### Experience and adoption

Connection/setup must produce a specific first useful result and a clear next step. Reuse prior data and documents. An accountant must not maintain the same schedule independently in our product and a spreadsheet to compensate for an unfinished workflow.

Direct controls and natural language should reach the same outcome. Review should show the proposal, sources and accounting effect together. Client replies should be short, contextual and usable on a phone. Keyboard operation, focus, readable exports and error recovery are core functionality.

Provide tested onboarding/migration templates and named support. Preserve the firm's client relationship through branding and permissions. Coexist with QBO and the firm's existing engagement/tax systems; do not require migration of unrelated practice administration to get value.

## What makes the offer worth joining

The offer should be an operational benefit delivered by the product:

> We set up your firm's methods and selected existing clients. The system prepares their recurring accounting work, brings your team the decisions, and produces the management accounts and cash outlook your clients need. Your team retains review and approval. You help shape the next improvements after the complete service is working for you.

The proposed founding-partner package includes:

- **Done-for-you implementation:** agreed client eligibility, initial data/schedule mapping, roles, close obligations and branded reporting templates. Record our setup effort rather than hiding it as autonomous product work.
- **A complete first deliverable:** reviewable close work, verified supported corrections, an approved management pack, a maintained cash view and the next period ready to start.
- **Predictable commercial terms:** explicit active-client/workload pricing, included onboarding/support, and a separately scoped historical-cleanup service. Manual invoicing is acceptable; vague future pricing is not.
- **A named owner:** one person accountable for onboarding, incidents and escalation, with support commitments we can actually honor.
- **Control and portability:** retained ledger, reversible access, ordinary exports, clear data terms, and a usable exit without recreating the close.
- **Meaningful influence:** priority for reusable firm-method and workflow improvements, a modest agreed feedback cadence, and transparent decisions. No requirement to label training data, debug basic flows or provide a positive testimonial.

Their reason to join is reduced operational effort and a stronger service they can deliver to clients. A discount or a roadmap vote is supplementary. The proposed differentiation is the connected chain from evidence to reviewed accounts to business decision, with unusually dependable completion and change handling. We must prove that advantage against the firm's actual workflow; market research does not prove competitors lack it.

## What we must prove before the first partner signs up

Private interviews and demonstrations can occur during development. Customer-operated product access waits until the following gates pass. A design-partner label does not waive a gate.

| Gate | Evidence required |
| --- | --- |
| **Ordinary work coverage** | A named list of the admitted client's accounts, transaction families and recurring deliverables. Every required item has a working completion path; exclusions are agreed before access, not discovered during close. |
| **Independent accounting acceptance** | Versioned, authorized real-world/synthetic cases reviewed by an accountant independent of implementation, including a held-out set. Verify inputs, treatments, calculations, exceptions and outputs by work type. |
| **Complete repeated operation** | At least three varied eligible client datasets through two consecutive periods as a proposed minimum end-to-end exercise, alongside the broader corpus. Include opening-history issues and late changes. This minimum is not statistical proof of universal reliability. |
| **Useful automation coverage** | Measure work completed without manual preparation across the full agreed job, with human preparation, review, corrections, unsupported work and our assistance separately visible. Do not calculate a flattering rate only on easy successful invoices. |
| **Actual customer effort benefit** | Proposed internal release threshold: at least 40% lower median total human effort across the preselected comparable completed client-period scopes, counting preparation, chasing, review, correction, off-screen work and recurring builder assistance. Report each case, scope and sample size, with initial setup separately. Fix the cohort and baseline before evaluation. This is a product decision inherited from the PRD target, not a statistical guarantee or an advertised result. |
| **Unaided task completion** | Every critical scripted journey must be completed correctly by unfamiliar role-appropriate evaluators without builder coaching. Set task-time limits before testing: proposed starting limits are a first useful workpaper within 30 minutes after usable access/evidence, a prepared short client response within 3 minutes, locating a report figure's source within 60 seconds, and publishing an already approved pack within 5 minutes. These measure interface overhead, not speed of professional judgment or document acquisition. Paid domain evaluators can do this before partner access. |
| **Exception and recovery completion** | Missing page, conflicting amount, disconnected ledger, partial reply, duplicate event, timeout after write, changed approval, changed published report and worker restart all have tested outcomes. No routine recovery requires a developer database edit. |
| **Truthful product state** | No false complete/verified state from missing data, changed evidence or a failed read. Headline cards and their lists agree. Signed-off, published, delivered and paid have separate meanings. |
| **Hosted provider acceptance** | Actual configured model/extraction, auth, scanner, ledger and communication paths work in the deployed environment. Enabled writes pass operation-specific sandbox tests and have appropriate production authority. Mock responses are not this evidence. |
| **Enterprise operation** | Tested client/tenant isolation, executive read-only access, MFA/revocation, staff restrictions, approval separation, monitoring delivery, representative workload performance, timed restore/provider reconciliation, data/export terms and support ownership. |

Zero critical failures is a release condition for the exercised suite. It does not certify that no future defect exists. Broadening the admitted cohort or enabling a new accounting method requires new acceptance evidence. Fewer false alarms and less manual intervention matter alongside refusal of unsafe work.

Calculation methods may evolve, but each enabled version must already be correct under its declared policy. Changing a rule requires reviewed examples, versioning and impact assessment. Incorrect arithmetic, unauthorized actions and silent incompleteness are not acceptable learning opportunities for a design partner.

## Changes required in the current build plan

The [existing implementation review](enterprise-readiness-review.md) identifies substantial foundations: exact arithmetic, evidence, reviews, controlled writes, tenant isolation, recurring jobs, client requests and portable binders. The existing [maturity re-review](maturity.md#implementation-re-review-and-dashboard-specification-2026-10-02) also records dashboard-count, refresh and measurement defects. Those findings remain blockers to trusting an operational home; they were not independently reproduced again in this market-analysis pass.

Organize delivery into the following dependency sequence. These are internal milestones, not permission to sell a partial stage.

| Sequence | Deliverable | Main dependency and exit |
| --- | --- | --- |
| **A Establish a trustworthy operating base** | Clean release revision; authoritative account/obligation inventory; consistent readiness and freshness; verified provider capability matrix; realistic accounting corpus. | Existing merge conflicts, environment gaps and state defects resolved. The exact deployed version is reproducible. |
| **B Complete recurring accounting work** | Existing-client takeover, normal daily cleanup, required account reconciliations, maintained schedules, client follow-through, review returns, approved corrections/readback and next month. | Each admitted transaction family finishes through the product, including exceptions. Reuse current capabilities and durable jobs. |
| **C Complete the business deliverable** | Financial statements, versioned budget/actuals, source-backed commentary, branded management packs, client publication and executive access. | Uses the same validated snapshots and approval state as accounting. No separate financial truth in the dashboard. |
| **D Complete cash decisions** | Maintained 13-week cash scenarios and AR/AP action ownership, including approved collection drafts and external payment handoff. | Reliable open-item data plus confirmed obligations/assumptions; no double counting or implicit send/payment authority. |
| **E Prove the entire product and service** | Independent accounting/usability acceptance, repeated periods, hosted recovery/capacity/security proof, actual effort comparison, onboarding/support/commercial readiness. | Every signup gate above passes for the first admitted cohort. Only then open partner-operated access. |

Design of C and D and independent accounting preparation can proceed while B is implemented. That is useful parallel work, not a reason to release unfinished stages. Estimates should follow implementation sizing, actual connector tests and named staffing; a feature-count-based launch date would be misleading.

Important additions versus the original close-focused PRD are management-report publication, budget versions, short-term cash planning and action-oriented AR/AP. Important completion work is takeover, daily transaction resolution, broad enough agent preparation, late-change propagation and verified native operations. These should be explicit requirements rather than miscellaneous dashboard enhancements.

## Breadth that belongs outside this first customer promise

This release is feature-rich across the recurring jobs of its admitted customers. It does not need to become a global ERP to meet that standard. Inventory/manufacturing, consolidation, foreign-exchange accounting, complex revenue recognition, payroll execution, tax filing and payment rails require additional scope before customers depending on them can join.

That boundary does not remove related ordinary accounting work. Payroll liabilities still need source-backed review; tax obligations still belong in a cash plan when relevant; debt balances still need support; and the year-end accountant needs the trial balance, GL, schedules, adjustments and questions. W-9/1099 preparation becomes required if sold as part of a full US bookkeeping engagement, with explicit collection/privacy controls and a filing-system handoff. Do not quietly promise it through generic “all accounting” language.

A useful refusal handles a genuine limitation and gives the user a path forward. A product that refuses most normal work is unfinished, however safe its arithmetic is.

## Evidence limits and work performed

This analysis used three parallel research lenses—market/competitors, firm/accountant work, and CEO/CFO decisions—plus primary-source verification and a second adversarial completeness review. Available native research agents were used; the skill's named external-vendor agents were not available. Public sources were checked against the claims; no customer interviews, competitor hands-on trials, willingness-to-pay tests or new production validation were performed.

Current repository code/docs were compared with the proposed workflows. The prior local test results remain recorded in the technical review and were not rerun for this documentation-only change. No application feature, external communication, hosted setting or ledger record was changed. Concurrent edits in `docs/maturity.md` and existing merge conflicts were preserved.
