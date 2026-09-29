# Deploying the context machine

The service runs on **Cloudflare Containers**, behind a private Worker service binding. Its image installs **Codex CLI 0.157.1** and **Claude Code 2.1.284**. Both execute on demand; Cloudflare starts the container when called. There are no interactive CLI sessions or public research URLs.

```text
API / preparation Go container
  → llm.Generate(purpose="research")
  → fixed context-machine.internal outbound handler
  → CONTEXT_MACHINE Worker service binding
  → private context-machine Worker / Container
  → Codex or Claude CLI
       → budgeted model proxy → provider API
       → scoped MCP read tools → read-only PostgreSQL login
```

Cloudflare uses [container outbound handlers](https://developers.cloudflare.com/containers/configuration/workers-connections/) to reach Worker bindings. Staging targets `close-context-machine-staging`; production targets `close-context-machine-production`. No caller configures `contextMachineURL`, and no public hostname or DNS record is needed. The API and queue Workers each declare their environment-specific binding.

## One-time configuration

Use the matching GitHub **environment** (`production` or `staging`). Do not put secrets in repository files, browser variables or chat.

| GitHub setting | Kind | Purpose |
| --- | --- | --- |
| `CONTEXT_MACHINE_TOKEN` | Secret | At least 32 random characters, shared only by the API, queue and context-machine services. |
| `CONTEXT_MACHINE_DATABASE_URL` | Secret | Dedicated non-owner, read-only PostgreSQL login with TLS, on the same database as that environment. Never reuse the application or migration login. |
| `OPENAI_API_KEY` | Secret | API credential for Codex and existing OpenAI workflows. Required for direct API workflows; optional for subscription-only research. |
| `ANTHROPIC_API_KEY` | Secret | Optional API credential for Claude Code. |
| `CODEX_ACCESS_TOKEN` | Secret | Codex workspace access token for subscription/account-backed automation. Not a general OpenAI API key. |
| `CLAUDE_CODE_OAUTH_TOKEN` | Secret | Claude Code token generated with `claude setup-token`. |
| `CODEX_AUTH_MODE`, `CLAUDE_AUTH_MODE` | Variable | `auto` (default), `api_key`, or `subscription`. Auto prefers the API key when both are present. |
| `CODEX_REASONING_EFFORT`, `CLAUDE_REASONING_EFFORT` | Variable | Explicit agent effort; defaults to `medium`. Use a level supported by the selected model. |
| `OPENAI_REASONING_EFFORT` | Variable, optional | Default effort for direct Responses calls; unset preserves provider behavior. |
| `LLM_REASONING_EFFORTS` | Variable, optional | JSON purpose-to-effort overrides, using the same purpose names as `LLM_MODELS`. |
| `OPENAI_MODEL` | Variable | Approved model for existing workflows; required when `OPENAI_API_KEY` is set. |
| `CODEX_MODEL` | Variable | Exact OpenAI model ID for enabled Codex. |
| `CLAUDE_MODEL` | Variable | Exact Anthropic model ID for enabled Claude. |
| `CONTEXT_MACHINE_MODEL` | Variable | `codex` or `claude`: selects the research agent. |
| `CONTEXT_MACHINE_PROFILES` | Variable | Nonempty JSON array of exact organization/subject/access-profile triples. |
| `LLM_MODELS` | Variable, optional | JSON purpose-to-model overrides for existing API workflows. |

Existing `CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID` deploy all services. The token/account must support Workers, Durable Objects and Containers; Containers billing must be enabled. API keys require API billing and access to the chosen models. Subscription tokens use the corresponding account's permissions and limits. Only the selected research agent must be configured; an unused Claude agent does not require a key or model. An explicitly selected auth mode with a missing credential fails startup/deployment. `auto` chooses once at startup; expired tokens, quota errors and refusals fail the run without changing credentials or repeating the request.

For example, these **variables** pin Codex research independently from direct API workflows:

```text
CONTEXT_MACHINE_MODEL=codex
CODEX_MODEL=gpt-5.5
CODEX_REASONING_EFFORT=medium
CODEX_AUTH_MODE=auto
OPENAI_MODEL=gpt-5.5
OPENAI_REASONING_EFFORT=medium
LLM_REASONING_EFFORTS={"investigation":"high"}
```

Use model IDs available to your account; omit `OPENAI_MODEL` when no OpenAI API key is installed. Setting `CODEX_AUTH_MODE=subscription` explicitly chooses the account token even when an API key exists. Direct API workflows still need an API key: their structured output, tool and consent contracts are not interchangeable with research runs.

Upload credentials through GitHub's production environment secrets UI or an interactive `gh secret set NAME --env production` prompt. Never put token values into chat, shell command arguments or repository files. [Codex access tokens](https://learn.chatgpt.com/docs/enterprise/access-tokens) are intended for trusted automation; [Claude Code tokens](https://code.claude.com/docs/en/env-vars#claude_code_oauth_token) can be generated using `claude setup-token`. This service accepts those static token secrets; it does not import a desktop keychain, refresh `auth.json`, accept browser cookies, or perform interactive login. Rotate expired/revoked tokens in GitHub and redeploy. Codex token availability depends on workspace permissions.

The CLI receives an ephemeral local proxy token, never the real subscription/API credential. The parent selects the fixed provider endpoint, substitutes the selected credential, pins the model and effort on every inference, strips unsupported tools, and counts all attempts against the run budget. Codex subscription inference goes to the Codex account endpoint rather than the general Responses API. Claude subscription inference uses bearer authentication with the OAuth protocol header. Real account entitlement and token validity require a paid/account-authorized smoke run after deployment; local protocol tests cannot prove these.

Example profile shape (replace both IDs with existing members):

```json
[{"organization":"org_EXAMPLE","subject":"user_EXAMPLE","access_profile":"accounting-read"}]
```

Profiles map to existing organization memberships and **explicit client grants**. They do not infer access from an administrator title or allow arbitrary subject substitution. Provision members and client grants using existing platform controls. Disabled organizations and revoked memberships/clients are denied by the read tools.

Create a new database LOGIN with a random password using the database owner's secure administration tooling. Grant only the supported read surface:

```sh
psql "$MIGRATION_DATABASE_URL" -v agent_role=context_machine_reader \
  -f infra/postgres/context-machine-role.sql
```

The script expects the login to exist and never stores its password. Set its connection string as `CONTEXT_MACHINE_DATABASE_URL`. Readiness rejects ownership, writes, privileged role membership and schema creation. On older PostgreSQL installations granting CREATE on public to everyone, remove that PUBLIC grant as an explicit database administration change first. Never grant SELECT on all tables: connection credentials and unrelated data are outside the tool surface.

Local development and tests use local PostgreSQL only. Never create Neon branches. Host-run Go processes may set `CONTEXT_MACHINE_ENDPOINT=http://127.0.0.1:8081/v1/run` for a separately started local runner. Docker Compose API/worker containers need a reachable private HTTPS endpoint: their loopback addresses refer to themselves, not the host.

## CI and rollout

1. The PR's CI runs Go/TypeScript checks, local PostgreSQL isolation tests and Worker dry runs, including production bindings.
2. CI builds the actual context-machine image. A test binary inside it starts a fake provider and MCP server, runs **both installed CLIs**, and requires a read-tool invocation plus a final answer for both API-key and subscription modes through the enforcing proxy. This catches incompatible CLI flags, tool names and output formats without provider credentials.
3. Merge after CI is green and environment settings exist. Deploy production verifies the merged PR's CI, validates configuration, applies normal migrations, and deploys the private context machine **before** API and queue Workers. Staging uses the same contract through its manual workflow.
4. Each service receives only its required secrets. The context machine receives no application write credentials, posting keys or integration secrets. Temporary Wrangler secret files are mode 0600 and deleted after deployment.
5. The final `${PRODUCTION_URL}/readyz` check traverses the **Go API → outbound handler → service binding → container** path. It verifies both binaries and read-only database privileges. Deployment retries span 100 seconds of delays, exceeding the 60-second readiness cache so a transient cold-start failure can recover. Persistent failure fails deployment; it does not silently disable research.

For an authorized manual rollout with environment settings loaded securely:

```sh
node scripts/deploy-context-machine.mjs --check
node scripts/deploy-context-machine.mjs --production
# Then deploy API and queue through Deploy production.
```

Do not add a public context-machine route for testing. Readiness tests connectivity without inference. A first paid research smoke call should originate from an authorized server workflow using the example in [LLM gateway](llm-gateway.md), with a real profile and reserved budget. This PR adds no public research endpoint and preserves existing accounting consent/approval behavior.

## Operations and rollback

`/healthz` is container liveness; authenticated `/readyz` checks the database role and enabled agent binaries. The API caches the private readiness result (including failures) for 60 seconds and coalesces concurrent probes; run authorization and database checks remain fresh on every execution. Neither contacts a provider, so billing, provider permissions and model availability still need the first authorized inference check. Errors expose no CLI stderr, secrets or prompts.

Model calls return the selected `model`, `reasoning_effort`, and `auth_mode` as non-secret metadata. Unsupported model/effort combinations fail at the provider; there is no silent effort downgrade.

**Token-limit capability:** API-key mode enforces the requested per-call output-token limit. Claude subscription mode retains `max_tokens`. Codex's subscription transport does not accept `max_output_tokens`, so that route cannot promise a hard per-call token ceiling. Use `CODEX_AUTH_MODE=api_key` where that ceiling is required. Both modes enforce the same model-call budget, response byte cap and execution deadline. Subscription usage is subject to account limits, not interchangeable with API dollar budgets.

One container accepts one run at a time, with a two-minute deadline and 1–16 model calls. Busy requests return 429; the caller decides whether a new attempt is authorized. Each run gets a fresh temporary workspace. Streaming reports start and completion, not live token output. Five idle minutes allow the container to sleep.

Pin upgrades in `services/context-machine/Dockerfile` and rerun CI's actual-CLI smoke tests. Roll back by redeploying the last known-good commit of **all three Workers** with matching configuration. Update a rotated service token on all three in the same deployment. Failed later steps do not automatically undo earlier Worker deployments.

Provision the selected agent credentials/models, read-only service database credential and authorized profiles before deploying. A PR or dry run is not evidence of a live deployment.
