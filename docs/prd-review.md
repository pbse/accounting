# PRD review and foundation plan

Reviewed the complete supplied v2 PRD: sections 1–20 and appendices A–H. The copied PRD is product input, not authority to contact customers, provision services, transmit documents to models, or make ledger changes. Author of this scaffold: Codex (GPT-6).

## Verdict

The product has a coherent center: prepare supported work, preserve its evidence and accounting method, ask for specific decisions, then execute only authorized changes and verify them. The living workpaper is the right unit for both the reviewer experience and durable agent work. The commercial product is much larger than this repository foundation; a successful synthetic demo does not establish R1 or R2 readiness.

The principal implementation risk is making the React/Go/Cloudflare split into several competing workflow engines. Keep one Go capability layer and one authoritative Postgres job/approval model. Cloudflare handles routing, delivery and resumable orchestration. Frontend, REST, MCP, and agent calls must reach the same authorization and accounting code.

## Full-document assessment

| PRD area | Assessment and implementation consequence |
| --- | --- |
| 1–2: promise, boundaries, release order | Preserve preparation → review → approved execution → verification as distinct results. Foundation work should support the loop without claiming the loop is implemented. |
| 3: competitive and research basis | Differentiation remains a hypothesis. The scaffold cannot validate effort savings, market demand, or third-party API coverage. No new competitor claims were researched for this task. |
| 4: target and accounting scope | Explicit USD, single entity, accrual reference. Cash-basis and specialized accounting cannot inherit these fixture assumptions. |
| 5: living workpapers and procedures | Persist immutable source/procedure versions and dependencies before implementing delta review. Changes propose new versions rather than rewriting approvals or posted history. |
| 6: first session | Start with one entity, period and work type. First useful evidence-linked item is the onboarding outcome. Credentials and upload counts are intermediate states. |
| 7: Work | The primary surface is decisions and specific blockers. No invented percentages, activity feed of model thoughts, or dashboard-first chart grid. |
| 8: close, review, requests | Evidence and proposed effect must be inspectable together. Request approval, accounting approval, posting, and sign-off need separate actions and authority. |
| 9: reference journey | The $12,000 invoice is a reproducible synthetic reference, not accounting advice. Current JSON shows all 12 months and a proposed $11,000 adjustment; it is not a live calculation service. |
| 10: context/evidence | Coverage, entity, source location and revision are required data. Upload quarantine, ambiguity and incomplete source handling precede automatic preparation. |
| 11: reconciliation | Independent statement coverage and opening/closing reconciliation are prerequisites. Native QBO reconciliation remains an unverified capability. No manufactured balancing entry. |
| 12: schedules/exceptions | Deterministic exact arithmetic and approved procedures belong in Go. Defer generic schedule engines, loans and accounting inference until reviewed examples require them. |
| 13: follow-up/review | Replies produce candidate facts, never policy or posting authority. Consolidated requests need secure routing, original-message retention, and bounded reminders. |
| 14: posting/package | Persist intent before external effects. A timeout is unknown, not failed; reconcile before retry. Exports must retain scope and unresolved items. |
| 15: agent execution | A job owns the work; chat is just an input. Typed tools, budgets, checkpoints and cancellation are mandatory when agents are added. Model credentials and ledger credentials remain separated. |
| 16–17: sequence/ownership | The three engineering streams are a staffing plan, not a reason to add three frameworks. Accounting, connector and experience owners remain prerequisites for the next release. |
| 18–19: pilots/metrics | Real-data authority, baseline effort and assistance must be measured. Track active human minutes separately from waiting and model processing. No pilot or savings claims from a fixture. |
| 20: adjacent products | Keep expansion out of this scaffold. Do not introduce multi-currency, consolidation, payments or tax abstractions now. |
| A–B: connectors | Maintain operation-level readiness. Native APIs do bulk ingestion and writes. MCP availability is neither connector readiness nor permission. |
| C: API/MCP | One versioned capability contract, one authorization path, explicit errors and durable job IDs. MCP is an adapter to that layer, not a parallel accounting backend. |
| D: deployment/data | React assets + Worker ingress; Go in portable Containers; Postgres for product state; R2 for artifacts. Go does not consume Worker bindings natively. |
| E: controls/durability | Tenant-scoped inbox/outbox, deduplication and action receipts precede queue enablement. Do not promise exactly-once ledger effects from queue or Workflow delivery. |
| F: evaluation | Add the acceptance cases as their behavior ships, using accountant-reviewed development and held-out examples. Do not create a large passing suite against unimplemented behavior. |
| G–H: gates/sources | The PRD's open gates are real dependencies. Cloudflare runtime assumptions were checked against current official docs for this scaffold; external accounting/provider/compliance claims remain unverified here. |

## Decisions still needed before live development

1. **Identity and firm membership:** identity provider, MFA/session model, entity membership, reviewer separation and revocation semantics. No live routes until these are enforced in Go.
2. **Accounting ownership:** qualified owner for fixtures, policy applicability, rounding, account mapping and approval thresholds. The equal-month reference does not cover partial periods, credits or prior postings.
3. **Evidence/data route:** database region/provider, R2 region constraints, model transmission permission, quarantine/scanning, retention and deletion. Choose based on customer obligations before provisioning.
4. **Durability:** Go persists scoped jobs/steps and transactional outbox; Workflows waits/retries; queues deliver stable job references. Define leases and readback before adding a consumer binding.
5. **Execution:** operation-specific QBO schemas/scopes, idempotency behavior and ambiguous-result lookup. Posting executor gets its own credentials and authority checks when ACT-01 is implemented.
6. **UX validation:** test whether a reviewer can assess an unfamiliar workpaper in this canvas without opening a spreadsheet. Keyboard and screen-reader testing, mobile layout, error recovery and source navigation must remain acceptance requirements.

These are not blockers for directory structure or the synthetic reference. They are blockers for handling real accounting records.

## Scope and acceptance of this change

**Goals:** clear frontend/backend/queue boundaries, a review-first example, easy local boot, Cloudflare-compatible Go packaging, GitHub CI/deployment definitions, and documented shared agent capabilities.

**Non-goals:** a complete accounting product, production infrastructure, real connectors, AI/model calls, customer authentication, real documents, database persistence, accounting calculation services, client sending, ledger posting, MCP protocol implementation, or adjacent products.

**Acceptance:** frontend type-check/build; Go API and worker compile and pass the focused boundary check; queue handoff check passes; deployment definitions describe exact setup and remaining gates; UI visibly identifies synthetic work. Docker and cloud execution must be reported separately from source/build validation.

**Scope not changed:** PRD's ledger-of-record boundary, React/Go/Postgres/Cloudflare choice, accounting scope, professional judgment and approval requirements. Original PRD retained unchanged.

**Minimalism ladder:** Go stdlib HTTP and JSON (rung 3); native HTML/CSS review controls (rung 4); npm workspaces, Docker Compose and Cloudflare routing (rung 4). No custom scheduler, shared UI library, repository abstraction, ORM, Redis, vector database, model framework or speculative database schema (rung 1). Only the queue handoff is small custom transport code (rung 7).

This unborn repository has no origin or base commit; a worktree from updated origin/main cannot exist yet. This first scaffold is local and uncommitted. Cross-model review belongs to an explicitly requested CommitPR gate, not an invented mid-implementation gate.
