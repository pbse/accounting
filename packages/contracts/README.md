# Capability contracts

`reference.ts` describes `GET /v1/closes/examples/prepaid`, a synthetic worked example that only Krith's own firm (the one registered for `STAFF_EMAIL_DOMAIN`) can read. `prepaid.ts` describes the implemented, stateless `POST /v1/workpapers:prepare` capability. It supports equal full months, inclusive actual days, one prospective credit and confirmed booked-balance adjustments. `accrual.ts` describes fixed incurred accrual preparation. New methods require explicit confirmation and source-linked accountant review; they do not enable posting. See `docs/accounting-methods.md` for exact boundaries.

The first live capability should introduce versioned OpenAPI and generated consumers here, with request validation in Go. REST, MCP and UI must share that capability and authorization path. MCP remains a transport adapter; tool descriptions are not permissions.

PRD capability map (planned):

| REST | MCP tool |
| --- | --- |
| POST /v1/closes; GET /v1/closes/{id} | start_close; get_close |
| POST /v1/workpapers:prepare | prepare_workpaper |
| GET /v1/workpapers/{id}/evidence | explain_workpaper |
| POST /v1/proposals:preview | preview_adjustment |
| POST /v1/client-requests:draft | draft_client_request |
| POST /v1/approval-requests | request_approval |
| POST /v1/actions | execute_approved_action |
| POST /v1/workpapers/{id}:refresh | refresh_workpaper |
| POST /v1/closes/{id}:export | export_close_package |

Long operations return persisted job IDs and links for status/cancellation. Pagination and bounded filters are specified per capability when it is exposed.

## Errors

Every error from `/v1/closes` routes is `{"code": "…", "message": "…"}`. Branch on `code`; show `message` to a person.

| Code | Status | Meaning for an agent |
| --- | --- | --- |
| `INVALID_REQUEST` | 400, 413, 415, 422 | Fix the request; retrying it unchanged fails again. |
| `UNAUTHENTICATED` | 401 | Sign in again. |
| `SCOPE_DENIED` | 403 | The actor lacks the firm, client or permission. Do not retry; ask a person. |
| `NOT_FOUND` | 404 | The resource is absent or outside the actor's scope. |
| `CONFLICT` | 409 | Current state does not allow this action. Re-read the resource before deciding again. |
| `SOURCE_STALE` | 409 | The evidence, capture or policy source behind this action was replaced or changed. Re-read and re-propose from current sources. |
| `EXPIRED` | 410 | The link or attempt expired. Start again. |
| `LIMIT_REACHED` | 429 | A firm or rate limit applies. Resolve existing work first. |
| `EXECUTION_UNKNOWN` | 502 | A provider write may have happened, or its readback is unverified. Never repeat the write; use the posting's status or verify action. |
| `UNAVAILABLE` | 5xx | A dependency failed. Re-read current state before retrying a change. |

The stateless prepaid capability also returns `POLICY_REQUIRED` with the missing confirmation `field`. `APPROVAL_EXPIRED` is reserved: today's approval refusals cannot tell expiry from another reviewer's approval or an existing attempt, so they return `CONFLICT`.

## Retries

There is no `Idempotency-Key` yet; mutations are made safe to retry by their preconditions:

- Review and confirmation calls carry the digest or revision the actor saw; a repeat returns `CONFLICT` or `SOURCE_STALE` rather than applying twice.
- Close creation returns the existing close for the same client and month.
- Provider writes reserve one durable attempt with a stable request ID before calling QuickBooks.

A response cache for every POST was rejected in review: a replay could skip access revoked since the first call, a lost reservation could admit a second provider write, and a cached refusal could outlive a renewed approval. Add keys per capability, bound to that capability's durable record, when an outside consumer needs them.

An agent can request approval but cannot grant it. Execution accepts an approved plan reference, never arbitrary instructions. Job messages contain version, job ID and scoped identifiers; the consumer loads durable authority and validates scope rather than trusting delivery contents.
