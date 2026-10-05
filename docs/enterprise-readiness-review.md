# Enterprise readiness and remaining product work

**Status: supporting historical analysis, consolidated into the [agent-first build plan](agent-first-build-plan.md) in PR #223.** Follow that plan’s B01–B12, phases A–E and launch gates. Its scope overrides narrower recommendations or deferrals below. Dated code/test findings retain their original baseline and require rechecking; owner disciplines do not require an accounting-lead hire before development. See the [repository index](../INDEX.md) for document roles.

**Product scope update, October 2:** [Complete product requirements before design partner access](design-partner-product-bar.md) supersedes this review's narrower launch recommendations. It requires a complete recurring workflow before customer-operated access and expands the day-one scope to management reporting, budget comparison, cash planning and AR/AP action management. This document retains the technical inventory and validation evidence from the first review.

Reviewed October 2, 2026 against checkout `971ab7d`. Accounting firms remain the initial market; the same product must serve their clients' CEOs/CFOs and accounts teams. This is a delivery proposal and evidence assessment, not certification of the deployed product.

The product already has substantial accounting and control functionality. The shortest path to a credible commercial release is to finish and prove the complete QuickBooks close, add a restricted business-owner experience, and make onboarding and operations dependable. More accounting methods or another agent framework are not the immediate constraint.

**Verdict:** suitable for a clearly disclosed product demonstration; enterprise production readiness is not established. Missing functionality and unverified activation are different kinds of remaining work. Neither a large codebase nor passing synthetic tests establishes correctness on real books.

## Scope and evidence

The earliest PRD available in repository history is [v2 dated September 21](product-requirements.md), also present in the initial upload. No separate v1 was found in that history. This review uses v2 as the initial specification, the [delivery backlog](implementation-backlog.md), [maturity assessment](maturity.md), current source, and focused local tests. It does not assume every historic completion note is still accurate.

The checkout already had merge conflicts in `package.json` and `scripts/deploy-context-machine.test.mjs`. They were left untouched. GitNexus has no index for this repository, so source tracing used direct searches and file reads. Hosted configuration, customer files, live provider behavior and current production health were not inspected.

## The minimum product by user

| User | Questions the product must answer | Minimum useful experience |
| --- | --- | --- |
| Firm owner or partner | Which clients will close on time? What needs my decision? Is review taking less effort? | Portfolio status, due dates, owners, blockers, source freshness, review queue and measured human effort. |
| Preparer or accounts team | What has the agent prepared? What is missing? How do I finish the exception? | Automatic supported preparation, source-linked reconciliation and schedules, consolidated evidence requests, recoverable jobs and clear next actions. |
| Reviewer or controller | Can I reproduce this amount? What changed? What will posting do? | Source and calculation together, exact differences, approved policies, independent review where required, posting preview/readback, sign-off and portable evidence. |
| Client CEO or CFO | How is the business doing? What cash is recorded? Who owes us? What needs my attention? | One restricted business summary with dated ledger figures, revenue/profit movements, receivables/payables, close status and plain-language decisions; drill-down to permitted support. |
| Client contact | What exactly do you need from me? Did my answer resolve it? | Secure mobile-friendly requests, partial responses, safe uploads, clear receipt and automatic resumption of relevant work. |
| Firm administrator | Who can see or change records? Can we recover and leave safely? | Invitations, client assignments, revocation, authentication policy, activity history, exports, retention/deletion and dependable support. |

One accounting engine and permission model should serve both markets. The first in-house customer should fit the existing single-entity, USD service-business scope. Multi-entity consolidation, inventory, payroll execution, tax filing and payment execution are separate products or later scope.

## Coverage against the PRD

“Present” below means implementation exists, not live-provider or accountant acceptance.

| Requirement | Current evidence | What remains |
| --- | --- | --- |
| CTX-01 and source readiness | QBO/Xero captures, transaction indexes, ledger certification, client profiles and coverage exist. | Live completeness/pagination/reconnect evidence; consistent freshness expectations; client business-time boundaries. |
| EVD-01/02 | Uploads, extraction, source hashes, citations, entity checks, scanning gates and selected Drive intake exist. | Activate and prove safe PDF intake, OCR and recovery with realistic documents; evaluate false accepted facts. |
| POL-01 | Firm procedures, client overrides, versioned learning proposals and reviewed examples exist. | Independent accountant acceptance of every enabled method; make first-client setup understandable without internal terminology. |
| REC-01/02/03 | Statement workpapers, reviewed groups/tolerances/transfers and outstanding items exist. | Prove a complete bank/card workflow with imperfect statements and opening balances; complete supported investigation into a draft or question. Keep QBO native reconciliation a separate manual step where unverified. |
| SCH-01/02 and EXC-01 | Prepaids, accruals, exceptions and selected native corrections exist; extra loan/owner/settlement methods go beyond launch scope. | Demonstrate ordinary supported preparation without repeated manual orchestration; publish exactly which methods and corrections are enabled. |
| COM-01/02 | Consolidated requests, secure links, scanned attachments, partial replies and resumption exist. | Real sender/routing/scanner acceptance, delivery failure and revoked-link recovery, and a usable client journey. |
| REV-01/02 | Evidence review, versions, approval boundaries, independence and scoped sign-off exist. | Role-specific workflow validation and complete browser acceptance; establish a genuinely read-only executive role before sharing workspace access. |
| REV-03 and living workpapers | Source invalidation, change review, recurring lineage and automatic replacement for eligible prepaid invoices exist. | Late ledger changes and other/manual workpapers still need broader dependent recalculation; prove affected approvals reopen while unrelated approvals remain valid. |
| ACT-01/02 | Durable attempts, stale checks, grants, uncertain-outcome blocking and canonical readback exist. | Provider-specific sandbox proof and separate production authorization for each operation; prove recovery after timeout and restore. |
| PKG-01/02 | Signed binder, PDF/CSV/manifest, year-end handoff and next-month schedules exist. | Validate usefulness with an accountant and a second close; complete missing PDF checks in the review environment. |
| Agent execution and API/MCP | Durable goals, recurring handover, bounded investigations and shared capability access exist. | Close the investigation-to-preparation gap, broaden safe automatic draft preparation within launch scope, and validate hosted OAuth. |
| Enterprise controls | Tenant isolation, membership/revocation, retention, secrets, operations and staff controls exist. | Hosted isolation/MFA evidence, external outage detection, timed recovery, security review, workload tests and support ownership. |
| CEO/CFO experience | Close brief, P&L movements, tie-out and aging are reusable building blocks. | A dedicated business summary, consistent financial-report presentation, restricted access and controlled sharing/export. This extends the firm-focused PRD to the confirmed buyer/user scope. |

## Remaining work in priority order

### 1 Establish a reproducible release baseline

**Type:** release blocker. **Owner:** engineering.

Resolve the existing merge conflicts through the active integration work, run the normal build/CI on the exact release revision, and complete the PDF/OCR tests with the required tools. Keep local development and checks on local PostgreSQL; never create a Neon branch.

**Done when:** the reviewed revision builds, migrations apply and rerun cleanly, required tests pass, and a fresh environment can reproduce the supported journey. Do not treat historical green CI as evidence for this conflicted checkout.

### 2 Turn the accuracy principle into enforceable release gates

**Type:** validation plus any defects it exposes. **Owners:** accounting lead and engineering.

Create the small versioned replay corpus already proposed in the PRD/maturity review, with authorized historical books and independent accountant labels. Include missing pages, OCR decimal errors, wrong client, duplicates, split payments, late entries, locked periods, partial answers and changed policies. Separate development cases from held-out client-period cases; exclude future answers and final adjustments from historical inputs.

**Done when:** every enabled method has accountant-approved expected results, the release corpus has zero critical false acceptances or unsafe actions, and failures produce explicit blockers. Record coverage and denominators, not one blended “accuracy” score. The existing suggestion of roughly 20 client-months and 100 documents is a starting corpus, not proof of universal reliability.

### 3 Finish the supported agent close

**Type:** product work. **Owners:** agent engineering and accounting lead.

[StraightThrough](../services/backend/internal/preparation/straight.go) currently admits an agent-read prepaid invoice under an enabled equal-full-month procedure and a matching QBO capture. Other facts and work types commonly return to a person. [Investigations](../services/backend/internal/transport/written_answers.go) use bounded read-only tools and return an answer; they do not prepare the resulting draft.

Reuse the existing preparation jobs and capabilities to turn a supported investigation into a reviewable draft or one precise question. Finish bank reconciliation and simple accrual preparation to the declared launch boundary. Preserve human judgment where facts or treatment are ambiguous. Add recurring client timezone/cutoff semantics: [monthly handover](../services/backend/internal/transport/routines.go) currently uses UTC month boundaries.

**Done when:** delegated work advances with the browser closed; required evidence arriving resumes it; restarts do not duplicate work; stopping states what already happened; each blocked item names its owner and next action. People still approve external messages, ledger actions and final sign-off.

### 4 Complete change handling across the launch workflow

**Type:** product work. **Owners:** accounting engineering and platform.

Expand from the proven eligible prepaid-invoice replacement path to relevant late ledger entries, manually prepared supported work and next-period dependencies. Refresh and deterministically recalculate affected drafts, show the difference, and invalidate the corresponding review. Already posted work must lead to a separately reviewed correction rather than an automatic replacement entry.

**Done when:** a late invoice, credit, ledger change, account remapping or policy change cannot leave a stale “ready” result; unrelated current approvals survive. Repeat the scenario after sign-off and in month two.

### 5 Add the restricted CEO and CFO summary

**Type:** missing product capability. **Owners:** product, frontend and API engineering.

Build one “Business overview” from existing [brief](../services/backend/internal/transport/brief.go), [flux](../services/backend/internal/transport/flux.go), [aging](../apps/frontend/src/features/closes/Aging.tsx), tie-out and ledger snapshots. Show recorded cash/bank balances, revenue and profit for the selected period, material movements, overdue receivables, recorded payables, close status and decisions awaiting the client. Clearly distinguish month-end balances from live bank availability, posted figures from proposed adjustments, and missing values from zero.

Provide readable P&L and balance-sheet access from the connected ledger or explicitly labeled uploaded reports, with comparable periods, basis, currency, source and capture time. Add a concise accountant-reviewed management summary and permitted export. Do not derive a cash-flow statement or runway estimate from an incomplete balance snapshot.

Create and enforce a client-scoped read-only executive role. Existing [client assignments](../services/backend/internal/transport/access.go) govern access to a client; they alone are not a read-only contract. For example, [close creation](../services/backend/internal/transport/closes.go) checks client access. Hiding buttons is insufficient: inventory and deny unauthorized mutation routes server-side, including agent capabilities and exports outside the intended scope.

**Done when:** the CEO can answer the business questions above without navigating accounting setup, every figure drills into permitted source support, and negative API tests prove the role cannot prepare/change work, approve, post, administer members or read another client's data. A person with incomplete reports sees that limitation immediately.

### 6 Make onboarding and review usable without the builders

**Type:** product refinement and usability evidence. **Owners:** product and experience.

Keep the PRD's three decisions: work, rules and authority. Reuse [Setup](../apps/frontend/src/features/closes/Setup.tsx), but translate permission strings, connection setup and policy choices into role-appropriate actions. The first result must be a useful workpaper, with specific recovery for missing evidence or access. Make Work decisions the starting point; keep the many manual preparation options available without making them the primary journey.

Exercise keyboard-only review, screen-reader labels, focus after errors/dialogs, narrow screens, large tables, loading/empty/failure states and reconnect recovery. The existing [layout check](../scripts/layout-check.mjs) is useful but does not establish accessibility or full hosted user journeys; it explicitly omits some provider-dependent screens.

**Done when:** an accountant unfamiliar with the build completes onboarding, reviews a workpaper, requests evidence and exports the close without live coaching. Measure the PRD's ≤30-minute first-useful-workpaper target after usable access/evidence, including assistance separately. Do not invent a click-count target that rewards hiding necessary accounting decisions.

### 7 Activate and verify the real integration loop

**Type:** configuration and provider acceptance. **Owner:** platform/integrations.

Follow the existing [staging acceptance matrix](onboarding.md#staging-acceptance-matrix): hosted sign-in, QBO access/captures, real model interpretation on authorized evidence, safe PDF/TXT/CSV upload, approved sending, inbound handling, reconnection and hosted MCP when offered. Production writes require operation-specific sandbox checks and explicit client authority.

**Done when:** dated evidence names the revision, environment, company, operation and observed result. Timeout after a successful write is reconciled without another create. A connected account or supplied API key does not satisfy this gate.

### 8 Prove enterprise access and recovery

**Type:** operational acceptance and targeted hardening. **Owners:** platform and security.

Prove tenant and client isolation with the hosted restricted database role, MFA/session/revocation behavior, reviewer separation, agent scope, and staff content access. Test malicious document instructions and unauthorized tool calls. Require identity-provider SSO where a contracted customer's policy requires it; use the existing identity provider rather than building authentication. SCIM can wait for an actual provisioning requirement.

Configure an external monitor and alert receiver with a named responder. Perform a timed restore with the matching encryption key and provider reconciliation before workers resume. Agree recovery targets and support response commitments that the deployed system can demonstrate. Load-test the promised client/close volume, concurrency, queue delay and model cost with representative documents; the configured 500-close/250-job caps are not capacity evidence.

**Done when:** an independent security review has no unresolved launch-blocking findings, revocation is effective across access paths, an outage reaches the responder, and restore/load results are recorded. The [operations guide](operations.md) explicitly does not claim demonstrated hosted RTO/RPO. Never describe planned certification as obtained certification.

### 9 Make the product purchasable and supportable

**Type:** commercial launch work. **Owner:** founder/product with legal review.

The [contact page](../apps/frontend/src/features/marketing/Contact.tsx) now keeps each note and forwards it to Krith's inbox, and hello@getkrith.com is routed there (2026-10-05, [public site](marketing-site.md)). Still to provide: a named customer contact, explicit pilot scope, price, onboarding responsibilities and escalation process. The public site carries a [privacy notice](../apps/frontend/src/features/marketing/Privacy.tsx) and a [security page](../apps/frontend/src/features/marketing/Security.tsx); complete the actual business identity, reviewed agreements, data terms and subprocessor/retention commitments in each firm's written agreement.

**Done when:** a prospect can request a demo, understand the supported scope, sign the reviewed terms, get onboarded and reach support. Manual invoicing is sufficient for initial pilots. A self-service billing system is not required to prove value.

## What 100 percent accuracy should mean in the product

Treat 100% accuracy as the engineering objective. An unconditional promise that an AI system will never make an error cannot be established by finite tests. The PRD itself distinguishes zero critical acceptance failures from zero production risk.

- Every accepted critical amount/date has validated source support; ambiguous extraction remains a question.
- Every calculation uses deterministic exact arithmetic and passes its accounting invariants and approved fixtures.
- Every result names its entity, period, basis, currency, source version and supported scope.
- Missing evidence, stale data and unresolved differences prevent the corresponding verified/ready claim.
- Every external effect has current authority, a durable attempt and authoritative readback; uncertain outcomes remain blocked.
- Every material error has containment, affected-client assessment, accountant review and a documented correction path.

Measure false accepted facts, incorrect drafts, missed material exceptions, false-ready states and unauthorized/duplicate actions separately. Also measure how often the system abstains and the human effort required: a system that blocks everything is not a useful accurate product.

This approach is consistent with [NIST's AI RMF emphasis on evaluation](https://www.nist.gov/itl/ai-risk-management-framework). Keeping tool permissions narrow and enforcing human approval outside the model follows [OWASP's excessive-agency guidance](https://genai.owasp.org/llmrisk/llm062025-excessive-agency/). These sources support the control approach, not a certification or an accuracy claim for this product.

## Release sequence and pitch evidence

| Stage | Required result | Permitted pitch |
| --- | --- | --- |
| Demonstration | Reproducible supported journey, labeled synthetic/provider behavior, clear remaining gates and working contact route. | Agent-prepared, firm-approved close preparation; disclose what is demonstrated versus planned. |
| Authorized shadow use | Correctness corpus, hosted security/recovery prerequisites, accountant-reviewed historical comparison and usable evidence intake. | Supported read-only preparation alongside the firm's current process. |
| First commercial close | Complete request/reply/review/sign-off/binder/month-two journey; restricted CEO view for the confirmed dual-user scope; enabled integrations proven. | Sell the exact supported preparation service with measured scope and support. |
| Approved ledger execution | Operation-specific sandbox validation, client grants, current approvals, readback and incident/recovery evidence. | Claim only the verified enabled operations; this completes the PRD's R2 execution promise. |
| Broader enterprise sale | Customer-specific identity/procurement requirements, capacity and recovery commitments supported by evidence. | Offer only commitments the deployed service and operating team can meet. |

The demonstration should tell one story: the agent starts a client close, prepares ordinary work, refuses an incomplete statement, drafts one evidence request, resumes after the approved reply, presents a changed workpaper for review, verifies a human-approved sandbox posting, produces the scoped binder and carries into month two. End with the CEO's concise business summary. Show the refusal and recovery, not just the happy path.

Use the PRD's total human minutes per supported close as the main value measure, including setup, review, corrections and our assistance. The ≥40% reduction and paid-pilot conversion figures remain targets until observed. Assign accountable people to the owner roles above before giving delivery dates.

## Work to keep out of the first release

Defer additional accounting-method breadth, another agent/planning framework, new general dashboards, automatic payments, collections sending, payroll/tax execution, consolidation, FX accounting and broad mailbox ingestion. Forecasting, budgets and runway become separate scoped work when customers need them and the necessary inputs exist. Keep Xero/Stripe capabilities available as explicitly validated optional scope rather than making every connector a prerequisite for a QBO-first release.

Native reconciliation API behavior must be proved before it is advertised. Manual completion with an explicit status is acceptable. Autonomous approval or sending would contradict the recorded human-authority boundary.

## Validation performed for this review

A disposable native PostgreSQL 18 instance was created locally on loopback and all 106 numbered migrations applied successfully. No Neon branch or hosted database was used.

The existing prepaid, accrual, reconciliation and preparation package tests passed with `-count=1`. The broader documents/transport run failed on two PDF checks because `pdftotext` is absent; the extraction check also requires the documented OCR tools. This is an incomplete environment check, not evidence of passing PDF behavior.

All nine focused database-backed checks passed: tenant isolation, sign-off invalidation, client-reply resumption, bounded investigations, second-month carry-forward, governed finish, one-attempt sandbox/production posting with mocked providers, and independent review. These prove the exercised local scenarios, not live provider behavior.

Frontend build and browser checks were not rerun against the conflicted package manifest. No live providers, production posting, real-book accountant acceptance, hosted restoration or measured customer savings were verified. No application behavior was changed by this review.
