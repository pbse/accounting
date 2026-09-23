# Delivery backlog and acceptance

This is the current delivery plan; the PRD remains the product requirements. Code, local validation, deployed operation and accountant acceptance are separate states. No release is certified enterprise-ready yet.

## Working agreement

Build one coherent feature at a time. Preserve exact arithmetic, immutable evidence and explicit human authority. Agents propose and resume work; they do not grant themselves permissions or sign off a close. Each slice must include its user flow, setup, failure/recovery instructions and focused checks. Reuse existing modules and native database constraints before adding abstractions or dependencies (Ponytail ladder rungs 2–4).

Scope not to change: supported calculation methods without accountant-approved acceptance examples; production ledger data; actual external messages; live provider calls while credentials are deferred. No automatic deployment, production migration or production posting is implied by this plan.

## Sequence

| Order | Feature | Completion evidence |
| --- | --- | --- |
| 1 | Multi-firm organization boundary — implemented, local checks passed | Registered organizations, scoped portfolio and close access, isolated carry-forward, public links and workers; two-firm negative checks. Existing local data retains explicit development ownership. |
| 2 | Close readiness and firm sign-off — implemented, local checks passed | Versioned required scope, latest-draft scope suggestion, external tasks, explained blockers, authorized sign-off, invalidation/reopening and exported decision history. The suggestion covers only existing drafts; the firm still defines the supported scope. Local identities are labeled development. |
| 3 | Firm procedures and client configuration — initial supported methods implemented | Versioned approved instructions/account names, client overrides, pinned cited context and explicit application. Client assignments isolate client-side accountants. New methods, materiality and automated learning remain pending. |
| 4 | QBO onboarding and read snapshots — sandbox accounts and bounded period report evidence implemented | Organization/client/user-bound OAuth, encrypted durable token refresh, company confirmation and bounded account snapshots passed mocked checks. Reported transaction rows, General Ledger, Trial Balance and P&L link to immutable evidence and deterministic account coverage proposals. Larger report snapshots retain full data with a bounded agent index; exact-account ledger pages can be saved as source-linked agent evidence. Live activation and raw transaction-entity synchronization remain pending. |
| 5 | Whole-close preparation coordinator — reviewable account and document plan implemented | Uploaded-source jobs, bounded model evidence selection, account-level QuickBooks coverage, exact account-to-job suggestions, account-scoped ledger search and cited row pages form a candidate plan. Portfolio prepare, recheck and stop commands act on up to 10 selected closes with per-close outcomes and current-state fences. Missing documents, unknown transactions and unsupported work remain explicit; complete-record certification is not inferred. |
| 6 | Client evidence collection | Scoped attachments, validation/quarantine and attribution; approved Resend delivery implemented with mocked checks; client PDF attachments use Cloudflare and TXT/CSV use an isolated on-demand ClamAV service plus firm release, then resume affected jobs. Signed delivery tracking and individually approved reminders are implemented with synthetic checks; live Resend activation remains pending. |
| 7 | Accounting coverage — reviewed draft methods implemented | Actual-day partial prepaids, one prospective or up to 20 separately cited multiple/retrospective credits, confirmed booked-balance adjustments, two month-boundary cancellation treatments, fixed incurred accruals and estimate-to-actual true-ups have explicit method/example confirmation. Reconciliation adds same-ID and opposite-sign investigation hints without inferring a match or entry. Each new treatment requires a client accountant-approved rule reference and final review; grouped/tolerance matching and prior-period journal revision remain unsupported. Accountant acceptance and live activation are separate. |
| 8 | Recurring close — reviewed prepaid refresh implemented | The next close applies selected current approved procedures, carries unresolved follow-up, and prepares reviewed schedules. A fresh sandbox period capture now shows exact prepaid Trial Balance rows; a client accountant-confirmed booked balance queues a source-linked revision and reopens affected review. Automated balance certification and other accounting methods remain outside this slice. |
| 9 | Approved production actions | Sandbox posting now requires a current approved workpaper review before posting approval or execution. Production code now uses separate OAuth credentials, exact client grants, account-balance/version preconditions, one durable journal attempt and canonical journal/balance readback for the initial full-month prepaid method. Uncertain outcomes remain blocked until a matching journal ID is found and read back; a definitive provider rejection without an ID still requires manual investigation rather than an automatic retry. Live credential activation and client-accountant validation remain outstanding. |
| 10 | Enterprise operations — implemented controls, hosted proof pending | Hosted firm enrollment, append-only membership/revocation, explicit retention/hold and close-scoped deletion with receipts, 500-close / 250-active-job default caps, and an operations snapshot are implemented. Malware scanning and local restore rehearsal predate this slice. External alert delivery, contractual backup expiry, key rotation, RLS and hosted recovery/provider reconciliation remain pending. |
| 11 | Agent capability access and final binder — binder implemented, MCP blocked | A release-gated ZIP captures current accountant sign-off, source files, report captures, receipts, decision history and SHA-256 manifest. It is limited to the supported scope and is not an audit opinion. Network-facing MCP access remains pending a specifically approved destination and data scope. |
| 12 | Cloud activation and pilot | GitHub repository/actions, Cloudflare, AuthKit, OpenAI and QBO sandbox configured; run the same supported flows on staging and an accountant-reviewed pilot. |

## Code-completion gaps after enterprise controls

The table above records delivered slices, not full PRD coverage. The remaining **first commercial release (R0–R2)** code is:

| PRD area | Remaining behavior |
| --- | --- |
| CTX-01, Appendix A | Sync bounded QuickBooks transaction entities and company settings with provider IDs, revisions, completeness windows and change polling/webhook recovery. Today's period reports are source evidence, not an entity read model or complete ledger sync. |
| EVD-01/02 | Controlled forwarded-document intake, selected-file cloud-drive access when permitted, copy-versus-revision deduplication and source-location checks for incomplete/contradictory documents. Keep unsupported formats or ambiguous entity routing blocked. |
| REC-01/02/03 | Reviewer-selected exact groups and split settlements, approved date/amount tolerances, transfer pairs, prior-period outstanding-item roll-forward and native reconciliation status where supported. Current exact one-to-one candidates and two-row group suggestions do not confirm matches. |
| POL-01, §5.4 | Show affected work before applying a new approved procedure version; preview each proposed entry against a versioned balance and flag item-level conflicts. Existing procedures are entered and approved manually. |
| EXC-01, SCH-02 | Complete supported uncategorized/clearing/owner-related investigations and additional approved accrual methods using accountant-owned examples. A journal entry is not a substitute for correcting a bill or payment. |
| COM-01/02 | Governed inbound email/forward attribution and unauthorized-sender review if email replies are offered; current secure request links and replies cover the scoped baseline. |
| ACT-01/02, PKG-01/02 | Add only accountant-approved native correction operations beyond the first full-month prepaid journal, and refresh other eligible schedules; emit actual portable PDF workpapers alongside the existing HTML, CSV and manifest. |
| §7, §15, Appendix C | Persistent natural-language command shortcut over the same durable jobs, operation budgets and checkpoints, and a scoped MCP adapter over existing authorization/capabilities. Current portfolio controls are direct actions. |
| Appendix E | External alert routing, key rotation/re-encryption, database row-level isolation and code-backed backup/restore verification. Current operations view and local restore rehearsal are not hosted monitoring or recovery proof. |

The later **R3** code remains:

| PRD area | Remaining behavior |
| --- | --- |
| §§5.1–5.2, REV-03 | More precise dependency refresh and portfolio-scale change review groups, with prior approvals retained only for unchanged independent work. |
| §5.4, SCH-03 | Portfolio/dependency-aware close rehearsal; reviewed loan and recurring schedules with explicit supported interest and payment conventions. |
| REC-04, Appendices A–B | Processor settlement reconciliation with charge, fee, refund, dispute and payout lineage; Xero tenant/account/report/journal support with its own permission and model-use rules. |
| §5.5, §15 | Accountant-reviewed procedure drafting from firm documents, suggested improvements from edits, affected-client preview and regression examples before activation. |

The **R4** code remains three separate product flows: Daily Books' continuous evidence and exception preparation, a Year-End Handoff of schedules/roll-forwards/open questions, and Cash Operations' receivable, payable and settlement preparation. Payment execution, bank changes and collections need separate authority and are not implied by those preparation flows. Optional Gmail, Graph, Drive, Slack and Plaid connectors are not launch prerequisites merely because they appear in the PRD.

Provider credentials, hosted resources, real-data/accountant acceptance, and production activation remain a separate configuration and verification phase. Code completion cannot establish those outcomes. For MCP, specify the client/destination and allowed data categories before enabling network access.

## Already implemented locally

Saved closes, exact-cent full-month prepaids, conservative CSV reconciliation, TXT/CSV/PDF evidence and OCR, source-linked review and recalculation, durable evidence-to-preparation jobs, live Work inbox, scoped text reply links, next-month prepaid schedule carry-forward and ZIP review packages. AuthKit, OpenAI and sandbox-posting integrations have code and synthetic/provider mocks; live activation is not claimed.

## Decisions

- 2026-09-22: Multi-firm SaaS with isolated organizations (confirmed by the user).
- OpenAI credentials/model will be supplied later; finish provider behavior with mocked checks.
- Local workspace token remains for development only.
- The client’s accounting professional approves accounting methods; no sample workpapers are available. Build explicit method approval and synthetic calculation examples rather than inventing active policy.
- Resend with a verified firm sender is selected for approved email delivery. Durable approved delivery is implemented and tested with a mocked provider. Actual sender/credentials remain unset; links are still manually shared until configured.

For actual checks and their limits see [validation](validation.md). For the source requirements see [PRD](product-requirements.md).

- 2026-09-22: Client attachments require automated malware scanning now (confirmed). Cloudflare WAF scans PDFs; an isolated on-demand ClamAV service scans TXT/CSV; clean scan plus firm release gates preparation. See [coverage requests](coverage-requests.md).
