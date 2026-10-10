# LLM gateway and context machine

All ten model request paths go through `services/backend/internal/llm`: evidence selection, preparation, procedure drafting, statement extraction, work commands, workflow planning, written answers, investigation rounds and the agent chat loop (`chat`, reserved by the [agentic chat plan](agent-chat.md) and routed like the others), plus PDF image reading (`ocr`). Feature code builds a request and validates the result. The library owns HTTP, credentials on the wire, provider response parsing, streaming, refusals, timeouts and response limits.

The library includes an OpenAI adapter and a context-machine client. The hosted runtime installs Codex and Claude Code in a private Cloudflare Container. See [deployment instructions](context-machine-deployment.md) for required secrets, database grants, CI and rollout. All accounting purposes can select direct API or context-machine completion by configuration; research is available to authorized server workflows through the library, with no new public research endpoint.

## Routing and ownership

Features call only `llm.Generate(ctx, request)`. They do not read provider environment variables, construct a client, choose an endpoint, pass credentials, or format provider-specific messages. The API composition root (`ConfiguredAPI`) and worker `main` load `llm.FromEnv()` once, validate configuration, and inject the resulting immutable client into HTTP request and background execution contexts. Context injection preserves cancellation and the existing authentication/tenant values; no package-global singleton or lazy environment loading is involved.

A request declares its purpose, instructions/data, output schema or typed conversation/tool messages, and execution limits. The gateway maps its purpose to the configured model and adapter. Unknown or disabled purposes return `ErrUnavailable`; there is no fallback that could send a new workflow's data to an unintended provider. Callers may use `llm.Model(ctx, purpose)` for availability checks and configured-model provenance. Responses also include the actual reported model, provider, run ID and usage.

`LLM_PROVIDER=api` (the compatibility default) routes accounting text to the direct OpenAI adapter. PDF images use the configured paid Gemini adapter, or explicit `LLM_API_IMAGE_ENABLED=true` selects OpenAI image reading instead. A text API key alone enables no image route. `LLM_PROVIDER=context_machine` routes all ten to private `/v1/complete`, using the CLI selected by `CONTEXT_MACHINE_MODEL`. No feature changes are required to switch. Research always uses `/v1/run`. Selection is fixed at startup; failures never cause fallback. Model proposals persist `engine: context_machine` on that route and retain every confirmation, source freshness, straight-through eligibility and replay check used for API proposals. Historical `openai` proposals keep those checks too. Replay model identity includes the context-machine agent/transport so switching routes triggers a fresh evaluation.

```go
// Inside an authorized workflow, with the startup dependency already attached.
// Reserve four model calls from the firm's budget before dispatch.
result, err := llm.Generate(ctx, llm.Request{
    Purpose: "research",
    Instructions: "Investigate why these balances differ. Cite your evidence.",
    Data: authorizedContext,
    Scope: &llm.Scope{
        Organization: organizationID,
        Subject: memberID,
        AccessProfile: provisionedReadOnlyProfile,
    },
    MaxModelCalls: 4,
    MaxOutputTokens: 4000,
    OnProgress: reportProgress, // optional func(string) bool
    OnText: reportProvisionalText, // optional func(string) bool
})
// Validate the completed result and its citations; record provenance and usage.
```

### Deployment configuration

| Setting | Meaning |
| --- | --- |
| `LLM_API_IMAGE_ENABLED` | Explicit opt-in (`true`, default `false`) to sending whole, unredacted PDF images to OpenAI in API mode. Does not enable credentials or text inference. Gemini uses its existing paid-service acknowledgment; context-machine images follow the selected private route. |
| `LLM_PROVIDER` | `api` or `context_machine`; applies to all ten accounting purposes. |
| `OPENAI_API_KEY`, `OPENAI_MODEL` | Direct API credentials/model. Neither is needed in the main services when routing completions through the context machine. |
| `GEMINI_API_KEY`, `GEMINI_OCR_MODEL` | The API-mode `ocr` route uses Google's Gemini when its paid-service key is configured and OpenAI image reading has not been explicitly selected. In context-machine mode the same gateway purpose sends page images through Codex or Claude; Gemini credentials cannot bypass the provider switch. `GEMINI_PAID_SERVICE=confirmed` is also required: the credential script installs it with the key, by `--paid-service`, the operator's confirmation that the key's Google project is on the paid service. Its calls still count against the firm's daily model-call limit ([PDF reading](recalculation-and-pdf.md)). |
| `LLM_ALLOW_UNCAPPED_OUTPUT_TOKENS` | Explicit `true` acknowledgement required for Codex subscription completions; API-key and Claude modes retain token caps. |
| `CONTEXT_MACHINE_PROFILES` | Authorized organization/subject/profile triples; every completion must match a trusted HTTP or job identity. A subject of `*` names every current member of the organization: the gateway sends the member's own subject under that profile, and the service checks that member's membership and client grants on every call. Wide profiles need API-key agents ([deployment](context-machine-deployment.md#organization-wide-profiles)). |
| `LLM_MODELS` | Optional JSON object of purpose-specific model overrides, for example `{"preparation":"careful-model","work_command":"fast-model"}`. Overrides apply to the selected route; unknown purposes and empty models fail startup. |
| `CONTEXT_MACHINE_TOKEN`, `CONTEXT_MACHINE_MODEL` | Enable the research route; both or neither must be set. The value is `codex` or `claude`; the service selects the actual model. |
| `CONTEXT_MACHINE_ENDPOINT` | Optional local/private infrastructure override. Cloudflare sets the fixed bridge `http://context-machine.internal/v1/run`; callers never provide a URL. |

Compose and the Cloudflare API/worker container launchers forward these settings. Changes require restarting the process; a running gateway does not observe mutated environment variables. Nothing is exposed to the browser.

Cloudflare's container outbound handler intercepts the exact virtual host `context-machine.internal` and forwards to the private `CONTEXT_MACHINE` Worker service binding. Staging and production target their own service. This is not public DNS or an exposed HTTP service. The service has no routes or workers.dev ingress. Configurable remote endpoints require HTTPS; loopback HTTP is allowed locally. The fixed Cloudflare bridge is the only non-loopback HTTP exception.

Budgets, firm settings, member/client authorization, redaction and accounting validation stay with the existing workflows. They already know the authenticated subject and transaction context; the gateway must not reconstruct that authority from prompt text. The gateway neither executes returned tool calls nor approves, posts, sends or changes accounting records.

There are no implicit retries or provider fallbacks. Each extra attempt needs another explicit budget reservation; changing providers may change consent and data handling. HTTP redirects are refused. Calls inherit shorter deadlines and otherwise stop after two minutes. Text requests are capped at 2 MiB; single-page OCR requests at 18 MiB (one PNG up to 12 MiB and 16 million pixels); responses default to 1 MiB with a 4 MiB ceiling. Streaming deltas remain provisional until successful completion and domain validation.

## Context-machine protocol, version 1

For Codex subscription mode, a caller must set `AllowUncappedOutputTokens: true` (wire field `allow_uncapped_output_tokens`) to acknowledge that `max_output_tokens` cannot be enforced by that transport. Omission rejects the run before database/provider work. Byte caps do not bound hidden reasoning tokens or cost. API-key routes retain the token ceiling.

POST through the private service binding with a dedicated bearer credential. Local development may use loopback HTTP. Database credentials, executable names, shell flags and arbitrary tool definitions are never request fields.

```json
{
  "version": 1,
  "purpose": "research",
  "model": "codex",
  "instructions": "Investigate the variance and cite evidence.",
  "data": {"close_id": "123"},
  "scope": {
    "organization": "firm-a",
    "subject": "member-a",
    "access_profile": "predefined-member-a-readonly"
  },
  "read_only": true,
  "store": false,
  "deadline": "2026-09-29T03:02:00Z",
  "max_model_calls": 4,
  "max_output_tokens": 4000,
  "stream": false
}
```

A successful response is HTTP 200:

```json
{
  "version": 1,
  "status": "completed",
  "scope": {
    "organization": "firm-a",
    "subject": "member-a",
    "access_profile": "predefined-member-a-readonly"
  },
  "id": "run-123",
  "model": "actual-model-used",
  "text": "Findings and evidence references…",
  "usage": {"input_tokens": 1000, "output_tokens": 300},
  "model_calls": 3
}
```

`status: "refused"` maps to a refusal. Missing completion, a mismatched scope, a missing run/model identifier, unexpected executable tool calls, an exceeded call budget, malformed JSON and oversized results fail closed. Partial research is not a completed result. Non-200 responses become bounded status-only errors; service error bodies are not surfaced to users.

Without callbacks, the service returns the JSON result above. With `OnProgress` or `OnText`, the adapter sets `stream: true` and requires `Content-Type: application/x-ndjson`. Each line is one event:

```json
{"type":"progress","scope":{"organization":"firm-a","subject":"member-a","access_profile":"predefined-member-a-readonly"},"text":"Reading the approved evidence"}
{"type":"text_delta","scope":{"organization":"firm-a","subject":"member-a","access_profile":"predefined-member-a-readonly"},"text":"Findings and evidence references…"}
{"type":"completed","scope":{"organization":"firm-a","subject":"member-a","access_profile":"predefined-member-a-readonly"},"response":{"version":1,"status":"completed","scope":{"organization":"firm-a","subject":"member-a","access_profile":"predefined-member-a-readonly"},"id":"run-123","model":"actual-model-used","text":"Findings and evidence references…","usage":{"input_tokens":1000,"output_tokens":300},"model_calls":3}}
```

Every event must carry the authorized scope; a mismatch is rejected before invoking callbacks. The terminal event contains the same validated result envelope. When deltas were sent, their concatenation must match the final text. `refused` and `error` events fail the call; unknown events, a lost connection before completion, malformed lines, scope mismatch, excessive bytes and cancellation never become completed results. Callbacks returning false close the response and cancel the request. Progress is provisional and not an accounting assertion. The service must flush events as they become available.

The adapter uses streaming HTTP with ordinary connection reuse today. The public request, callbacks and final result do not depend on HTTP, SSE, NDJSON or gRPC. A later gRPC implementation can replace the private adapter without changing workflows; no unused gRPC dependency or generated protocol is included. OpenAI streaming remains SSE inside its adapter. Typed tool calls/results are translated inside the selected adapter. For multi-step API calls, append `result.Continuation` unchanged to the conversation before appending tool results. This opaque, in-memory state preserves reasoning items, item IDs and assistant phases in provider order; reconstructing only `ToolCalls` loses required context. Continuation state is excluded from response JSON and must not be persisted or shown to users. See the [Responses reasoning guidance](https://developers.openai.com/api/docs/guides/reasoning).

Context-machine **research** accepts instructions plus data, not caller-defined tools, conversation history or structured-output schemas. **Completions** accept the same schema, typed conversation and tool-proposal contract as API inference. Each completion is one CLI turn with a proxy budget of exactly one provider call. It has no MCP, shell, web or filesystem tools and cannot acquire additional context. Caller tools are serialized descriptions; the CLI returns validated proposals, and only the authorized application workflow executes them. The proxy enforces the output schema, model, effort and token policy. Tool continuations contain typed assistant/call messages; API-specific opaque continuation state is rejected rather than silently translated. Text callbacks receive the validated final text once; this mode does not forward live CLI tokens. Domain validation still checks strict JSON, citations, facts and capability arguments. The service's agent uses its own preconfigured read tools internally. Long-running research should later use a durable workflow with submit/status/cancel and idempotency; avoid keeping an API request or database transaction open for minutes.

## Hosted runtime and database boundary

`services/backend/internal/llm/machine` authenticates the caller and matches the complete scope against a preconfigured profile before launching a CLI. It checks current organization status, membership and explicit client grants on every read. Profiles never grant clients by themselves. Each task gets a fresh temporary home/workspace; the entire subprocess group is killed on cancellation or deadline. No reusable conversation crosses requests.

The CLI receives a per-run loopback token, two MCP read tools and its assigned model. Provider API keys, static subscription tokens and the read-only database credential remain in the parent service. Personal Codex managed login additionally uses a private auth.json in its temporary home so the unmodified CLI can refresh it; the updated file persists in private Durable Object storage. Shell/file/web tools are disabled; the model proxy also strips every tool except the two scoped reads, including Codex's namespace format. The proxy pins the model, limits tokens per call where supported (see subscription capabilities below) and enforces the total model-call budget even if a CLI retries. Other provider paths and redirects are refused. CLI stdout and upstream response sizes are bounded. Prompts, provider error bodies and CLI stderr are not logged or returned.

The database login must be separate, non-owner and read-only. Readiness refuses superuser, bypass-RLS, create-role/create-database, table-write, schema-create and role-membership privileges. Reads use fixed SQL, a read-only transaction, tenant scope, a three-second statement timeout and existing redaction. No SQL tool or database credential is available to the agent. Provision exact grants using `infra/postgres/context-machine-role.sql`, including the base tables required by security-invoker views.

The tools are `list_closes` (up to 50 authorized closes) and `read_evidence` (up to five latest active, extracted evidence records for an authorized close, each truncated to 32,000 characters). Broaden the read surface only with corresponding authorization tests.

The service handles one run per container. Autonomous research returns 429 when busy; completions wait for the existing slot within the caller deadline without retrying inference. It sends a started progress event and completed result; it does not currently forward live CLI token deltas. There are no automatic retries, queues, warm CLI sessions or gRPC dependencies. Cloudflare can stop the container after five idle minutes; the next service call starts it.

## Coverage and remaining product integration

Gateway tests cover immutable routing, HTTP injection, typed tools, SSE/NDJSON parsing, refusals, truncation, cancellation, limits, redirects, scope mismatches and a source guard for known provider endpoints outside this library across backend, command binaries, frontend and Workers. Runtime tests cover request authorization, provider isolation, budgets, tool filtering, subprocess deadlines, read-only PostgreSQL privileges and grant revocation. CI runs the pinned CLIs inside the release image against a fake provider and real MCP server, requiring each to execute a read tool before returning its final answer. Tests use no real provider keys or paid calls.

Production readiness traverses the Go API's outbound bridge, authenticates to the context machine, verifies configured CLI binaries and checks database privileges. It does not prove provider billing/model availability or perform paid inference. Before enabling a user-facing research workflow, add explicit firm consent, reserve its call budget, record audit/provenance, and validate returned citations. Research results are proposals; they do not execute accounting changes.

## Explicit model, effort and authentication policy

`OPENAI_MODEL` and `LLM_MODELS` select direct API models. `OPENAI_REASONING_EFFORT` and JSON `LLM_REASONING_EFFORTS` select default/per-purpose effort; leaving the default unset preserves existing provider behavior. Configuration validates known purposes and effort spellings; the provider validates model-specific support. Replay checks are keyed by both the preparation model and its configured effort; migration 106 records the effort alongside saved results. Historical runs have null effort (not recorded) and receive a fresh scheduled check; new runs record an empty string for provider default. This one-time check uses up to three calls per firm within its existing daily budget, with at most 20 firms selected per sweep. For new runs, empty effort means the provider default, so pin an explicit supported effort and model snapshot for reproducible checks.

Research selects a CLI via `CONTEXT_MACHINE_MODEL`. The service independently pins `CODEX_MODEL`/`CLAUDE_MODEL`, `CODEX_REASONING_EFFORT`/`CLAUDE_REASONING_EFFORT` (default `medium`), and each agent's `*_AUTH_MODE`. Accounting completion model/effort defaults use the selected `CODEX_*`/`CLAUDE_*` settings, with `LLM_MODELS` and `LLM_REASONING_EFFORTS` overrides applied and checked in both the gateway and service. CLI efforts must be supported by the selected CLI; API-only `none` is rejected instead of downgraded. Auto prefers an API key, then Codex managed ChatGPT login when present, then its configured static subscription token. `CODEX_AUTH_MODE=chatgpt` explicitly selects personal managed login; personal modes require one owner profile. Explicit modes fail if their credential is absent. Selection never changes after an upstream failure, and accounting completions never become autonomous research runs.

Responses expose non-secret `reasoning_effort` and `auth_mode` alongside model provenance. The research access profile, read-only tools, tool filtering, deadline, byte cap and call budget apply in either credential mode. Codex subscription mode cannot enforce the API's per-call output-token ceiling; callers must explicitly opt in with `AllowUncappedOutputTokens: true`, or use API-key mode to preserve that guarantee. See [deployment configuration](context-machine-deployment.md) for supported token types, rotation and the local-versus-live validation boundary.

## PDF images and evaluation

Document jobs attach the evidence uploader's identity and check current membership and client grants before using their context-machine profile. They reserve one OCR call per page against the existing daily model budget. Each rendered page goes through `Generate` with purpose `ocr`. The context machine validates the image and attaches it to the provider request in memory; the CLI cannot browse files, run shell commands or fetch image URLs. Models transcribe only: PDF text layers remain authoritative, disagreements are flagged and scanned readings still require original-document verification. A failed image call uses the existing local OCR path, never another model provider.

Agent evaluation now supplies both the platform actor and gateway identity. Context-machine evaluation requires an explicit loopback `CONTEXT_MACHINE_ENDPOINT` and a local database. `agent-eval -profile local-eval` maps every recorded synthetic fixture to one exact configured local evaluation member, letting a personal subscription exercise the entire golden set without creating production grants or expanding the hosted owner's profile. See the deployment document for setup. CI continues using fake providers; a passing protocol test does not establish live model accuracy or account entitlement.

Context-machine page reading has a 45-second deadline including CLI startup. Images enter only when the single execution slot is idle; they never wait in its interactive queue. A busy reply or other context-machine failure turns off model reading for the rest of that PDF, preserving PDF text and local Tesseract recovery without more model reservations. An already running image call can still delay another completion until its deadline; shared-container capacity and live latency remain acceptance checks before activation. Known PNG/Claude transport limits are checked before reserving a call. Each provider's image output still requires original-document verification.
