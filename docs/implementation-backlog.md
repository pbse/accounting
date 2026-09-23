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
| 4 | QBO onboarding and read snapshots — sandbox accounts and bounded period report evidence implemented | Organization/client/user-bound OAuth, encrypted durable token refresh, company confirmation and bounded account snapshots passed mocked checks. Reported transaction rows, General Ledger, Trial Balance and P&L link to immutable evidence and deterministic account coverage proposals. Live activation, raw transaction-entity synchronization and large-ledger evidence selection remain pending. |
| 5 | Whole-close preparation coordinator — initial scoped run implemented | Pinned uploaded-source scope reuses durable jobs, groups questions with original-job links and tracks source changes. The UI puts upload, next decisions and Work ahead of sign-off, and shows proposed work kinds and ledger coverage. A snapshot-checked reviewer action stops unresolved evidence jobs, revokes unused links, and permits individual restart. Large ready-evidence inventories use bounded, reviewable model selection. The Work inbox previews and stops unresolved jobs across up to 10 selected closes, with separate outcomes. Period-ledger coverage and consolidated requests/emails are implemented. Complete semantic scope inference, large-ledger selection and other portfolio commands remain pending. |
| 6 | Client evidence collection | Scoped attachments, validation/quarantine and attribution; approved Resend delivery implemented with mocked checks; client PDF attachments use Cloudflare and TXT/CSV use an isolated on-demand ClamAV service plus firm release, then resume affected jobs. Signed delivery tracking and individually approved reminders are implemented with synthetic checks; live Resend activation remains pending. |
| 7 | Accounting coverage | Actual-day partial prepaids, one prospective credit, confirmed booked-balance adjustments, two reviewed month-boundary cancellation treatments and fixed incurred accruals with optional reversal proposals are implemented with explicit method/example confirmation and existing accountant review. Reconciliation gives source-row investigation hints without inferring a match or entry. Multiple/retrospective credits, broader accrual methods and approved exception treatments remain pending. Accountant acceptance and live activation are separate. |
| 8 | Complete recurring close — partial | The next close can apply explicitly selected current approved procedures and carry unresolved jobs/unapproved drafts as incomplete readiness steps. Carried schedules clear historical booked balances and never claim a verified new-month ledger balance. Fresh-period agent resumption and actual ledger balance comparison remain pending. |
| 9 | Approved production actions | Sandbox posting now requires a current approved workpaper review before posting approval or execution. Production still needs exact current grants, ledger preconditions, durable attempts and canonical readback; uncertain outcomes cannot be blindly retried. Production credentials and authorization remain separate. |
| 10 | Enterprise operations | Entity permissions, audit export, retention/deletion policy, malware scanning, per-firm resource bounds, backups/restore, monitoring and incident runbooks. Local synthetic PostgreSQL restore rehearsal passed; hosted recovery and provider reconciliation remain required. |
| 11 | Agent capability access and final binder | The existing portable ZIP now includes captured QuickBooks report JSON alongside scope, sources, workpapers, receipts, unresolved work and sign-offs. MCP invocation of the same scoped capabilities and final accountant-certified binder remain pending. |
| 12 | Cloud activation and pilot | GitHub repository/actions, Cloudflare, AuthKit, OpenAI and QBO sandbox configured; run the same supported flows on staging and an accountant-reviewed pilot. |

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
