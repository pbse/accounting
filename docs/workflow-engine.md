# Workflow engine plan

**Status:** W0 decided (see [the spike](#w0-result-dbos-transact-for-go)); **W1 (engine core) implemented** in `services/backend/internal/wf`, migration 092 and `services/backend/workflows`. Later phases are proposals. The engine supersedes the Cloudflare Workflows boundary once reserved in [`workers/workflows`](../workers/workflows/README.md). The staff UI for it is planned in [admin panel](admin-panel.md).

## Goals

- Many predefined workflows, each written as Go code in its own directory. **A new directory is a new workflow: no database migration, no registration edit.**
- Open-ended, client-specific workflows created at runtime as **plans**: built by platform staff now, proposed by the agent later. The agent path is low priority, but the data model, validation and approval rules support it from the start.
- State lives in our PostgreSQL, next to the accounting records it coordinates. No Redis, Cloudflare Workflows or Cloudflare Queues: the engine runs wherever the Go worker runs (Compose locally, Cloudflare Containers today, any container host later). The per-minute cron stays only as a wake-up for the container.
- Every run, step, wait and error is recorded in a form the admin panel can draw as a flow and act on (start, cancel, retry, signal, approve).

## Non-goals

- A general-purpose workflow product or visual programming language. Steps are Go code, and plans compose only vetted activities.
- Rewriting the existing job loops up front. The 13 loops in `cmd/worker` keep working; they move onto the engine one at a time, when that pays off (phase W5).
- Changing how accounting rules are enforced. Activities call the existing domain functions; approvals, posting grants and review rules stay where they are.

## Concepts

| Term | Meaning |
|---|---|
| **Workflow** | A named, versioned Go function registered from `services/backend/workflows/<name>/`. |
| **Run** | One execution of a workflow for one firm (or for the platform), with its input, state and history. |
| **Step** | A named unit inside a run whose result is saved. On resume, finished steps return their saved result instead of running again. |
| **Activity** | A vetted building block in `services/backend/activities/<name>/` (for example `client_request.send`), with an input schema, a required permission and an effect class. Code workflows call activities; plans consist of them. |
| **Plan** | A JSON graph of activities, waits, approvals and conditions, stored as data and executed by the built-in `plan` workflow. Created by staff or proposed by the agent. |
| **Event** | A recorded signal (a client reply, an approval, a close signed off) that wakes runs waiting for it. |
| **Trigger** | What starts a run: manual/API, a schedule, or an event. |

## Layout

```text
services/backend/internal/wf/        engine: definitions, steps, waits, retries, claim loop, registry
services/backend/internal/wf/wftest/ test harness on local PostgreSQL
services/backend/workflows/<name>/   one directory per workflow (package with a wf.Define)
services/backend/workflows/plan/     built-in interpreter for plans
services/backend/activities/<name>/  one directory per activity (package with a wf.DefineActivity)
services/backend/internal/wf/gen/    `go generate` tool that writes the registry imports
```

Go only runs a package's registration if something imports it. `go generate ./workflows` (the generator is `internal/wf/gen`) scans `workflows/*/` and `activities/*/` and writes `workflows/registry_gen.go` with one blank import per directory; `cmd/worker` imports the `workflows` package. The generated file is committed, and CI regenerates it and fails on any difference. The file lives in `workflows`, not `internal/wf`, because workflows import the engine. `workflows/registry_test.go` fails unless every directory registers exactly the workflow named after it. So adding a directory, then running `go generate ./workflows`, is the whole registration step.

## Authoring a workflow

```go
// services/backend/workflows/month_kickoff/workflow.go
package month_kickoff

type Input struct {
	CloseID string `json:"close_id" jsonschema:"title=Signed-off close"`
}

type Output struct {
	NextCloseID string `json:"next_close_id"`
}

var _ = wf.Define(wf.Spec[Input, Output]{
	Name:        "month_kickoff",
	Title:       "Start next month's close",
	Description: "Creates next month's close, carries forward schedules and asks the client for documents.",
	Triggers:    []wf.Trigger{wf.Manual(), wf.OnEvent("close.signed_off")},
	Concurrency: func(in Input) string { return "close:" + in.CloseID },
	Run: func(ctx wf.Context, in Input) (Output, error) {
		next, err := wf.Step(ctx, "create-close", func(c context.Context) (string, error) {
			return closes.CreateNext(c, in.CloseID)
		})
		if err != nil {
			return Output{}, err
		}
		request, err := wf.Call(ctx, "draft-request", clientrequest.Draft, clientrequest.DraftInput{CloseID: next})
		if err != nil {
			return Output{}, err
		}
		if err := wf.WaitForApproval(ctx, "approve-request", wf.Approval{Permission: "requests:send", Subject: request.ID, Timeout: 7 * 24 * time.Hour}); err != nil {
			return Output{}, err
		}
		if _, err := wf.Call(ctx, "send-request", clientrequest.Send, clientrequest.SendInput{RequestID: request.ID}); err != nil {
			return Output{}, err
		}
		reply, err := wf.WaitForEvent[clientrequest.Reply](ctx, "client-reply", "client_request.replied:"+request.ID, 14*24*time.Hour)
		if errors.Is(err, wf.ErrTimeout) {
			_, err = wf.Call(ctx, "remind", clientrequest.Remind, clientrequest.RemindInput{RequestID: request.ID})
		}
		...
	},
})
```

### Primitives

| Primitive | Behaviour |
|---|---|
| `wf.Step(ctx, key, fn, options…)` | Runs `fn` once and saves its JSON result under `key`. Retries per the step's policy. At-least-once: `fn` must be safe to repeat. |
| `wf.Effect(ctx, key, fn)` | For external writes (ledger, email, provider calls). Records an attempt before calling `fn`. If the worker dies mid-call, the step becomes `unknown` and the run waits for a person; it is **never** called again automatically. Mirrors [production posting](production-posting.md). |
| `wf.Call(ctx, key, activity, input)` (W3) | Runs an activity as a step (or an effect, per the activity's class). |
| `wf.Sleep(ctx, key, d)` / `wf.SleepUntil` | Parks the run until a time. |
| `wf.WaitForEvent[T](ctx, key, eventKey, timeout)` | Parks until a matching event (see [events](#events)) or the timeout (`wf.ErrTimeout`). |
| `wf.WaitForApproval(ctx, key, approval)` (W2) | A wait whose event must come from a person holding the named permission in the run's firm. |
| `wf.Child(ctx, key, workflow, input)` (W2) | Starts a child run and waits for its result. |
| `wf.Now(ctx)` / `wf.Random(ctx)` | Saved values, so the run replays identically. |
| `wf.Permanent(err)` | Marks an error as not retryable. |

### Rules for workflow code

1. **Code outside steps must be deterministic.** No `time.Now()`, randomness, database reads or network calls outside steps; use `wf.Now`, `wf.Random` or a step. A replay must reach the same step keys in the same order. A `go vet` analyzer in `internal/wf/gen` flags the common violations.
2. **Step keys are the contract.** Changing what a step means requires a new key. Adding or removing steps is safe: new keys run, unused saved results are ignored. Keys built at runtime (`"remind-"+docID`) are allowed and are how loops and open-ended flows work.
3. **Steps are at-least-once.** Internal writes use idempotent upserts or the step's `wf.StepID(ctx)` as an idempotency key. External writes use `wf.Effect`.
4. **Pass IDs, not documents.** Inputs, outputs and step results are small JSON (a size cap, 64 KiB, is enforced). Evidence bytes stay in their own tables.
5. **Fields that hold client content carry `wf:"sensitive"`** so the admin panel redacts them by default.

## Data model

One migration creates the engine's tables. After it, adding workflows or activities never needs a migration. Every table enables row-level security with the same policy shape as migration 047, so hosted `/readyz` keeps passing: `USING (current_setting('app.organization',true) IN ('*', organization_id))`. Rows with a `NULL` firm (platform runs) are visible only to system work.

**`workflow_runs`**
- Identity: `id`, `organization_id` (nullable for platform runs), `workflow`, `code_version` (the git SHA at start), `plan_id`, `parent_run_id` and `parent_step` for child runs.
- Deduplication and serialisation: `idempotency_key`, unique per firm and workflow; `concurrency_key`, with a partial unique index over active runs so one close is not worked on by two runs at once.
- State: `state` (`queued`, `running`, `waiting`, `completed`, `failed`, `cancelled`), `input`, `output`, `error`, `wake_at`, `waiting_on`.
- Lease: `claim`, `claimed_at`, `attempts`.
- Cancel and attribution: `cancel_requested_at`, `cancel_reason`, `started_by` (user, schedule, event or staff), `agent` (the same attribution as `auth.WithAgent`).
- Times: `created_at`, `started_at`, `finished_at`, `updated_at`.
- Index: `(state, wake_at)` for claiming.

**`workflow_steps`**
- Primary key `(run_id, key)`, plus `organization_id`, `seq` (order first reached, used for drawing).
- `kind` (`step`, `effect`, `activity`, `sleep`, `event`, `approval`, `child`, `value`).
- `state` (`running`, `waiting`, `completed`, `failed`, `unknown`, `cancelled`), `attempts`, `retry_at`.
- `input`, `output`, `error`, `waiting_on`, `child_run_id`, `actor` (who approved or signalled), `started_at`, `finished_at`.

**`workflow_events`**
- `id`, `organization_id`, `key`, `payload`, `actor`, `created_at`. Append-only.
- `wf.Emit(ctx, tx, key, payload)` is called inside the domain transaction that caused it: a client reply, a sign-off, a released attachment. The event commits with the change and wakes any matching waits in the same transaction.

**`workflow_plans`**
- `id`, `organization_id`, `title`, `plan` (JSON), `digest`.
- `state` (`draft`, `proposed`, `approved`, `rejected`, `archived`), `source` (`staff`, `agent`, `member`), `rationale`.
- `created_by`, `approved_by`, `approved_at`, `is_template`, `template_id`, `run_id`, `created_at`.

## Execution

- **Claiming.** A `wf.Run(ctx, db)` loop in `cmd/worker` claims up to N runnable runs: `queued`, `waiting` with `wake_at <= now()`, or `running` with an expired lease. It uses `FOR UPDATE SKIP LOCKED` and a claim token, the same pattern as today's `email_outbox` and `quickbooks_entity_syncs` loops, so any number of workers can run safely. Concurrency comes from `WF_CONCURRENCY` (default 4); the worker's connection pool grows to match (it is 2 today).
- **Tenant scope.** Each run executes with `auth.WithActor(ctx, &auth.Claims{Organization: run.organization_id, …})` and `auth.WithAgent(ctx, "workflow:<name>#<run>")`, so `OpenTenantDB` sets `app.organization` to that firm and row-level security confines every query the steps make. **This is mandatory:** the worker's database fallback is `*` (every firm), and the engine must never run a firm's steps under it. Platform runs (`NULL` firm) run as system work and may not call firm activities.
- **Replay.** The engine calls the workflow function from the top. Each primitive looks up its key in `workflow_steps`: a completed step returns its saved result; a new step is recorded as `running` (committed), then executed, and its result committed. Sleeps and waits record `waiting`, set the run's `wake_at` or `waiting_on`, and return a suspend signal that unwinds the function. The run is released until it's woken.
- **Leases.** A claim is extended while a step runs. After a crash, the lease expires (2 minutes) and another worker replays the run. A step left `running` is retried if it's a `step`, or marked `unknown` if it's an `effect`.
- **Retries.** Per-step policy: attempts, exponential backoff and timeout. Default: 3 attempts, 30 seconds doubling. When attempts are exhausted the run fails, and the failed step stays visible and retryable from the admin panel.
- **Cancellation.** Sets `cancel_requested_at`. The engine checks it between steps and cancels the running step's context. The run ends `cancelled`; child runs are cancelled too. Optional compensation hooks come in W2.
- **Wake-up latency.** The loop polls every 2 seconds while there is work and every 10 seconds when idle. Events and starts set `wake_at = now()`. `LISTEN/NOTIFY` on the direct endpoint can replace polling later without changing the model.
- **Versions.** The deploy workflows write the git commit to `internal/wf/version.txt` before building (it reads `dev` otherwise), and `Start` stamps it on each run as `code_version`. A run keeps replaying on newer code under the step-key rule above. Retiring a workflow means removing its triggers first and deleting the directory once no run is active; the admin panel shows active runs per workflow.

## Events

- `wf.Emit` inside domain transactions for facts other flows care about: `client_request.replied:<id>`, `close.signed_off:<close>`, `evidence.released:<id>`, `approval.granted:<run>/<step>`.
- A wait is satisfied by the earliest matching event recorded **after the run started** that no earlier wait in the same run consumed. This covers late signals, where the reply arrives before the run reaches the wait. A step can ask for "after this step" instead.
- An event recorded after a wait's deadline does not satisfy it. An event whose payload does not fit the wait's type is passed over and listed on the step (`input.skipped`), so a malformed signal neither fails the run nor keeps waking it.
- **Cancelling wins.** Whenever the engine parks, releases or finishes a run, it applies a pending cancel request in the same statement, so a cancel sent while a run executes always ends it (completed steps keep their results; an unknown effect stays unknown).
- **Event triggers** start runs from `wf.OnEvent(pattern)`. Each start's idempotency key is the event ID, so replaying an event never starts two runs.
- **Schedules** (`wf.Schedule(cron)`) are evaluated by the engine loop. Each slot has the idempotency key `<workflow>@<slot>`, so a restarted worker cannot double-start one. Schedules can be firm-scoped (for each enabled firm) or platform-wide.

## Plans: staff-built and agent-proposed

### Activities

An activity is a Go package that registers `wf.DefineActivity`:
- `Name` (`client_request.send`), `Title`, and a plain-language `Description`, written to be read by staff and by a model.
- `Input` and `Output` types. A JSON Schema is generated from them with a schema library such as `invopop/jsonschema`.
- `Permission`: what the approving person must hold, for example `requests:send`.
- `Effect`: `read`, `internal` or `external`.
- `Run`: always a call into existing domain code (`internal/preparation`, `internal/delivery` and so on), never a new copy of accounting logic.

The catalog (`GET /v1/admin/workflows/activities`) returns all of this. It is the same shape an LLM tool list needs, which is the first bone for agent proposals.

### Plan format (version 1)

```json
{
  "version": 1,
  "title": "Collect missing bank statements for Acme, September",
  "nodes": [
    {"id": "ask", "activity": "client_request.send", "input": {"close_id": "412", "questions": ["September bank statement for account 1234"]}},
    {"id": "reply", "after": ["ask"], "wait": {"event": "client_request.replied:{{ask.request_id}}", "timeout": "P14D"}},
    {"id": "remind", "after": ["reply"], "when": "reply.timed_out", "activity": "client_request.remind", "input": {"request_id": "{{ask.request_id}}"}},
    {"id": "prepare", "after": ["reply"], "when": "reply.received", "activity": "preparation.start", "input": {"close_id": "412"}},
    {"id": "review", "after": ["prepare"], "approval": {"permission": "workpapers:review", "subject": "{{prepare.job_id}}"}}
  ]
}
```

- Nodes form a directed acyclic graph through `after`. Each node is exactly one of `activity`, `wait` (event or duration), `approval` or `workflow` (a child run of a code workflow).
- `{{node.field}}` references earlier outputs. `when` takes only node outcomes: `received`, `timed_out`, `completed`, `failed`, `approved`, `rejected`. There is no general expression language; anything more complex should become a code workflow.
- Limits: at most 50 nodes and a 90-day horizon.

### Validation (identical for every source)

A plan is accepted only if:
1. Every activity exists and every input validates against its schema.
2. References resolve to earlier nodes, and the graph has no cycles.
3. It stays within the limits.
4. The approving person holds every activity's permission in that firm.
5. Every `external` activity is preceded by an `approval` node unless the approver holds that activity's permission and explicitly waives it.

Validation runs on save, on approval and again when the run starts.

### Lifecycle

`draft` → `proposed` → `approved` → running as a `plan` run.
- Approving records the approver and binds to the plan's `digest`. Any edit returns the plan to `draft`, the same digest-bound approval the repo uses for postings.
- The `plan` workflow receives `{plan_id, digest}`, reloads the approved snapshot, and executes each node as a step keyed by node ID. So a plan run has the same history, retries and flow view as a code workflow.
- Templates are plans with `is_template`. Instantiating one copies it into a new `draft` for a specific firm and close.

### Agent-proposed plans: bones now, planner later

Built in W3 so nothing needs redesign later:
- `source = agent`, `rationale` and `agent` attribution columns.
- An agent plan can only ever be created in `proposed`. **There is no code path that runs an agent plan without a person's approval.**
- The activity catalog is model-ready: descriptions plus JSON Schemas.
- Proposals are rate-limited per firm, and the validator treats agent and staff plans identically.

Deferred (W4, low priority):
- The planner itself: a prompt over the catalog and the close's state, using the existing OpenAI integration and its budget accounting.
- A `propose_plan` tool on the [MCP server](mcp.md).
- Evaluation of proposal quality.

## API

W1 provides these as Go functions (`wf.Start`, `wf.Cancel`, `wf.Emit`, `wf.RetryStep`, `wf.Resolve`); their HTTP routes arrive with the admin panel's staff authentication (phases A0 and A1). Staff routes live under `/v1/admin/workflows/*` (see [admin panel](admin-panel.md)). Firm-facing routes under `/v1/workflows/*`, scoped to the signed-in firm by row-level security, come later with the firm UI.

| Operation | Route |
|---|---|
| Catalog of workflows and activities | `GET …/definitions`, `GET …/activities` |
| Start a run (idempotency key required) | `POST …/runs` |
| List and filter runs | `GET …/runs?firm=&workflow=&state=&started_by=&since=` |
| Run detail: run, steps, events, children | `GET …/runs/{id}` |
| Cancel with a reason | `POST …/runs/{id}/cancel` |
| Retry a failed step | `POST …/runs/{id}/steps/{key}/retry` |
| Resolve an `unknown` effect after reconciliation (records who and the evidence reference; never re-calls) | `POST …/runs/{id}/steps/{key}/resolve` |
| Send an event or approval | `POST …/events` |
| Plans: create, edit, propose, approve, reject, instantiate a template | `…/plans…` |

## Testing

Per [AGENTS.md](../AGENTS.md), tests run against local PostgreSQL (`TEST_DATABASE_URL`). `wftest` provides:
- a controllable clock;
- a way to kill a run at any step boundary or mid-step, then replay it;
- helpers to emit events and assert the step history.

These proofs, which [`workers/workflows`](../workers/workflows/README.md) already demands, are required W1 tests:

1. Interruption and resume: a crash mid-step replays without repeating completed steps.
2. A late signal is consumed correctly.
3. Cancellation while running and while waiting.
4. A changed source: a new evidence event while a run waits.
5. Budget limits: attempts and timeouts, and plan size limits.
6. Duplicate delivery: the same event twice, and the same start twice.
7. A crash during `wf.Effect` yields `unknown` and no second call.
8. A step for firm A cannot read firm B's rows under the runtime role.

## Phases

| Phase | Scope | Exit criteria |
|---|---|---|
| **W0. Spike (1–2 days)** | Try DBOS Transact for Go (open-source durable workflows on PostgreSQL) against this plan: per-connection tenant context with row-level security, a non-superuser runtime role, the proof list. | Go/no-go recorded here. **Go:** the engine wraps DBOS behind the `wf` API. **No-go:** build W1 as specified. |
| **W1. Engine core** (implemented) | Migration 092; `internal/wf` (`Define`, `Step`, `Effect`, `Sleep`, `SleepUntil`, `WaitForEvent`, `Now`, `Random`, `Permanent`, `NotApplied`); `Start`, `Cancel`, `Emit`, `RetryStep`, `Resolve` as Go functions; concurrency keys; registry generator and CI check; claim loop in `cmd/worker`; version stamping. The first real workflow moved to W2, which brings the event triggers and approvals it needs. | Proofs 1–8 pass in CI (`internal/wf/engine_test.go`); a run survives a container restart in staging. |
| **W2. Composition** | `Child`, `WaitForApproval`, schedule and event triggers, the first real workflow (`month_kickoff`), `wf.Emit` from the first domain transactions (client reply, sign-off), compensation hooks. | `month_kickoff` runs end to end on a synthetic close in staging. |
| **W3. Activities and plans** | Activities directory and generator, catalog API, plan format and validator, `plan` workflow, plan lifecycle and templates, agent bones (`proposed` only, attribution, rate limit). | Staff build, approve and run a plan from the admin panel; an agent-sourced plan can't run without approval (tested). |
| **W4. Agent planner (low priority)** | Model-backed proposals over the catalog; MCP `propose_plan`. | Proposals appear in the admin approval queue with rationale. |
| **W5. Migrate existing loops** | Close goals first (already step-shaped, with an events log), then others where it simplifies. | Each migrated loop is deleted; its UI reads engine runs. |

## W0 result: DBOS Transact for Go

**No-go (2026-09-27).** Checked against the source of `github.com/dbos-inc/dbos-transact-golang` v1.4.0 (MIT, pgx v5, Go 1.25+):

- **Its own schema and migrations.** At launch it creates its own schema and applies over a hundred embedded migrations. That is a second migration history beside `infra/postgres/migrations`, and it needs privileges the runtime role deliberately lacks.
- **Recovery is per executor.** A process recovers the pending workflows of its own executor ID (default `local`). Taking over a dead container's workflows across several instances is what DBOS Conductor, a hosted service, provides; our leases do it in Postgres.
- **Runs are pinned to an application version.** By default that is a hash of the binary, so after a deploy, in-flight runs are not recovered unless the version is pinned or patching is enabled. We deploy often and want runs to replay on newer code under the step-key rule.
- **What we need most would still be ours to build:** firm-scoped execution under row-level security, `unknown` effects that never repeat, event matching after run start, plans, and the admin panel's step history.

## Operations

- Apply migration 092 before deploying a build that includes the engine. The deploy workflows check for the workflow tables and stop otherwise.
- `WF_CONCURRENCY` sets how many runs a worker executes at once (default 4).
- A run for a workflow the running build does not register (for example during a rolling deploy) is handed back every minute, with that reason in its error, until a build that knows it picks it up.

## Risks and open questions

- **The determinism rule is the main new discipline.** Mitigations: the vet analyzer, replay tests in `wftest`, and code review.
- **Replay cost grows with step count.** Each wake replays from the top, so a run with thousands of steps should be split into child runs. The admin panel groups repeated keys.
- **Database load.** Polling adds queries to an already always-on Neon compute (see [deployment](deployment.md#provider-choice-and-cost-boundary)). The Free plan's compute hours will not cover continuous operation.
- **Schema generation and cron parsing** each need one small dependency; choose them in W1.
- **Firm-facing visibility** (showing a firm its own runs) needs product decisions about wording and permissions. Not covered here.
