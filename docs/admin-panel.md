# Admin panel plan

**Status:** **A0 (foundations), A1 (workflows), A2 (firm management) and A3 (plans) implemented** (2026-09-28); A4 (hardening) is a proposal. Workflow screens depend on the [workflow engine](workflow-engine.md); firm-level controls that already exist are described in [operations](operations.md#security-center).

## Purpose

An internal console for **platform staff** (our employees, not firm members) to manage every firm from one place:
- firms and their status and usage;
- people and access;
- activity;
- workflow runs, which staff can start, cancel, retry and approve, and inspect step by step in a flow view;
- staff-built and agent-proposed plans;
- health of connections and the existing background jobs;
- an audit trail of everything staff do.

The firm-facing **Security center** stays as it is. It is a firm administrator's view of their own firm; this console is ours, across all firms.

## Principles

1. **Separate identity.** Staff sign in as members of a dedicated WorkOS organization that is never enrolled as a firm. Firm sessions cannot reach admin routes, and staff sessions cannot use firm routes.
2. **Least privilege, explicit permissions.** Reading, operating and viewing client content are separate permissions.
3. **Metadata by default.** Staff see states, counts, errors and IDs. Client content (documents, extracted text, `wf:"sensitive"` fields, email bodies) requires `platform:content` and a stated reason, and is logged.
4. **Writes go through domain code, under the target firm.** An admin action runs with that firm's tenant context, so row-level security still confines it, and it calls the same Go function the app uses. No raw SQL edits, no "set state to queued".
5. **Accounting invariants still hold.** Staff cannot bypass them:
   - an unknown external outcome is reconciled, never retried;
   - approvals stay bound to their evidence;
   - a signed-off close must be reopened through its normal path.
   See [production posting](production-posting.md) and [operations](operations.md#access-and-incidents).
6. **Everything is audited.** Every staff write records who, what, which firm, why and the result, in the same transaction as the change.

## Identity and access

### WorkOS setup
- **Staff organization:** create "Platform staff" in each WorkOS environment. The API reads its ID from `PLATFORM_ORGANIZATION_ID`.
- **Permissions:**
  - `platform:read`: see all firms' metadata.
  - `platform:operate`: start, cancel and retry runs, signal and approve, suspend or resume a firm, manage invitations.
  - `platform:content`: view client content, with a reason.
  - `platform:plans`: build and approve plans. Approving still requires the plan validator's per-activity permission check.
- **Roles:**
  - Support: `platform:read`.
  - Operator: adds `platform:operate`.
  - Content reviewer: adds `platform:content`.
  - Platform admin: all four.
  Staff are added and removed in the WorkOS dashboard in phase A0; an in-console staff screen comes in A2.
- **MFA:** require it for every staff member, either through WorkOS's MFA setting or through SSO to a company identity provider that enforces it. Confirm which one WorkOS applies per organization before relying on it.
- **Short sessions:** the admin app refreshes tokens, and the API rejects staff tokens whose organization isn't `PLATFORM_ORGANIZATION_ID`.

### API verification
- A staff verifier is `auth.Verifier{ClientID, Issuer, Organization: PLATFORM_ORGANIZATION_ID}`. The pinned organization means a firm's token fails verification on admin routes.
- Firm routes already refuse the staff organization, because it's never registered in `organizations` (the "403 organization not enabled" case in [onboarding](onboarding.md)). A test must prove both directions.
- **Local development:** when WorkOS isn't configured, the existing `WORKSPACE_TOKEN` also grants every platform permission, mirroring today's local-only development access. A configured WorkOS client disables that.

### Network (phase A4)
Move the console to its own hostname (for example `admin.<domain>`) behind Cloudflare Access, or any identity-aware proxy. This limits who can even load it, but it is defence in depth only: authorization stays in Go.

## Architecture

```text
apps/admin/                 separate Vite + React app (own bundle; firm users never load it)
  → built to apps/frontend/dist/admin/ (base "/admin/"), served by the same edge Worker for now
services/backend/internal/transport/admin.go   /v1/admin/* routes, staff middleware, read/act helpers, audit
  (in transport, beside every other route, so it shares the mux, problem replies and tenant database)
```

### Edge Worker
- `/v1/admin/*` is already forwarded to the API container because it's under `/v1/`.
- Add `/admin/*` to `run_worker_first`, serving the asset if it exists and `/admin/index.html` otherwise. The current single-page-app fallback returns the firm app's `index.html`, which is wrong for admin deep links.

### New setting
`PLATFORM_ORGANIZATION_ID` must be added everywhere settings are listed:
- `.env.example`;
- `compose.yaml`;
- the edge Worker's container `envVars`;
- both deploy workflows' configuration checks and secrets files.

### Build
The root `build` script runs the firm app, then the admin app. The admin app empties only its own `dist/admin`.

### Admin-only dependencies
- `@xyflow/react` (React Flow) for graphs. Layout is computed in the page (a column for code workflows, depth from `after` for plans); `elkjs` waits until graphs outgrow that.
- JSON-Schema forms use a small in-house renderer (`components/SchemaForm.tsx`) for the subset `wf.Schema` emits, with a JSON box for anything else.

None of these reach the firm app's bundle.

### Go helpers (`internal/transport/admin.go`)
- `admin.Read(ctx, db, fn)`: marks the context as system work (`*`) and runs `fn` in a `READ ONLY` transaction, so a read-only screen cannot write even by mistake.
- `admin.Act(ctx, db, firm, action, reason, fn)`: runs `fn` with `auth.WithActor` set to the target firm (subject = staff user) and `auth.WithAgent(ctx, "staff:<email>")`, then inserts the `platform_audit` row in the same transaction. The reason is required and non-empty.
- **Content access:** handlers that return client content check `platform:content`, require an `X-Access-Reason` header, and audit the read.

**Existing handlers can't be reused directly.** Many operations live inline in HTTP handlers in `internal/transport`, and they authorize the *signed-in member* (active membership plus WorkOS permissions), which a staff user never has. Each admin action therefore includes separating that operation into a function that takes an explicit authority: the member, or the audited staff actor. This is the bulk of phase A2's work; budget for it per action.

## Data

One admin migration. Both tables enable row-level security so `/readyz` keeps passing.

**`platform_audit`**
- `id`, `staff_user`, `staff_email`, `action`, `organization_id` (the affected firm, nullable), `target_type`, `target_id`, `reason`.
- `request`: redacted JSON of the parameters. `result`: `ok` or `refused`, plus the error.
- `created_at`, `request_id`.
- Append-only: a trigger refuses updates, deletes and truncation for every role, whatever table grants a migration run hands out.
- Policy: `IN ('*', organization_id)`, so a later firm-facing "staff actions on your firm" view is possible.

**`worker_heartbeats`**
- `loop` (primary key), `instance`, `beat_at`, `detail` (JSON: last error, items processed).
- Written by each worker loop, including the engine's, at most once a minute.
- Platform-level: policy `current_setting('app.organization',true) = '*'`.

Workflow tables (`workflow_runs`, `workflow_steps`, `workflow_events`, `workflow_plans`) come from the engine's migration.

## Information architecture

| Area | Screens | Actions (permission) |
|---|---|---|
| **Overview** | Platform health: worker loop heartbeats (stale over 5 minutes), runs by state, oldest waiting run, failed runs and jobs in the last 24 hours, unknown effects, connections needing reconnect, firms suspended. | Links only. |
| **Firms** | List: name, status, created, members, clients, open closes, active and failed runs, last activity, model calls in 30 days, stored evidence size. **Firm detail** tabs: Overview, People, Clients and closes, Workflows, Plans, Jobs, Connections, Activity, Audit. | Suspend or resume with a typed firm ID and reason (`platform:operate`), through the same function as today's `POST /v1/closes/organization/revoke`. |
| **People** | Global search by email or user ID across firms. Per person: app memberships and their decisions, client assignments, WorkOS memberships and roles, pending invitations, agent (MCP) access, recent sessions. | Revoke app membership, resend or revoke an invitation, revoke WorkOS sessions (`platform:operate`, needs `WORKOS_API_KEY` on the API). |
| **Activity** | Per-firm and global timelines, filterable by firm, person, type and date. | None; read-only. |
| **Workflows** | **Catalog**: each workflow's title, description, triggers, input schema, active runs, success rate. **Runs**: filters (firm, workflow, state, started by, date) and saved views ("stuck over 1 hour", "waiting on client over 7 days", "unknown effects", "failed today"). **Run detail**: flow graph, step inspector, events, children. **Start run**: pick a firm and workflow, fill a schema-driven form, see the idempotency key, confirm. | Start, cancel, retry a failed step, send an event, approve or reject a step (`platform:operate`). Resolve an `unknown` effect with a reconciliation reference (`platform:operate`; never re-calls the provider). |
| **Plans** | Builder, templates, and a **proposed plans queue** (agent- and member-sourced) showing rationale, validation result and digest. | Save draft, propose, approve or reject (`platform:plans`, plus the validator's per-activity permissions), instantiate a template for a firm. |
| **Jobs** | Health of the existing job tables until they move to the engine: by kind and firm, queued, running, failed, oldest age, stale claims. Kinds: preparation, email outbox, forwarded files, QuickBooks syncs, Stripe reports, alerts. | Only the recheck or cancel each kind already exposes, through its own function. Never a generic requeue. |
| **Connections** | QuickBooks, Xero, Stripe and email-sender connections across firms, by state and last successful capture. | None in A2; reconnecting stays a firm action. |
| **Audit** | Search staff actions by staff member, firm, action and date; export CSV. | None. |

### Activity sources

**Phase A2 reads the existing records through one paged API:**
- the Security center's "Recent changes" derivation in `internal/transport/security.go`: membership, client and agent access, invitations, connections, posting grants, email-sender changes, and records agents made;
- close goal events;
- preparation job state changes;
- email outbox deliveries;
- workflow steps and events;
- `platform_audit`.

**Later, if needed:** an append-only activity log written by domain code. One migration then, not per feature.

## Workflow flow view

The run detail page is the centre of the console.

### The graph (from `workflow_steps`, in order reached)
- **Nodes:** one per step, with an icon by kind (step, effect, activity, sleep, event wait, approval, child) and a colour by state (running, waiting, completed, failed, unknown, cancelled).
- **Waits** show what they wait for and for how long, for example "client_request.replied:881 · 3 days".
- **Child runs** are collapsible groups that link to their own page.
- **Plan runs** also show nodes not reached yet, dimmed, from the plan's graph. Branches not taken are drawn as "skipped".
- **Loops:** more than 20 steps sharing a key prefix collapse into one counted node.

### Step inspector (side panel)
- Kind, state, attempts with timestamps and errors, duration, and the actor for approvals and signals.
- Input and output JSON, with `wf:"sensitive"` fields masked. **Reveal** requires `platform:content` and a reason.
- Child and event links.

### Actions
- **Header:** Cancel (reason); Retry step on failed steps; Resolve on `unknown` effects; Send event on event waits. Each asks for a reason. Approval waits are decided by the firm, not staff.
- **Controls:** disabled with an explanation when the staff member lacks the permission.

### Behaviour
- **Live updates:** polls every 3 seconds while the run is active and stops when it ends. Server-sent events can replace polling later.
- **Accessibility:** an ordered step list mirrors the graph for keyboard and screen-reader use. Every graph action is also available from the list.
- **Deep links:** `/admin/runs/{id}?step={key}`.

### Plan builder (phase A3)
- A form-based node editor: choose an activity from the searchable catalog, fill its schema form, set `after` and `when`.
- A live, read-only React Flow preview, with inline errors from the server-side validator.
- Drag-and-drop editing on the same graph component is a later improvement, not an MVP requirement.

## API (staff)

All routes under `/v1/admin`. They require a staff token plus the permission listed, and every write needs a `reason` field.

| Route | Permission |
|---|---|
| `GET /overview`, `GET /firms`, `GET /firms/{id}`, `GET /firms/{id}/people`, `GET /firms/{id}/activity`, `GET /firms/{id}/audit` | `platform:read` |
| `POST /firms/{id}/suspend`, `POST /firms/{id}/resume` (typed firm ID), `POST /firms/{id}/members/{userId}/revoke`, `POST /firms/{id}/invitations/{id}/revoke` | `platform:operate` |
| `GET /people?q=` (a WorkOS user ID or an email address) | `platform:read` |
| `POST /firms/{id}/invitations/{id}/resend`, `POST /people/{userId}/sessions/revoke` | `platform:operate` |
| `GET /workflows/definitions`, `GET /workflows/activities`, `GET /workflows/runs`, `GET /workflows/runs/{id}` | `platform:read` |
| `POST /workflows/runs`, `POST /workflows/runs/{id}/cancel`, `…/retry` and `…/resolve` (step `key` in the body), `POST /workflows/events` | `platform:operate` |
| `GET /plans`, `GET /plans/{id}`, `POST /plans` (a draft or template), `POST /plans/{id}` (edit a draft), `POST /plans/validate`, `POST /plans/{id}/propose`, `…/reject`, `…/instantiate` (templates) | `platform:plans` |
| `POST /plans/{id}/approve` (the reviewed digest) | `platform:plans` and `platform:content` |
| `GET /jobs`, `GET /connections`, `GET /audit` | `platform:read` |
| Any response field marked content, when revealed | `platform:content` plus the `X-Access-Reason` header |

## Phases

| Phase | Depends on | Scope | Exit criteria |
|---|---|---|---|
| **A0. Foundations** (implemented) | none | Staff organization, roles and permissions in WorkOS (staging and production); `PLATFORM_ORGANIZATION_ID` wired through every place settings are listed; staff middleware and the `adminRead`/`adminAct` helpers; admin migration (098); `apps/admin` shell with staff sign-in; `/admin` edge routing; read-only Overview (heartbeats), Firms and firm detail. | `internal/transport/admin_test.go`: a firm token gets 401 on `/v1/admin/*` (even claiming a platform permission); a staff token gets 403 on firm routes; `adminRead` refuses writes; `adminAct` runs as the firm and audits success and refusal; the edge serves admin pages (`routes.test.ts`). |
| **A1. Workflows** (implemented) | engine W1 | Catalog, runs list and saved views, run detail flow graph and inspector, start, cancel, retry, event, resolve; schema-driven start form. | On staging, a staff member starts, watches, retries a failed step in, and cancels a run for a synthetic firm; each action appears in `platform_audit`. |
| **A2. Firm management** (implemented) | A0 | Firm detail tabs, People (app and WorkOS), Activity timeline, Jobs health, Connections, suspend and resume, invitations; one extracted domain function per action. | Every action runs under the target firm's context and is audited; content reveal requires permission and reason (tested). |
| **A3. Plans** (implemented) | engine W3 | Builder with live preview, templates, proposed-plans queue, approve and reject. | A staff-built plan is approved and runs; an agent-sourced plan waits in the queue until approved. |
| **A4. Hardening** | A0–A3 | Own hostname behind Cloudflare Access (or equivalent); time-boxed content-access grants instead of per-request reasons; audit export; alerting on stale heartbeats and stuck runs; pagination and performance for large firms. | A security review of the console is recorded in [validation](validation.md). |

**Suggested order across both plans:**
1. Engine W0/W1 alongside admin A0.
2. A1.
3. W2 and A2.
4. W3 and A3.
5. W4 and W5.

## As built (A0)

- **WorkOS.** "Platform staff" is its own organization in each environment (production `org_01M3JTPHHZXPV1MPYTQTZMQ52H`, staging `org_01M3JTPJ7038BZYHDY9M3K4ENK`). The permissions `platform:read`, `platform:operate`, `platform:content` and `platform:plans` are environment-wide, but the roles are scoped to the staff organization, so they cannot be given to a firm member. WorkOS requires organization roles to start with `org-`: `org-platform-support`, `org-platform-operator`, `org-platform-content`, `org-platform-admin`.
- **Settings.** The API reads `PLATFORM_ORGANIZATION_ID`; with WorkOS configured and this unset, every staff route answers 503. The admin app is built with `VITE_PLATFORM_ORGANIZATION_ID` (the same public ID) and signs staff in to that organization. The deploy workflows derive both from the GitHub variable `PLATFORM_ORGANIZATION_ID`, which is already set for production.
- **WorkOS redirect.** Register `https://<app>/admin/` as a redirect URI for the environment's AuthKit application before staff sign in. With `AUTH_SESSION=server` the API holds the staff session in its own cookie; `https://<app>/admin/` is then a sign-out redirect (see [sign-in session](auth-sandbox-staging.md#sign-in-session)).
- **Heartbeats.** Only the worker process (`worker`) and the workflow engine loop (`workflow_engine`) report so far. Adding one per job loop means touching each loop; that comes with A2's jobs screen.
- **One browser, one organization.** With `AUTH_SESSION=client`, AuthKit keeps one session per browser: a person who is both a firm member and staff signs in to one organization at a time, and the console offers to switch. With `AUTH_SESSION=server`, the workspace and the console have separate cookies, so both stay signed in.
- **Local development.** Without WorkOS, the workspace token unlocks the console with every platform permission. Run it with `npm run dev:admin` (port 5174, `/admin/`), or the `admin` Compose service.

## As built (A1)

- **Screens.** Catalog (workflows with 7-day counts, activities), Runs (filters, the views `stuck`, `waiting_on_client`, `unknown_effects` and `failed_today`, paging), Start a run (schema form, reason, a fresh idempotency key per page), and the run page: the flow graph, the ordered step list, the step inspector (deep link `?step=<key>`), cancel, retry, resolve, send event, reveal.
- **Starting runs.** Staff can start a workflow only if it has a manual trigger, and must name a firm when its trigger is `wf.ManualForFirm()`. The start form and catalog follow the same rule. `plan` runs start only when a plan is approved (A3).
- **Actions.** Every write goes through `adminAct`, so it runs as the target firm and is audited with its reason, refusals included. The engine's `CancelTx`, `RetryStepTx` and `ResolveTx` take the caller's transaction for this. Staff cannot send engine events (`wf.` keys), so approvals stay with the firm: the inspector says who must decide instead of offering Approve or Reject.
- **Masking.** Content is masked unless revealed, since a workflow may copy client content anywhere. Numbers, booleans and declared field names show; text, fields tagged `wf:"sensitive"`, and anything a schema does not describe (free-form maps, raw JSON, steps without a schema) are masked or shown only by size. Errors and notes are free text and are masked too. Approvals show their permission and decision but not their subject, prompt or note. An event key shows only its topic, the part before the first `:` (`client_request.replied:••••`), since code often builds the rest from its input; a key with no `:` is masked whole. A plan's title, node inputs, approval text and whole event keys are masked. Names of workflows and steps, states and times show, so code keeps client content out of step keys and event topics. `?reveal=1` needs `platform:content` and an `X-Access-Reason` header and is audited as `content.reveal`. The page reads a reveal once and does not poll it.
- **Live updates.** The run page polls every 3 seconds while the run is active. The engine itself checks for work every 10 seconds when idle, so a change can take that long to show.
- **Not yet.** Loops are not collapsed and child runs are links rather than nested groups; both wait for a workflow that needs them.
- **Local check.** `npm run dev:admin` against a local API and worker; the run page was exercised on a staff-approved plan waiting for an event, and sending that event from the inspector completed the run.

## As built (A2)

- **Firm page tabs.** Overview (counts, last health check, recent runs, suspend or resume), People (members with their decision, client-assignment count and agent access; invitations), Activity (paged, filterable by kind) and Staff audit (paged `platform_audit` for the firm, refusals and reveals included).
- **Shared domain functions.** The firm's own screens and the console call the same code: `setOrganizationEnabled` (suspend and, new, resume), `decideMemberAccess` (a membership decision, removing a revoked member's assistant history, portfolio goals and attention marks), `closeInvitation` and `revokeInWorkOS`. Staff actions run through `adminAct` as the firm, need a reason of 10–2,000 characters like the firm's own screens, and are audited. Suspending or resuming needs the firm's ID typed to confirm. Revoking a member needs the decision staff saw (`previous_id`), so a change made meanwhile is not overwritten.
- **Masking.** People shows user IDs, decisions, invited email addresses and assignment counts. Member reasons and the names of assigned clients are masked until revealed. Activity comes from the Security center's derivation (`activityEvents` in `security.go`, now with a kind per row), plus workflow runs and the last suspension. The firm's own records are masked until revealed; workflow rows show. The firm page shows when and by whom a firm was last suspended; the reason is in its activity. The staff audit shows who did which action to what and whether it went through; reasons, the request's details and a refusal's error are masked until revealed. Reveals use the same `platform:content` gate as the run page, via `adminReveal`, and are audited per firm.
- **Firm detail.** Moved members to People (masked) and dropped run errors from the recent-runs list; the run page masks them.
- **Stuck work.** `GET /v1/admin/jobs` lists what each firm's operations screen shows, across firms: counts by kind (items, firms, oldest), then items oldest first, filtered by kind and firm. It shares `operationsAlerts` (`operations.go`) with the firm screen and the alert worker; one firm or all firms, by the same query. Read-only: firms act on their items from their own screen, and no generic requeue exists.
- **Connections.** `GET /v1/admin/connections` lists QuickBooks, Xero, Stripe and the enabled email sender across firms. Each row has its state, mode, when it was connected and changed, and when it last brought data in (a snapshot, a saved Stripe report, an accepted email). Counts by provider and state come first, and connections needing a reconnect lead the list. Client, company and account names are never selected. Reconnecting stays a firm action.
- **People.** `GET /v1/admin/people?q=` finds one person by WorkOS user ID or email address. It returns their WorkOS account, organization memberships and sessions (state, organization, sign-in method, times; never IP address or browser), and the app's membership decisions and invitations for them in every firm, with no reasons or client names. Without a WorkOS key, or when WorkOS does not answer, the app's records still show and the page says why the rest is missing. Staff can sign a person out everywhere, in two platform-level audit entries that outlive the request. `sessions.revoke` ends the app's own sessions (`auth_sessions`, `AUTH_SESSION=server`) and commits first. `sessions.revoke_workos` then revokes every active WorkOS session on every page of the list, so none can be renewed; it is refused, with what ended, when WorkOS fails part way, and ending again finishes the job. With `AUTH_SESSION=client`, the API checks access tokens locally, so a token a browser already holds keeps working until it expires (minutes); rejecting such tokens at once would need a revocation check on every request. When WorkOS cannot answer an email search, the IDs the app itself recorded for that address (accepted invitations, its sessions) stand in, so memberships still show; the page warns they may include earlier owners of the address. A resend whose answer is lost is judged against WorkOS's copy of the invitation read just before it. When that copy cannot be read either, the outcome is reported as unknown and audited as refused with that reason. The invitation is then kept open at least as long as a resend would, so a stale date cannot expire an invitation WorkOS renewed. They can also resend a pending invitation that has not expired, keeping WorkOS's new expiry. Revoking a membership stays on the firm's People tab, where the decision being replaced shows.
- **Not in A2.** Audit search and export belong to A4.

## As built (A3)

- **Screens.** Plans lists plans newest first, filtered by state, source and firm, or the templates. The proposed queue is state `proposed`, and agent proposals show their agent. A plan's page draws it with the run page's graph (every node not yet reached), lists its steps, and offers the decisions its state allows: propose a draft, reject a draft or proposal, approve and start, or, for a template, copy it into a draft for a firm. An approved plan links to its run, and the run page links back to its plan.
- **Masking.** A plan is client content: its title, node inputs, approval subject and prompt, event keys and an agent's rationale are masked until revealed, like a run's. One function, `maskPlan`, now masks plans on both the plans screens and the run page. What a plan does (node IDs, activities, workflows, order, conditions, durations) always shows, as does its digest. Validation problems quote the plan, so only their number shows until revealed.
- **Approval.** Approving means reading the plan, so it needs `platform:content` as well as `platform:plans`. The server accepts an approval only when that staff member revealed that exact version (the reveal's audit row records its digest) within the last day. The page offers it only once the plan is revealed, and a changed plan is refused. Templates are copied, never proposed, rejected or approved; the engine refuses them. The approval and the run it starts commit with the audit row, as the plan's firm (`wf.ApprovePlanTx`, alongside `ProposePlanTx`, `RejectPlanTx` and `InstantiateTemplateTx`).
- **Staff approve with no firm permissions.** A plan whose activities need a firm permission (`ActivitySpec.Permission`) is not approvable by staff, and the page says which permission it needs. Such a plan waits for the firm to approve it, which comes with the firm-facing plan screens. External activities still need an approval node in the plan, which a firm member decides when the plan reaches it.
- **Builder.** `/admin/plans/new` writes a new draft for a firm, or a template for one firm or for every firm. `/admin/plans/{id}/edit` rewrites a draft or rejected plan staff wrote (never an agent's), and `/admin/plans/new?from={id}` starts from a copy of any plan, which is how staff take up an agent's proposal. Editing or copying reads the plan, so it is revealed first. The form edits steps (activity or workflow with JSON input prefilled from its schema, event wait, timed wait, approval), their order, what each comes after and on which outcome. A live graph preview and the validator's problems update as the plan is written (`POST /plans/validate`, which also says whether staff could approve it). A saved plan must be valid. Each save needs a reason and is audited (`plan.create` with the new plan's ID, `plan.update`), as the firm, through `wf.CreatePlanTx` and `wf.UpdatePlanTx`. Drag-and-drop editing on the graph stays a later improvement.

## Later ideas (not planned)

- Per-firm feature flags and limits (model budget, plan limits).
- Read-only "view as firm" support mode, behind `platform:content` and a time-boxed grant.
- A firm-facing "staff actions on your firm" page from `platform_audit`.
- Usage and billing views.
- In-console staff management.

## Open questions

- **MFA:** whether WorkOS can require MFA for one organization, or only for the whole environment, which would include firm users.
- **Retention:** how long `platform_audit` is kept, and whether firms can see it.
- **Content-reveal policy:** which roles hold `platform:content`, and whether a second person must approve the reveal.
