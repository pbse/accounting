# First feature: prepaid preparation

## Goals and acceptance

Turn entered invoice terms into a calculated, reviewable draft through one Go capability: `POST /v1/workpapers:prepare`. React calls the same typed API future agents will call. The operation is bounded, deterministic and synchronous; no queue/model is needed to divide a known amount.

Supported method: USD, accrual basis, whole calendar months, equal monthly allocation, round down to cents with any residual assigned to the final month. The user must confirm the method and that the full invoice was booked to the specified expense account, with no prior adjustments or credits. These confirmations record assumptions in the exported draft, not authenticated accounting approval or ledger verification.

Acceptance: the PRD's $12,000/12-month example produces $1,000 expense and $11,000 prepaid at the first month-end; uneven cents conserve the invoice total; unsupported periods and missing confirmations block preparation; editing inputs hides the previous result; source reference and assumptions travel with the JSON export; CSV contains the complete schedule. No zero-value adjustment is proposed at the end of the service period.

## Non-goals / unchanged scope

No file upload, extraction, ledger connection, persisted client data, login, approval grants, posting, agent reasoning, async orchestration, partial-month policy, credits or prior-posting reconciliation. User-supplied source references are not verified evidence. The existing public site and synthetic reference remain available. This is the first deterministic preparation capability, not a complete R1 close.

Drafts exist in browser memory and can be exported. Refreshing or leaving the preparation page discards them. The API does not save or log request bodies. Do not represent an export as a saved close or an approved journal entry.

## Plan and minimalism

1. Go stdlib input validation, date handling and integer-cent calculation (ladder rung 3).
2. One bounded JSON HTTP endpoint with specific field errors and cross-origin POST preflight (native HTTP/stdlib, rung 3).
3. Native form controls, an explicit preparation action, source/assumption review, full schedule and native file downloads (rung 4). Reuse the existing dashboard frame rather than duplicate it (rung 2).
4. Focused tests for monetary conservation, the golden reference, invalid/unsupported input and the HTTP contract; browser-check calculation, edit invalidation and exports.

Author: Codex. Continue the same uncommitted foundation scope; the repository still has no base commit or origin/main for worktree creation. No commit, push or release is included.

## Calling the capability

Send the `PrepaidInput` contract from `packages/contracts/prepaid.ts` as JSON to `/v1/workpapers:prepare`. The response contains normalized input, `status: draft`, the complete monthly schedule, period and cumulative expense, prepaid balance and a nullable proposal. At the final service month the proposal is null.

Errors are structured `{code, field, message}`: 422 for unsupported terms or missing confirmations, 400 for malformed/unknown/trailing JSON, 415 for a non-JSON content type and 413 when the initial JSON body exceeds the 16 KiB read limit. No request becomes an approval or a durable job. The method identifier `equal_full_months_v1` is included in every exported input so its rounding convention remains explicit.

## Saved close integration

The later saved-closes feature adds explicit save/reopen when preparation starts from a client close. The server reruns this same method and saves an immutable snapshot. The calculation itself remains stateless, and standalone drafts still require export. See `saved-closes-and-reconciliation.md`.
