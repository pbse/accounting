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
| `CONTEXT_MACHINE_ENABLED` | Variable | `true` (default) requires and deploys research; explicit `false` deploys the app without research or its service bindings. |
| `CONTEXT_MACHINE_TOKEN` | Secret | At least 32 random characters, shared only by the API, queue and context-machine services. |
| `CONTEXT_MACHINE_DATABASE_URL` | Secret | Dedicated non-owner, read-only PostgreSQL login with TLS, on the same database as that environment. Never reuse the application or migration login. |
| `OPENAI_API_KEY` | Secret | API credential for Codex and existing OpenAI workflows. Required for direct API workflows; optional for subscription-only research. |
| `ANTHROPIC_API_KEY` | Secret | Optional API credential for Claude Code. |
| `CODEX_AUTH_JSON` | Secret | Personal ChatGPT Pro managed login produced by `codex login` in an isolated file-backed home; never a browser cookie. Refreshed credentials persist privately in the context-machine Durable Object. |
| `CODEX_ACCESS_TOKEN` | Secret | Codex workspace access token for subscription/account-backed automation. Not a general OpenAI API key. |
| `CLAUDE_CODE_OAUTH_TOKEN` | Secret | Claude Code token generated with `claude setup-token`. |
| `CODEX_AUTH_MODE`, `CLAUDE_AUTH_MODE` | Variable | `auto` (default), `api_key`, or `subscription`; Codex also accepts `chatgpt` for personal managed login. Auto prefers an API key, then Codex managed login if present, then the static subscription token. |
| `CODEX_REASONING_EFFORT`, `CLAUDE_REASONING_EFFORT` | Variable | Explicit agent effort; defaults to `medium`. Use a level supported by the selected model. |
| `OPENAI_REASONING_EFFORT` | Variable, optional | Default effort for direct Responses calls; unset preserves provider behavior. |
| `LLM_REASONING_EFFORTS` | Variable, optional | JSON purpose-to-effort overrides, using the same purpose names as `LLM_MODELS`. |
| `OPENAI_MODEL` | Variable | Approved model for existing workflows; required when `OPENAI_API_KEY` is set. |
| `CODEX_MODEL` | Variable | Exact OpenAI model ID for enabled Codex. |
| `CLAUDE_MODEL` | Variable | Exact Anthropic model ID for enabled Claude; it must support adaptive thinking and `output_config.effort`. |
| `CONTEXT_MACHINE_MODEL` | Variable | `codex` or `claude`: selects the research agent. |
| `CONTEXT_MACHINE_PROFILES` | Variable | Nonempty JSON array of exact organization/subject/access-profile triples. |
| `LLM_MODELS` | Variable, optional | JSON purpose-to-model overrides for existing API workflows. |

Existing `CLOUDFLARE_API_TOKEN` and `CLOUDFLARE_ACCOUNT_ID` deploy all services. The token/account must support Workers, Durable Objects and Containers; Containers billing must be enabled. API keys require API billing and access to the chosen models. Subscription tokens use the corresponding account's permissions and limits. Only the selected research agent must be configured; an unused agent does not require a key or model. In `auto`, a missing model leaves that research agent disabled even if its API key exists for direct workflows. An explicitly selected auth mode with a missing credential fails startup/deployment. `auto` chooses once at startup; expired tokens, quota errors and refusals fail the run without changing credentials or repeating the request.

For example, these **variables** pin Codex research independently from direct API workflows:

```text
CONTEXT_MACHINE_MODEL=codex
CODEX_MODEL=gpt-5.5
CODEX_REASONING_EFFORT=medium
CODEX_AUTH_MODE=auto
OPENAI_MODEL=gpt-5.5
OPENAI_REASONING_EFFORT=low
LLM_REASONING_EFFORTS={"work_command":"none","investigation":"none","written_answer":"none"}
```

These small-budget overrides use GPT-5.5’s supported `none` effort; revalidate them if you choose a different model. Claude can be pinned separately with `CLAUDE_MODEL=claude-opus-5-5` and `CLAUDE_REASONING_EFFORT=medium`.

Use model IDs available to your account. Model/effort variables can be staged before credentials: the deployment preparation step clears direct-API route metadata from the step environment when `OPENAI_API_KEY` is absent, while preserving the GitHub variables. Installing the key activates that saved policy on the next deployment. Setting `CODEX_AUTH_MODE=subscription` explicitly chooses the account token even when an API key exists. Direct API mode still needs an API key. Context-machine completion mode preserves schemas and caller-executed tool proposals; it is separate from autonomous research.

Upload credentials through GitHub's production environment secrets UI or an interactive `gh secret set NAME --env production` prompt. Never put token values into chat, shell command arguments or repository files. [Codex access tokens](https://learn.chatgpt.com/docs/enterprise/access-tokens) are intended for trusted automation; [Claude Code tokens](https://code.claude.com/docs/en/env-vars#claude_code_oauth_token) can be generated using `claude setup-token`. The static-token modes do not import desktop keychains, accept browser cookies, or refresh those tokens. The separate Codex `chatgpt` mode below lets the unmodified CLI refresh its own managed login. Rotate expired/revoked tokens in GitHub and redeploy. Codex token availability depends on workspace permissions.

Static-token and API-key modes give the CLI only an ephemeral proxy token. Personal Codex managed-login mode additionally needs its own `auth.json` for native refresh; shell/file tools remain disabled and the proxy still enforces model, effort, tool and call limits. The parent selects the fixed provider endpoint, substitutes the selected credential, pins the model and effort on every inference, strips unsupported tools, and counts all attempts against the run budget. Codex subscription inference goes to the Codex account endpoint rather than the general Responses API. Claude subscription inference uses bearer authentication with the OAuth protocol header. Real account entitlement and token validity require a paid/account-authorized smoke run after deployment; local protocol tests cannot prove these.

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

1. The PR's CI runs Go/TypeScript checks, local PostgreSQL isolation tests and Worker dry runs, including production bindings. Go compilation is cached between pushes to the same PR, while `-count=1` forces tests to execute against the freshly migrated database. New pushes cancel superseded CI runs.
2. When backend sources, database migrations, the deployment script, the runtime image/Worker, the package lock, Docker context or CI workflow change, CI also runs the gateway/runtime race checks and builds the actual context-machine image. Reusable CI calls always run these checks. Unrelated frontend-only changes skip this additional stage, while retaining the full Go suite and gateway boundary test. A test binary inside it starts a fake provider and MCP server, runs **both installed CLIs**, and requires a read-tool invocation plus a final answer for both API-key and subscription modes at every accepted CLI effort level through the enforcing proxy. This catches incompatible CLI flags, tool names and output formats without provider credentials.
3. Merge after CI is green and environment settings exist. Both deployment jobs validate configuration before `npm ci`, migrations or builds. Context-machine preflight reports all missing required and selected-agent settings together without exposing values. Deploy production verifies the merged PR's CI, validates configuration, applies normal migrations, and deploys the private context machine **before** API and queue Workers. Staging uses the same contract through its manual workflow.
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

**Token-limit capability:** API-key mode enforces the requested per-call output-token limit. Claude subscription mode retains `max_tokens`. Codex's subscription transport does not accept `max_output_tokens`, so that route cannot promise a hard per-call token ceiling. The library caller must explicitly set `AllowUncappedOutputTokens: true` (wire field `allow_uncapped_output_tokens`) for this route; otherwise the service rejects the run before touching the database or provider. Use `CODEX_AUTH_MODE=api_key` where that ceiling is required. Both modes enforce the same model-call budget, response byte cap and execution deadline. The byte cap is not a token/cost ceiling and does not bound hidden reasoning work. Subscription usage is subject to account limits, not interchangeable with API dollar budgets.

One container accepts one run at a time, with a two-minute deadline and 1–16 model calls. Busy research requests return 429; the caller decides whether a new attempt is authorized. Completions wait for the existing execution slot within the caller deadline, without retrying inference. Each run gets a fresh temporary workspace. Streaming reports start and completion, not live token output. Five idle minutes allow the container to sleep.

Pin upgrades in `services/context-machine/Dockerfile` and rerun CI's actual-CLI smoke tests. Roll back by redeploying the last known-good commit of **all three Workers** with matching configuration. Update a rotated service token on all three in the same deployment. Failed later steps do not automatically undo earlier Worker deployments.

Provision the selected agent credentials/models, read-only service database credential and authorized profiles before deploying. A PR or dry run is not evidence of a live deployment.

## Launch acceptance checks

Starting the context machine enables the `research` route. Set `LLM_PROVIDER=context_machine` to route all eight accounting purposes through its private completion endpoint too; the main API and queue then need no provider API key. `LLM_PROVIDER=api` retains direct API routing. Research currently has no production feature caller. Integrate it into an authorized workflow with consent, budget reservation, provenance and citation validation before exposing it to users.

Before calling the AI layer ready:

- Require a passing PR CI run, then a successful production deploy and `/readyz`. If Actions was blocked by billing, resolve billing and rerun CI before retrying deployment; merging does not bypass this gate.
- Install the selected credentials, exact model IDs, efforts, read-only database login and profile using the settings above. `auto` uses API keys first; test subscription mode explicitly if it will be used, since successful API inference does not verify subscription entitlement.
- On synthetic, authorized accounting records, exercise evidence selection/preparation, statement extraction, procedure drafting, work commands, workflow planning, written-answer streaming and multi-round investigations. Verify strict JSON, final citations, tool continuation and the configured model/effort. Keep normal firm consent and approval gates enabled.
- Check token budgets against the chosen model and effort. These ceilings include reasoning: work commands have 200 output tokens, investigation rounds 600 and written answers 1,200. Higher effort can consume the ceiling before any usable answer. Select a supported low/no-reasoning setting for small tasks or deliberately change and retest their budgets; do not assume one global medium/high effort suits every purpose.
- Run an authorized context-machine research call through the main service's private binding for every credential mode that will be used. Confirm the response model, effort, auth mode, call count and evidence references. Confirm revoked client/member access is denied. Codex subscription calls require the explicit uncapped-output acknowledgement described in the gateway contract.

Fake-provider tests and `/readyz` do not prove live provider authentication, account entitlement, model capability or output quality. Keep those live acceptance checks as release prerequisites when credentials or model policy change.

### Missing configuration is not repaired by retrying deployment

A GitHub variable stores a non-secret setting; a GitHub secret stores a credential. `CONTEXT_MACHINE_DATABASE_URL` is a **secret containing a real, provisioned read-only login**, not a service URL and not a copy of `DATABASE_URL`. `CONTEXT_MACHINE_PROFILES` must name existing authorized platform members; a provider subscription token cannot replace that identity.

With `LLM_PROVIDER=api`, existing accounting features require `OPENAI_API_KEY`. With `LLM_PROVIDER=context_machine`, the selected CLI credential is sufficient for all model calls; provider keys remain confined to that service. `CODEX_ACCESS_TOKEN` can additionally enable Codex subscription mode, and `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN` enables Claude. Install these with GitHub's environment secrets UI or `gh secret set NAME --env production`, which prompts securely. API credentials cannot be generated by this repository, and GitHub does not return stored secret values.

With research enabled, do not rerun deployment until the required secrets and profile have been provisioned. Validation remains strict for enabled research.

To deploy the main app while research is being provisioned, explicitly set `CONTEXT_MACHINE_ENABLED=false` in the GitHub environment. Deployment then skips the context-machine build/deploy, removes its bindings from the disposable API/queue Wrangler configuration, and clears the caller token/model from the deployed secrets. Existing GitHub credentials and variables are retained. No context-machine Worker needs to exist, and readiness does not depend on research. Direct API features are independently available only when their API key is installed.

To enable research later, install the credentials, read-only login and access profile; set `CONTEXT_MACHINE_ENABLED=true`; then deploy from a fresh checkout. The committed configs restore the bindings and deployment creates the context machine before its callers. This switch does not delete an existing private context-machine service.

### Provision the service before credentials are available

With `CONTEXT_MACHINE_ENABLED=false` and `CONTEXT_MACHINE_TOKEN` installed in the production GitHub environment, run **Provision context machine in standby** (`provision-context-machine.yml`) on `main`. This deploys `close-context-machine-production` and the real container image with pinned Codex and Claude CLIs. It does not rebuild the main app, run migrations, require provider/database credentials, or enable research. The private Worker authenticates requests, then returns HTTP 503 with `{"status":"disabled"}` without starting a container. The image stays available on demand; no CLI process runs while disabled. Find it under Workers & Pages and Containers in the deployment account, not at a public URL.

The equivalent local command is `node scripts/deploy-context-machine.mjs --provision-disabled --production`. It refuses to run unless research is explicitly disabled. Routine app deployments still skip this service while disabled, avoiding repeated image builds. To activate it, supply the provider credentials, read-only database login and access profile, set `CONTEXT_MACHINE_ENABLED=true`, and run the normal production deployment so the service and caller bindings are configured together. Readiness remains unavailable until then.

### Personal subscriptions: your own research only

Use one owner access profile in `CONTEXT_MACHINE_PROFILES`; personal Codex login and Claude subscription mode reject multiple profiles. These credentials must not serve other platform users. All accounting workflows can use the personal login with `LLM_PROVIDER=context_machine`, but only requests from its configured owner identity are accepted. Other platform users require an appropriate API/provider arrangement.

**Codex / ChatGPT Pro:** create a separate managed session for this service using the official CLI. Do not upload the auth file actively used by your desktop: concurrent refreshes of the same session can invalidate each other. On your trusted computer:

```sh
# Use an isolated home, so this does not replace your usual Codex login.
auth_dir="$(mktemp -d)"
chmod 700 "$auth_dir"
CODEX_HOME="$auth_dir" codex -c 'cli_auth_credentials_store="file"' login
# After the browser login succeeds, upload privately; never print the file.
gh secret set CODEX_AUTH_JSON --env production --repo pbse/accounting < "$auth_dir/auth.json"
gh variable set CODEX_AUTH_MODE --body chatgpt --env production --repo pbse/accounting
# Delete this disposable login copy after a successful upload.
rm -f "$auth_dir/auth.json"
```

Deployment compacts the JSON and rejects seeds over 5,000 bytes before building, within Cloudflare’s [5 KB Worker secret limit](https://developers.cloudflare.com/workers/platform/limits/#environment-variables). `wc -c < "$auth_dir/auth.json"` checks the file size without displaying credentials.

The seed must contain `auth_mode: "chatgpt"` plus access, refresh, ID and account tokens. The service restores the current login into a mode-0600 file in each temporary CLI home. Codex performs its own refresh. Before inference and after completion, failure or cancellation, the service saves the updated file in the existing private Durable Object. The fixed `context-machine-auth.internal` outbound handler reaches an authenticated RPC method; no public or caller-facing route exposes credentials. No application/migration DB or new bucket is involved. Only the credential file persists, never conversations or task workspaces.

Storage holds sensitive access/refresh tokens under Cloudflare's storage protections and account access controls. Compromise of the context-machine service or Cloudflare account could expose them. One durable lease serializes use across container restarts/rollouts. A failed restore releases its lease without modifying credentials, since the CLI has not run. An expired unfinished execution lease fails closed because the process may have refreshed without saving: reseed with a new isolated login. Storage failures reject the run; they never fall back to a stale seed or another billing method. Routine deployments of the same seed preserve refreshed state. A newly generated seed replaces it; an old container cannot write back over that new seed. Removing `CODEX_AUTH_JSON` disables access to the stored login; revoke the session with the provider when retiring it.

**Claude Pro/Max:** run `claude setup-token` locally and complete Anthropic's browser authorization. Paste the generated token into the secure prompt from:

```sh
gh secret set CLAUDE_CODE_OAUTH_TOKEN --env production --repo pbse/accounting
gh variable set CLAUDE_AUTH_MODE --body subscription --env production --repo pbse/accounting
```

Claude's setup token is already supported by the service. Its real value stays in the parent; the restricted CLI receives a per-run proxy bearer. This static setup token is not automatically refreshed: generate and install a replacement when it expires or is revoked, then redeploy. Do not copy browser cookies or macOS Keychain data. Use this mode only for the subscription owner's research, with the published Claude Code binary; serving other users needs the appropriate API/provider arrangement.

Keep `CONTEXT_MACHINE_ENABLED=false` until the read-only database login and owner profile are configured. Then enable research and run the normal production deployment to install the credentials and restore caller bindings. Do not rerun the standby-only provisioning workflow to install credentials: it intentionally has no provider secrets. Validate one authorized run in each selected mode after activation; fake-provider CLI tests cannot prove real entitlement or token validity.

Sources: [OpenAI managed auth on trusted runners](https://learn.chatgpt.com/docs/auth/ci-cd-auth), [Claude token lifecycle](https://code.claude.com/docs/en/env-vars#claude_code_oauth_token), and [Claude hosted-use requirements](https://code.claude.com/docs/en/legal-and-compliance#authentication-and-credential-use).

## Route every accounting call through the context machine (B04/B12)

After installing the credentials and read-only owner profile, set production **variables**:

```sh
gh variable set LLM_PROVIDER --body context_machine --env production --repo pbse/accounting
gh variable set CONTEXT_MACHINE_ENABLED --body true --env production --repo pbse/accounting
gh variable set CONTEXT_MACHINE_MODEL --body codex --env production --repo pbse/accounting
# Required acknowledgement for personal Codex/subscription output limits:
gh variable set LLM_ALLOW_UNCAPPED_OUTPUT_TOKENS --body true --env production --repo pbse/accounting
# API-only "none" efforts are incompatible with the CLI. Start small-call purposes at low.
gh variable set LLM_REASONING_EFFORTS --body '{"work_command":"low","investigation":"low","written_answer":"low"}' --env production --repo pbse/accounting
```

Pin `CODEX_MODEL` / `CLAUDE_MODEL` and their default effort. Optional `LLM_MODELS` and `LLM_REASONING_EFFORTS` apply to every completion purpose; the same policy is deployed to the gateway and service. Redeploy using the normal production workflow to update the service, API and queue together. Preflight rejects routing to a disabled service, unsupported CLI efforts, missing credentials and unacknowledged Codex subscription token limits before builds/migrations. Small existing output ceilings may need explicit, budget-reviewed adjustment after live acceptance; a fluent partial answer is never success.

The HTTP organization boundary supplies the authenticated organization/subject. Preparation jobs use their recorded creator; scheduled replays use the original job creator, while manual replays retain the current caller identity. The service matches the entire provisioned profile and checks current organization/member access before launching the CLI. Personal accounts require one owner profile and reject other identities. Do not expose personal subscriptions to other platform users.

Each `/v1/complete` call uses exactly one model-call reservation and no agent read tools. Schemas and tool proposals are returned to existing domain validators; the caller's existing capability authorization, consent and approval gates still apply. Completion text arrives in one callback after validation, rather than live token deltas. Autonomous research retains its separately reserved multi-call/read-tool protocol. `/readyz` checks the same private binding for either mode but performs no paid inference.

To return accounting calls to direct API, set `LLM_PROVIDER=api`, install `OPENAI_API_KEY`/`OPENAI_MODEL`, restore API-compatible effort/model policy and redeploy. Research may stay enabled independently. There is no automatic fallback and no code change. Review provider disclosures and existing firm consent before switching account/provider types; task workspace deletion does not imply provider-side zero retention.
