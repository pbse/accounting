# Capability contracts

`reference.ts` describes `GET /v1/reference/prepaid`, a public synthetic fixture. `prepaid.ts` describes the implemented, stateless `POST /v1/workpapers:prepare` capability. It supports equal full months, inclusive actual days, one prospective credit and confirmed booked-balance adjustments. `accrual.ts` describes fixed incurred accrual preparation. New methods require explicit confirmation and source-linked accountant review; they do not enable posting. See `docs/accounting-methods.md` for exact boundaries.

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

Long operations return persisted job IDs and links for status/cancellation. Specify idempotency-key scope, pagination, revision preconditions and bounded filters. Errors must distinguish SOURCE_STALE, POLICY_REQUIRED, SCOPE_DENIED, APPROVAL_EXPIRED and EXECUTION_UNKNOWN.

An agent can request approval but cannot grant it. Execution accepts an approved plan reference, never arbitrary instructions. Job messages contain version, job ID and scoped identifiers; the consumer loads durable authority and validates scope rather than trusting delivery contents.
