# MCP access for agents

Any MCP client can use close preparation at `POST /mcp`: Claude, ChatGPT, Cursor, VS Code, or an in-house agent. The server uses the official MCP Go SDK in stateless mode. Clients on the current protocol (2026-07-28) and on earlier versions that start with `initialize` (2025-11-25, 2025-06-18, 2025-03-26) are both served.

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

AuthKit's OAuth tokens say who the person is but carry no app permissions, so the firm approves each member's agent access in **Firm settings → Agent access (MCP)**:

1. **The member asks** from their own signed-in session: read only, or read and prepare. "All clients" is offered only if their session has firm-wide access; otherwise the agent sees just their assigned clients.
2. **An administrator approves** (needs clients:all and members:manage). The approval can narrow the request but never widen it. The administrator can also decline or revoke, and the member can revoke their own access. Every decision is recorded with a reason.
3. **Access is bound to the member's current membership.** If the member is removed and later re-added, they must ask again. A pending or declined request never changes access already approved.

Read-only access lists only the read tools. Read-and-prepare adds the prepare, draft and request-approval tools; nothing beyond the tool table is reachable.

## Tools

Every tool replays one existing API call using the caller's own credentials. Firm membership, client assignment and permission checks are the same as in the app, and errors carry the same `code` (`SOURCE_STALE`: re-read; `CONFLICT`: re-read before deciding; `EXECUTION_UNKNOWN`: never repeat a write).

| Tool | What it does |
| --- | --- |
| `list_closes`, `get_close` | Closes the person can access; one close with its drafts |
| `list_work`, `review_queue` | Ranked preparation work; drafts ready for review, with approval requests |
| `list_evidence`, `read_evidence_text` | Documents on a close and their extracted text |
| `get_workpaper` | One draft: inputs, results, cited sources, blockers and questions |
| `find_exceptions` | Questions from the latest QuickBooks capture |
| `get_readiness` | What still blocks sign-off |
| `list_client_requests`, `draft_client_request` | Client request drafts; save a new draft (not sent or approved) |
| `open_close` | Open or return the close for a client and month |
| `add_evidence` | Upload a text or CSV document, which starts intake |
| `preview_close_preparation`, `prepare_close` | Coordinate preparation of a close's evidence |
| `request_approval` | Ask a person to review a draft; it shows as "Approval requested" in the review queue |

There are deliberately no tools that approve, sign off, post to QuickBooks, send client email, answer the agent's own questions, or change members, policy, procedures or retention. People keep that authority in the app (PRD Appendix C: "an agent cannot create its own authority").

Each bearer token is limited to 120 MCP requests per minute per API instance.
