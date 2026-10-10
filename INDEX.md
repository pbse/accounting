# Repository documentation index

Start here to find the governing plan, implementation details and acceptance evidence. This is a navigation map, not another backlog. Update it when adding or moving a project document.

## Start here: what governs the build

1. Read [AGENTS.md](AGENTS.md) for repository working instructions and [README.md](README.md) for local setup, commands and code layout.
2. Follow [docs/agent-first-build-plan.md](docs/agent-first-build-plan.md): **the authoritative remaining build scope, B01–B12, phases A–E and partner-signup gates**, merged in PR #223. All twelve packages are required; internal milestones are not partial launch products.
3. Check [docs/implementation-backlog.md](docs/implementation-backlog.md) for implementation progress and known limits, then read the relevant feature documents below and inspect current code before changing it.
4. Use [docs/maturity.md](docs/maturity.md) for dated audit findings, buyer/dashboard rationale, no-accounting-lead working guidance and follow-ups mapped to B01–B12.
5. Record proof in [docs/validation.md](docs/validation.md); use [docs/onboarding.md](docs/onboarding.md), [docs/deployment.md](docs/deployment.md) and [docs/operations.md](docs/operations.md) for environment acceptance and operation.

**Precedence:** for remaining launch scope, sequence and gates, the agent-first build plan governs over the original PRD, earlier reviews and narrower feature deferrals. Feature docs explain current behavior and implementation contracts; a requirement in the plan does not prove that behavior exists. Reconcile conflicting docs explicitly while preserving exact accounting, permissions, source lineage and human authority. Code present, locally tested, hosted, provider-verified and accountant-accepted are separate states.

## Planning, product and evidence

| Document | What it contains / when to read it |
| --- | --- |
| [Agent-first build plan](docs/agent-first-build-plan.md) | Required customer jobs, agent behavior, shared UX, accuracy/performance, complete demo, delivery dependencies and acceptance. Start every build package here. |
| [Implementation backlog](docs/implementation-backlog.md) | Delivery progress, existing coverage, decisions and remaining activation work. Update progress here without creating a competing launch plan. |
| [Maturity](docs/maturity.md) | Historical and October 2 implementation reviews, concrete defects, dashboard metric definitions, market/persona reasoning, acceptance detail and B01–B12 crosswalk. |
| [Design-partner product bar](docs/design-partner-product-bar.md) | Supporting October 2 market/persona analysis and complete-service offer. Consolidated into the build plan; not a separate source of launch authority. |
| [Enterprise readiness review](docs/enterprise-readiness-review.md) | Supporting technical inventory, earlier recommendations and dated local test results. Narrower scope/deferrals are superseded by the build plan. |
| [Original product requirements](docs/product-requirements.md) | Original customer/problem definition, requirement IDs, boundaries and success targets. Read for provenance; the build plan extends its scope. |
| [PRD review](docs/prd-review.md) | Earlier interpretation of the PRD and foundation design decisions; historical planning context. |
| [Acceptance corpus](docs/acceptance-corpus.md) | The release corpus's case format, how to run it through the real pipeline and how a run is scored (phase A). The real corpus holds client data and stays outside the repository. |
| [Demonstration seed](docs/seed-data.md) | `cmd/seed`: one firm with ten clients and four months of books, documents, agent work and decisions, written through the real routes, for seeing the workspace alive; how to run it against a local or the hosted firm and remove it again. |
| [Provider capability inventory](docs/provider-capabilities.md) | Per integration-contract row, what each provider path does on main and how far it is proven (live, synthetic, untested or missing), with the gaps that remain (phase A). As of 2026-10-06, nothing is proven against a live provider. |
| [Validation history](docs/validation.md) | Dated commands/results, exercised behavior and explicit unverified areas. Consult before claiming a feature is validated. |

## Find documents by build package

These are pointers into the existing implementation, not evidence that a package is complete. The build plan supplies acceptance for new budgets, cash plans, publication and executive access where a dedicated implementation document does not yet exist.

| Package | Primary supporting documents |
| --- | --- |
| **B01 Takeover** | [Onboarding](docs/onboarding.md), [QBO](docs/quickbooks-onboarding.md), [firm procedures](docs/firm-procedures.md), [carry-forward](docs/carry-forward.md) |
| **B02 Portfolio and decisions** | [Work inbox](docs/work-inbox.md), [coordinator](docs/close-coordinator.md), [maturity dashboard specification](docs/maturity.md#implementation-re-review-and-dashboard-specification-2026-10-02) |
| **B03 Daily books and account reconciliation** | [Saved closes/reconciliation](docs/saved-closes-and-reconciliation.md), [source sync](docs/source-sync-and-forwarding.md), [coverage](docs/coverage-requests.md), [posting](docs/production-posting.md) |
| **B04 Recurring preparation and investigation** | [Accounting methods](docs/accounting-methods.md), [evidence/preparation](docs/evidence-and-preparation.md), [assistant](docs/chat.md), [agentic chat plan](docs/agent-chat.md), [firm procedures](docs/firm-procedures.md) |
| **B05 Client follow-through** | [Requests](docs/client-request-links.md), [coverage requests](docs/coverage-requests.md), [email](docs/email-delivery.md), [scanning](docs/cloudflare-upload-scanning.md) |
| **B06 Review, corrections and change** | [Review](docs/workpaper-review.md), [change review](docs/change-review-and-requests.md), [posting](docs/production-posting.md), [operations](docs/operations.md) |
| **B07 Management and executive reports** | [Management reports](docs/management-reports.md), [build plan](docs/agent-first-build-plan.md), [existing binder/export](docs/close-review-package.md), [access architecture](docs/architecture.md), [design system](docs/design-system.md) |
| **B08 Budget comparison** | [Budget against actual](docs/management-reports.md#budget-against-actual), [build plan](docs/agent-first-build-plan.md), [supporting business requirements](docs/design-partner-product-bar.md) |
| **B09 Maintained cash decisions** | [Cash plan](docs/cash-plan.md), [build plan](docs/agent-first-build-plan.md), [cash assumptions and daily shortfall rationale](docs/design-partner-product-bar.md#cash-and-planning) |
| **B10 AR/AP actions** | [Open item follow-up](docs/work-inbox.md#cash-operations-overdue-receivables-and-payables), [build plan](docs/agent-first-build-plan.md), [existing email/outbox controls](docs/email-delivery.md), [governed corrections](docs/production-posting.md) |
| **B11 Next month and continuity** | [Carry-forward](docs/carry-forward.md), [coordinator](docs/close-coordinator.md), [methods](docs/accounting-methods.md), [package/year-end exports](docs/close-review-package.md) |
| **B12 Hosted and partner operation** | [Onboarding](docs/onboarding.md), [deployment](docs/deployment.md), [operations](docs/operations.md), [auth](docs/auth-sandbox-staging.md), [MCP](docs/mcp.md), [validation](docs/validation.md) |

## Architecture, agent runtime and interface

| Document | Contents |
| --- | --- |
| [Architecture](docs/architecture.md) | System boundaries, identity/authority, exact accounting and shared capabilities. |
| [Design system](docs/design-system.md) | Tray shell, components, agent interaction conventions and layout checks. Reuse for accountant and executive experiences. |
| [Workspace simplification](docs/workspace-simplification.md) | The 2026-10-07 audit's decisions: the cleaner look, the top bar, and the PR sequence that rebuilds the home, Clients, the close page, Firm settings and the copy. |
| [Workflow engine](docs/workflow-engine.md) | Go durable workflow primitives, activity execution and current/deferred engine capabilities. |
| [LLM gateway](docs/llm-gateway.md) | Model routing, context-machine integration, execution and operational boundaries. |
| [Context-machine deployment](docs/context-machine-deployment.md) | Runtime configuration, deployment and verification procedure. |
| [Agentic chat plan](docs/agent-chat.md) | The governing design for the assistant rebuild: model-chosen tool reads, writes as confirmed cards, event protocol, Context Machine constraints, panel look and feel, evaluation and the four delivery PRs (2026-10-07). |
| [Assistant](docs/chat.md) | Current scoped chat behavior: keyword reads, cards, written answers and investigations, until the agentic chat plan replaces each part. |
| [MCP](docs/mcp.md) | Agent-facing capabilities, attribution, OAuth/permissions and hosted setup. |
| [Admin panel](docs/admin-panel.md) | Platform-staff console, separate staff identity, workflows and access controls. |

## Accounting, evidence and customer workflows

| Document | Contents |
| --- | --- |
| [Accounting methods](docs/accounting-methods.md) | Supported deterministic treatments, eligibility, examples and accountant approval. |
| [Prepaid preparation](docs/prepaid-preparation.md) | Initial prepaid capability, input/output and acceptance boundaries. |
| [Saved closes and reconciliation](docs/saved-closes-and-reconciliation.md) | Persisted closes, statement workpapers, matching and review. |
| [Evidence and preparation](docs/evidence-and-preparation.md) | Evidence intake, extraction, proposals and durable preparation jobs. |
| [Recalculation and PDF](docs/recalculation-and-pdf.md) | Revised drafts, PDF/OCR setup and evidence handling. |
| [Source sync and forwarding](docs/source-sync-and-forwarding.md) | QBO transaction captures, ledger certification, optional Drive intake and forwarded attachments. |
| [Work inbox](docs/work-inbox.md) | Live preparation, evidence coordination, waiting states and decision queues. |
| [Close coordinator](docs/close-coordinator.md) | Delegated goals, recurring handover, coverage and close progression. |
| [Workpaper review](docs/workpaper-review.md) | Review decisions, version freshness and changed evidence. |
| [Change review and requests](docs/change-review-and-requests.md) | Focused re-review and consolidated unsent requests. |
| [Client request links](docs/client-request-links.md) | Scoped client reply/upload links, expiry and revocation. |
| [Coverage requests](docs/coverage-requests.md) | Missing-account/evidence resolution and required attachment scanning. |
| [Email delivery](docs/email-delivery.md) | Approved sends, inbound replies, reminders, delivery and recovery. |
| [Firm procedures](docs/firm-procedures.md) | Approved methods, client accounting authority and proposed learning. |
| [Carry-forward](docs/carry-forward.md) | Next-period schedules and bounded standing approval. |
| [Management reports](docs/management-reports.md) | Month-end Profit and Loss, Balance Sheet and working capital from captured reports; status, comparison and drill-down. |
| [Cash plan](docs/cash-plan.md) | The 13-week book-cash plan from captured balances, open items, people's promised and bill-pay dates and approved schedules. |
| [Close review package](docs/close-review-package.md) | Binder, sign-off scope, manifests and portable supporting exports. |
| [Settlement reconciliation](docs/settlement-reconciliation.md) | Stripe payout/deposit support, exports, connector flow and limits. |
| [Production posting](docs/production-posting.md) | Exact approved effects, grants, operation activation, one-attempt execution and readback/recovery. |
| [QuickBooks onboarding](docs/quickbooks-onboarding.md) | Per-client QBO OAuth, sandbox company and ledger snapshots. |
| [Xero onboarding](docs/xero-onboarding.md) | Xero connection, period reads, supported corrections and separate provider acceptance. |

## Setup, security and operations

| Document | Contents |
| --- | --- |
| [Onboarding](docs/onboarding.md) | Local/hosted setup, supported journeys and staging acceptance matrix. |
| [Public site](docs/marketing-site.md) | getkrith.com's pages and links, its design, the hero and agents animations, and the contact form's storage and email forwarding. |
| [Sign-in](docs/sign-in.md) | Google and Microsoft sign-in, company addresses only, firms created and joined by company domain, and roles. |
| [Auth and sandbox staging](docs/auth-sandbox-staging.md) | Hosted sign-in/session modes, sandbox authority and verification. |
| [Cloudflare upload scanning](docs/cloudflare-upload-scanning.md) | Required PDF scanning, TXT/CSV scanner, quarantine/release and deployment. |
| [Server rendering](docs/server-rendering.md) | Workspace pages rendered at the edge with their data: routes with loaders, TanStack Query, the Worker, headers, and the rules for code that renders on the server. |
| [Page speed](docs/performance.md) | The 400 ms page budget, what made pages slow, container size and placement, connection pooling, prefetching in the browser, and reading `Server-Timing`. |
| [Deployment](docs/deployment.md) | Environments, configuration/secrets, migrations, deployment workflows, costs and activation gaps. |
| [Operations](docs/operations.md) | Security center, monitoring, retention, recovery, incidents and restore boundaries. |
| [Migration README](infra/postgres/migrations/README.md) | Database migration conventions and operating instructions. Local checks use local PostgreSQL; never create a Neon branch. |
| [Contract README](packages/contracts/README.md) | Shared API/MCP contract boundaries. |
| [Retired workflow boundary](workers/workflows/README.md) | Why durable work belongs in Go rather than a second Cloudflare workflow implementation. |

## Where to inspect implementation

Use the [README layout](README.md#layout) and current source rather than inferring completion from document titles: `apps/frontend/` and `apps/admin/` contain interfaces; `services/backend/` contains Go capabilities, jobs and accounting; `workers/` contains Cloudflare adapters; `infra/postgres/` contains persistence; `packages/contracts/` contains shared shapes; `scripts/` and `.github/workflows/` contain checks and deployment tooling.

For each implementation PR, name the B01–B12 package, update its feature/progress documentation and record the actual validation level. Add new document pointers here; change the governing plan explicitly if scope or gates change. Do not treat a documentation merge as provider activation or permission to send, post or release financial results.
