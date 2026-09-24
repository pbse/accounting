# MCP access for agents

Any MCP client can use close preparation at `POST /mcp`: Claude, ChatGPT, Cursor, VS Code, or an in-house agent. The server uses the official MCP Go SDK in stateless mode. Clients on the current protocol (2026-07-28) and on earlier versions that start with `initialize` (2025-11-25, 2025-06-18, 2025-03-26) are both served.

## Connect

- **Local development:** point the client at `http://127.0.0.1:8080/mcp` and send the header `Authorization: Bearer <WORKSPACE_TOKEN>`.
- **Hosted:** send a signed-in WorkOS session token as the bearer. MCP accepts exactly the sign-ins the API accepts.

Browser-based clients must also run on an origin listed in `MCP_ALLOWED_ORIGINS` (comma-separated) or `FRONTEND_ORIGIN`; any other `Origin` gets 403. Clients that send no `Origin` header (desktop and server-side agents) are not affected.

Hosted OAuth sign-in through WorkOS AuthKit Connect is not enabled yet. Its tokens identify the person and firm but carry no app permissions, so the server would have no source for what that person may do. Until a permission source is chosen, those tokens are refused rather than guessed at.

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
