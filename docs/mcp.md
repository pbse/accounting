# MCP access for agents

Any MCP client can use close preparation at `POST /mcp`: Claude, ChatGPT, Cursor, VS Code, or an in-house agent. The server uses the official MCP Go SDK in stateless mode. Clients on the current protocol (2026-07-28) and on earlier versions that start with `initialize` (2025-11-25, 2025-06-18, 2025-03-26) are both served.

## Deployment boundary

The Go API implements this transport. The Cloudflare edge forwards `/mcp` and OAuth discovery (`/.well-known/oauth-protected-resource` and paths under it) to the API, behind the same check as `/v1` that hosted sign-in is configured; `/internal/*` stays private. The deploy workflows pass `WORKOS_AUTHKIT_DOMAIN`, `MCP_RESOURCE_URL` and `MCP_ALLOWED_ORIGINS` to the API container as GitHub environment variables, and refuse a deploy unless the first two are set together, the AuthKit domain is a bare HTTPS origin, and `MCP_RESOURCE_URL` is exactly `/mcp` on the API origin (`VITE_API_BASE_URL`, else the environment URL). Hosted MCP still needs the WorkOS setup below and a deploy; see [deployment](deployment.md#github-environment-settings).

## Connect

- **Local development:** point the client at `http://127.0.0.1:8080/mcp` and send the header `Authorization: Bearer <WORKSPACE_TOKEN>`.
- **Hosted, any MCP client (OAuth):** the client signs in through WorkOS AuthKit, and the agent acts under the member's approved agent access (below).
- **Hosted, a signed-in session token:** also accepted, with that session's own permissions.

Browser-based clients must also run on an origin listed in `MCP_ALLOWED_ORIGINS` (comma-separated) or `FRONTEND_ORIGIN`; any other `Origin` gets 403. Clients that send no `Origin` header (desktop and server-side agents) are not affected.

### Hosted OAuth setup

1. **WorkOS Dashboard → Connect → Configuration:** enable Client ID Metadata Documents (and Dynamic Client Registration for older clients). Add the MCP URL, for example `https://api.example.com/mcp`, as a Resource Indicator.
2. **Set on the API:** `WORKOS_AUTHKIT_DOMAIN=https://<your-authkit-domain>` and `MCP_RESOURCE_URL=https://api.example.com/mcp`. The API then publishes `/.well-known/oauth-protected-resource/mcp`, and a 401 from `/mcp` points clients to it.
3. **What the API checks on each token:** the AuthKit issuer, the MCP URL as audience, a user consent (`sid`) and the firm (`org_id`). Machine-to-machine tokens are refused. These tokens work only on `/mcp`, never on the REST API.

### Agent access per member

AuthKit's OAuth tokens say who the person is but carry no app permissions, so the firm approves each member's agent access in **Agent access (MCP)**, which members use from **Firm settings** and administrators from the **Security center**:

1. **The member asks** from their own signed-in session: read only, or read and prepare.
2. **An administrator approves** (needs clients:all and members:manage). The approval can narrow the request but never widen it. The administrator can also decline or revoke, and the member can revoke their own access. Every decision is recorded with a reason.
3. **Agents see only the member's assigned clients** (Security center → Client access). Firm-wide access isn't extended to agents, because an OAuth token can't show that the member still has it.
4. **Access is bound to the member's current membership.** If the member is removed and later re-added, they must ask again. A pending or declined request never changes access already approved.

Read-only access lists only the read tools, including `export_close_package`. Read-and-prepare adds the prepare, recheck, draft and request-approval tools; nothing beyond the tool table is reachable.

### Agent attribution

What an agent makes under a member's agent access stays the member's: it counts as theirs for client access and the independence rule (a draft an agent prepared for a member is prepared by that member). It also records the MCP client that made it (migration 071): evidence and the intake job it starts, close preparations, client request drafts and approval requests carry `agent`, `MCP client <client_id>` from the OAuth token's `client_id` claim, or `MCP (client unverified)` when the token names none. The client ID identifies the OAuth application, not one running agent. Calls with the workspace token or a signed-in session record no agent. The evidence list shows "added by <member> via <client>", and the review queue shows who asked for an approval and through which client; the evidence, request draft, preparation and review queue reads return both. Opening a close records no actor for anyone, so it has no attribution either.

## Tools

Every tool replays one existing API call using the caller's own credentials. Firm membership, client assignment and permission checks are the same as in the app, and errors carry the same `code` (see [Errors](#errors-and-retries)).

| Tool | What it does |
| --- | --- |
| `list_closes`, `get_close` | Closes the person can access; one close with its drafts |
| `list_work`, `review_queue` | Ranked preparation work; drafts ready for review, with approval requests |
| `list_evidence`, `read_evidence_text` | Documents on a close and their extracted text |
| `get_workpaper` | One draft: inputs, results, cited sources, blockers and questions |
| `find_exceptions` | Questions from the latest QuickBooks capture, abnormal Trial Balance balances and holding or clearing accounts that still hold a balance |
| `get_readiness` | What still blocks sign-off |
| `native_reconciliation` | For Xero books, Xero's own reconciled flags per bank account |
| `proposed_state` | Production journal proposals awaiting approval or execution, with balances before and projected after |
| `workpaper_changes` | What changed between a draft and the version it revises |
| `list_close_jobs` | A close's 20 newest preparation jobs |
| `morning_brief` | What the agent did in the past 24 hours across open closes, and which closes need a person today |
| `agent_track_record` | Per client over 12 months: model proposals people confirmed unchanged, the fields they changed, and how the drafts were judged |
| `close_brief` | Where a close stands, area by area, with a headline of what needs a person |
| `explain_changes` | The largest Profit and Loss movements from last month, with the payees behind them and questions about bills that did not recur |
| `tie_out` | Each asset and liability compared with the balance the close's workpapers support |
| `list_client_requests`, `draft_client_request` | Client request drafts; save a new draft (not sent or approved) |
| `open_close` | Open or return the close for a client and month |
| `add_evidence` | Upload a text or CSV document, which starts intake |
| `preview_close_preparation`, `prepare_close` | Coordinate preparation of a close's evidence |
| `request_approval` | Ask a person to review a draft; it shows as "Approval requested" in the review queue |
| `export_close_package` | The close's review package: its manifest (scope, readiness, workpapers, evidence and postings) and each file's SHA-256, with the ZIP attached as an embedded resource while it is at most 5 MB (a larger one is downloaded by a person from the close page). It is not the release-gated binder |
| `plan_catalog`, `propose_plan`, `list_plan_proposals` | Propose a [workflow plan](workflow-engine.md#agent-proposed-plans) for the firm from the catalog's activities, with the reason it should run (10–2,000 characters). It needs `workpapers:review`; it becomes a proposal in the platform staff's approval queue and runs nothing until they approve it. At most 20 a firm a day. `list_plan_proposals` shows what became of each, 50 at a time (`before` pages back); a member limited to some clients sees only the proposals made as them |
| `recheck_close` | Queue the close's failed or pending proposals whose documents are current to be prepared again, with `recheck.digest` from `preview_close_preparation`; open client request links for those jobs are revoked, as on the close page |

PRD Appendix C's `export_close_package` is the tool of that name. Its `refresh_workpaper` has no single tool: a changed or replaced document already starts new preparation and reopens the drafts that cite it, which `workpaper_changes` and `list_close_jobs` show, and `recheck_close` retries proposals that failed or wait for confirmation. There is no route that re-prepares one draft on request.

There are deliberately no tools that approve, sign off, post to QuickBooks, send client email, answer the agent's own questions, or change members, policy, procedures or retention. People keep that authority in the app (PRD Appendix C: "an agent cannot create its own authority").

Each bearer token is limited to 120 MCP requests per minute per API instance.

## Errors and retries

The REST API under `/v1/closes` and every MCP tool return errors as `{"code", "message"}`. What a client should do:

| Code | Meaning | What to do |
| --- | --- | --- |
| `SOURCE_STALE` | Evidence or a source changed since you read it | Re-read, then decide again |
| `POLICY_REQUIRED` | An accountant-approved procedure is missing or contradicts the draft (such as the loan procedure, the reconciliation or card procedure, or the approved method) | Ask the firm to approve or apply the procedure; do not retry unchanged |
| `SCOPE_DENIED` | The caller lacks the permission or client access (HTTP 403) | Do not retry; ask for access |
| `APPROVAL_EXPIRED` | A person's posting approval ran out (10 minutes) before execution; nothing was written | A person approves again |
| `EXECUTION_UNKNOWN` | An outside write may or may not have happened | Never repeat it; verify the saved attempt |
| `CONFLICT` | State changed or the action is not allowed now | Re-read before deciding |
| `PASSCODE_REQUIRED` | A client link needs its passcode | Enter the passcode the firm gave |
| `LIMIT_REACHED`, `NOT_FOUND`, `UNAUTHENTICATED`, `INVALID_REQUEST`, `UNAVAILABLE`, `EXPIRED` | As named, from the HTTP status | Fix the request, sign in, or retry later |

**Idempotency keys.** A POST under `/v1/closes` may carry an `Idempotency-Key` header of 1–255 visible ASCII characters (migration 059). Within 24 hours the same firm's key runs the request at most once.

- **Same request after a success:** `409 IDEMPOTENT_REPLAY`, naming the first status. Re-read current state.
- **Same request while the first is still running or ended with a 5xx:** `409 IDEMPOTENCY_IN_PROGRESS`, because the outcome is uncertain. Re-read before trying again with a new key.
- **Key reused for a different request (or by another person):** `422 IDEMPOTENCY_KEY_REUSED`.
- **After a refusal from the action itself:** the key is held too, because some refusals follow real work (a model call, a saved connection step). Fix the request and send it with a new key. A refusal before the action (sign-in, firm or client access) records nothing, so the key stays unused.
- **An empty key header** is refused, not treated as no key. If the final status could not be saved (logged by the API), the key stays "in progress" until it expires; re-read the state and use a new key.

Only a fingerprint of the request (person, path, query, content type and body) and the final status are stored, never the response. So no response, link token or passcode is copied, and a repeat reveals only that the key was used. Many actions are already safe to repeat without a key: they carry a digest or revision, or deduplicate by content. MCP tool calls do not send keys; re-read after an uncertain tool call.
