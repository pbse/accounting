# Admin panel plan

**Status:** **A0 (foundations), A1 (workflows), A2 (firm management), A3 (plans) and A4 (hardening) implemented** (2026-09-28). Workflow screens depend on the [workflow engine](workflow-engine.md); firm-level controls that already exist are described in [operations](operations.md#security-center).

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

1. **Separate identity.** Staff are Krith's own people: everyone with a verified Google address at `STAFF_EMAIL_DOMAIN` (getkrith.com in production; see [staff by email domain](#staff-by-email-domain)). They sign in to the console with a staff session of its own. Firm sessions cannot reach admin routes, and staff sessions cannot use firm routes.
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

### Staff by email domain
Since 2026-10-06, setting `STAFF_EMAIL_DOMAIN` makes everyone with a verified Google address at that domain staff. It is a GitHub environment variable, `getkrith.com` in production, which the deploy workflows pass to the API. It replaced the "Platform staff" WorkOS organization, retired on 2026-10-07 (see [As built (A0)](#as-built-a0)).
- **Sign-in.** `/v1/auth/login?staff=1` goes straight to Google and names no organization. Staff keep whatever WorkOS organizations they have, or none. A staff member who also belongs to a firm (such as getkrith.com's own) still gets a staff session that is not a firm session.
- **Who is turned away.** Every rule in [sign-in](sign-in.md) applies, and the address must be at the staff domain. Anyone else returns to `/login?error=staff` with no session.
- **Each session says whether it is staff.** A staff session's organization no longer tells the two kinds apart, so `auth_sessions.staff` (migration 128) records it. The staff cookie accepts only a staff row for an address at the domain, and the firm cookie only a firm row. Renewal ends a staff session once its address no longer qualifies. A session in no organization renews without naming one.
- **Only the session counts.** WorkOS access tokens carry no email address. The API takes the staff member's address from their session row, never from the request, so a bearer token alone never opens the console in this mode.
- **Permissions.** Every staff member holds all four platform permissions. Client content still needs a stated reason and is audited, as is every staff write.
- **Removing someone.** Suspending their Google Workspace account stops new sign-ins, but a staff session lasts up to 30 days. To end it at once, also sign them out everywhere from the console's People screen, which ends their sessions here and in WorkOS.
- **Server sessions only.** Staff access needs `AUTH_SESSION=server`. With WorkOS and no `STAFF_EMAIL_DOMAIN`, every staff route answers 503.
- **Two-step verification.** Staff sign in with Google, so turn on 2-Step Verification for the staff domain in the Google Workspace admin console.

### Permissions
- `platform:read`: see all firms' metadata.
- `platform:operate`: start, cancel and retry runs, signal and approve, suspend or resume a firm, manage invitations.
- `platform:content`: view client content, with a reason.
- `platform:plans`: build and approve plans. Approving still requires the plan validator's per-activity permission check.

Every staff member holds all four. Each route still names the permission it needs, on top of `platform:read`, so narrower staff roles could return without revisiting the routes.

### API verification
- A staff verifier accepts a WorkOS token in any organization, or none. What makes a request staff is its session: a row in `auth_sessions` marked staff, for a verified address at `STAFF_EMAIL_DOMAIN`. The API reads that row; a token alone, sent as a bearer token, never opens the console.
- A firm's session cannot reach admin routes, which read only the staff cookie and only staff rows. A staff session cannot use firm routes, which read only the firm cookie and only firm rows. Tests prove both directions.
- **Local development:** when WorkOS isn't configured, the existing `WORKSPACE_TOKEN` also grants every platform permission, mirroring today's local-only development access. A configured WorkOS client disables that.

### Network (phase A4)
Every page of the console lives under `/admin` on the app's own hostname, and only internal staff can load any of it.
- **The check.** The edge Worker serves nothing under `/admin` (pages, scripts, styles) until the API accepts the request's staff session. It asks `GET /v1/admin/me` with the request's cookies. That needs a staff session for a verified address at `STAFF_EMAIL_DOMAIN`.
- **Refusals.** A signed-out visitor to a page goes to staff sign-in and comes back to that page. A file is refused with 401, and a staff member without the console's role gets 403.
- **Sign-in mode.** This needs `AUTH_SESSION=server`, whose cookie the edge can see (see [sign-in session](auth-sandbox-staging.md#sign-in-session)). In any other mode the Worker serves nothing under `/admin`.
- **Authorization.** The gate is defence in depth: every staff API call is still authorized in Go.

A separate hostname behind Cloudflare Access is no longer planned.

## Architecture

```text
apps/admin/                 separate Vite + React app (own bundle; firm users never load it)
  → built to apps/frontend/dist/admin/ (base "/admin/"), served by the same edge Worker for now
services/backend/internal/transport/admin.go   /v1/admin/* routes, staff middleware, read/act helpers, audit
  (in transport, beside every other route, so it shares the mux, problem replies and tenant database)
```

### Edge Worker
- `/v1/admin/*` is already forwarded to the API container because it's under `/v1/`.
- Add `/admin/*` to `run_worker_first`, serving a file as asked and every page from `/admin/`, the console's index page. The current single-page-app fallback returns the firm app's `index.html`, which is wrong for admin deep links. Never ask the assets for `/admin/index.html`: Cloudflare answers that with a 307 to `/admin/`, and the Worker would loop.
- Before serving anything under `/admin`, check the staff session (see [network](#network-phase-a4)).

### New setting
`STAFF_EMAIL_DOMAIN` is listed everywhere settings are:
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
| **Contact notes** | Notes from the public site's contact page, newest first: name, email, firm, note, received ([public site](marketing-site.md#the-contact-form)). Krith's own correspondence, not client content. | Reply by email. |

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
| `GET /jobs`, `GET /connections`, `GET /audit`, `GET /audit/export` (CSV), `GET /contact-notes?before=` | `platform:read` |
| Any response field marked content, when revealed | `platform:content` plus the `X-Access-Reason` header |

## Phases

| Phase | Depends on | Scope | Exit criteria |
|---|---|---|---|
| **A0. Foundations** (implemented) | none | Staff organization, roles and permissions in WorkOS (staging and production); `PLATFORM_ORGANIZATION_ID` wired through every place settings are listed; staff middleware and the `adminRead`/`adminAct` helpers; admin migration (098); `apps/admin` shell with staff sign-in; `/admin` edge routing; read-only Overview (heartbeats), Firms and firm detail. | `internal/transport/admin_test.go`: a firm token gets 401 on `/v1/admin/*` (even claiming a platform permission); a staff token gets 403 on firm routes; `adminRead` refuses writes; `adminAct` runs as the firm and audits success and refusal; the edge serves admin pages (`routes.test.ts`). |
| **A1. Workflows** (implemented) | engine W1 | Catalog, runs list and saved views, run detail flow graph and inspector, start, cancel, retry, event, resolve; schema-driven start form. | On staging, a staff member starts, watches, retries a failed step in, and cancels a run for a synthetic firm; each action appears in `platform_audit`. |
| **A2. Firm management** (implemented) | A0 | Firm detail tabs, People (app and WorkOS), Activity timeline, Jobs health, Connections, suspend and resume, invitations; one extracted domain function per action. | Every action runs under the target firm's context and is audited; content reveal requires permission and reason (tested). |
| **A3. Plans** (implemented) | engine W3 | Builder with live preview, templates, proposed-plans queue, approve and reject. | A staff-built plan is approved and runs; an agent-sourced plan waits in the queue until approved. |
| **A4. Hardening** (implemented) | A0–A3 | `/admin` served only to signed-in platform staff (in place of an own hostname behind Cloudflare Access); time-boxed content-access grants instead of per-request reasons; audit export; alerting on stale heartbeats and stuck runs; pagination and performance for large firms. | A security review of the console is recorded in [validation](validation.md). |

**Suggested order across both plans:**
1. Engine W0/W1 alongside admin A0.
2. A1.
3. W2 and A2.
4. W3 and A3.
5. W4 and W5.

## As built (A0)

- **WorkOS (retired 2026-10-07).** Staff were first the members of a "Platform staff" WorkOS organization, with roles for each permission tier; [staff by email domain](#staff-by-email-domain) replaced it, and the API, the console and the deploys no longer read `PLATFORM_ORGANIZATION_ID`. As it was: "Platform staff" was its own organization in each environment (production `org_01M3JTPHHZXPV1MPYTQTZMQ52H`, staging `org_01M3JTPJ7038BZYHDY9M3K4ENK`). The permissions `platform:read`, `platform:operate`, `platform:content` and `platform:plans` are environment-wide, but the roles are scoped to the staff organization, so they cannot be given to a firm member. WorkOS requires organization roles to start with `org-`: `org-platform-support`, `org-platform-operator`, `org-platform-content`, `org-platform-admin`.
- **WorkOS redirect.** Register `https://<app>/admin/` as a redirect URI for the environment's AuthKit application before staff sign in. With `AUTH_SESSION=server` the API holds the staff session in its own cookie; `https://<app>/admin/` is then a sign-out redirect (see [sign-in session](auth-sandbox-staging.md#sign-in-session)).
- **Heartbeats.** Only the worker process (`worker`) and the workflow engine loop (`workflow_engine`) report so far. Adding one per job loop means touching each loop; that comes with A2's jobs screen.
- **Both stay signed in.** The workspace and the console have separate cookies, so a person who is both a firm member and staff stays signed in to both. With `AUTH_SESSION=client` the console does not sign staff in at all: only the server's session knows their address.
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
- **Builder.** `/admin/plans/new` writes a new draft for a firm, or a template for one firm or for every firm. `/admin/plans/{id}/edit` rewrites a draft or rejected plan staff wrote (never an agent's), and `/admin/plans/new?from={id}` starts from a copy of any plan, which is how staff take up an agent's proposal. Editing or copying reads the plan, so it is revealed first. The form edits steps (activity or workflow with JSON input prefilled from its schema, event wait, timed wait, approval), their order, what each comes after and on which outcome. A live graph preview and the validator's problems update as the plan is written (`POST /plans/validate`, which also says whether staff could approve it). A saved plan must be valid. Steps refer to each other by a key that never changes, so renaming a step never rewires what waits for what. Inputs stay as the JSON text written, and a revealed plan returns each step's input exactly as stored, so integers beyond 2^53 survive editing and copying. Each save needs a reason and is audited (`plan.create` with the new plan's ID, `plan.update`), as the firm, through `wf.CreatePlanTx` and `wf.UpdatePlanTx`. Drag-and-drop editing on the graph stays a later improvement.

## As built (A4, so far)

- **Staff audit.** `/admin/audit` searches every staff action and reveal across firms: by firm (`platform` for entries on no firm), staff member, action or its start (`plan.`, `content.reveal`) and result, newest first, paged by entry ID. The firm page's Staff audit tab uses the same search, and `GET /audit/export` returns it as CSV (at most 5,000 entries; `X-Truncated` says when there are more). Who did which action to what, and whether it went through, always show. Reasons, request details and refusal errors are masked until revealed, in the page and the export. A reveal is recorded, in one transaction, against every firm whose entries it shows, and entries on no firm at platform level, with the search and the entry IDs it covered. In the console a reveal belongs to its search (and a plans list's reveal to its filters): paging keeps it, and other filters are masked until revealed anew. CSV values that start like a formula (`=`, `+`, `-`, `@`, also after leading whitespace), or with whitespace or a control character, are prefixed with an apostrophe, so a spreadsheet never runs a staff member's text.

- **Platform alerts.** A worker loop of its own (`platform_alerts`, supervised like the others, so a backlog of firm alerts never delays it) watches the platform itself every minute: a heartbeat stopped for 5 minutes (the worker process and the workflow engine report one; the other job loops' stalls show as their stuck items, under Stuck work and in the firms' alerts), workflow runs stuck (the stuck view's rule, shared as `stuckRuns`), and provider writes of unknown outcome. Each opens one alert in `platform_alerts` (migration 100), sent once to the operations webhook and resolved when the problem clears (see [operations](operations.md)). A send the worker stopped before the webhook answered becomes unconfirmed, not stuck sending. The Overview lists the problems now, live, with when each was alerted and whether the webhook took it. A worker that is down cannot report itself, so an uptime monitor on the API, or someone watching the Overview, remains the check for that.

- **Content grants.** A staff member with `platform:content` can open a grant for one firm, for 15 minutes to 8 hours, with a reason (`POST /firms/{id}/grant`, audited as `content.grant`; migration 101). Opening another grant for the same firm replaces it. While a grant lasts, that firm's screens (its tabs, its runs, its plans, including editing or copying one, and the audit search or plans list filtered to that firm) reveal without asking. Each reveal is still recorded, with the reason `Under content grant #N: <reason>`. A request that can span firms (an unfiltered search, people) still asks every time; whether a grant applies depends on the request's scope, never on which firms one page of results holds. The sidebar lists open grants, each with a way to end it early (`content.grant_end`). A grant that ends or expires takes back what it showed (the plan builder closes, unsaved changes included, and warns of this while open), and hiding content while holding a grant keeps it hidden on that screen until shown again. The API honours a grant only for its holder and its firm, and only until it expires.

- **Security review.** Recorded in [validation](validation.md#staff-console-security-review-2026-09-28). The edge Worker serves `/admin` with a script-only-from-self, never-framed policy. A request carrying the staff cookie from another site is refused. Reveal reasons are capped at 2,000 bytes, and audit reads at 32 MiB. A WorkOS session revoke or invitation resend records a `started` entry (migration 102) before calling WorkOS, and its outcome after, and holds no transaction while WorkOS works. `TestEveryStaffRouteRefusesFirmsAndStaffWithoutRead` covers every staff route as it is added.

- **Large firms.** Measured against a local firm with 208,000 runs, 605,000 steps, 300,000 evidence files and 300,000 audit entries, plus 3,000 small firms. Every staff read answers in under 100 ms. To get there:
  - **Run page.** It shows a long run's steps 200 at a time, latest first, with how many there are in all. Earlier and Later page through the rest (`?steps_before`, a step's seq). It also shows the newest 200 child runs, with a link to all of them in the runs list, which now filters by parent run (`?parent`). A run with 5,000 of each went from 1.8 MB to 71 KB.
  - **Firms list.** It picks its page before counting. Counting in the same query had counted every firm the offset skipped, so page 30 took 330 ms; it now takes 45.
  - **Indexes** (migration 103). Workflow runs are indexed by parent run, by firm and state, and by firm and last update. A firm's run counts and latest activity no longer read all its runs (the big firm's row: 132 ms to 10 ms), and a run's children no longer need a scan of every run on each refresh.
  - **Other lists.** Firms, runs, plans, activity and audit all page with a next link, and each page reads at most 100 rows.

- **Agent proposals** (engine W4). The Plans page can **Ask the planner**: staff describe what a firm needs, and the firm's model drafts a plan from the catalog, saved as the agent's proposal in the queue (for firms that turned model-written answers on; each attempt counts against the firm's model calls; audited as `plan.draft_with_model`). Connected agents propose through MCP (`propose_plan`). **Agents' proposals** shows each agent's 30-day record: proposed, waiting, approved, rejected, and the planner's drafts that were invalid or unanswered.
- **Staff-only `/admin`.** The edge Worker serves the console's pages and files only after the API accepts the staff session (`GET /v1/admin/me`); signed-out visitors go to staff sign-in. It needs `AUTH_SESSION=server`. See [network](#network-phase-a4).

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
