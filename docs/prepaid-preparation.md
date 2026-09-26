# Prepaid preparation

The deterministic capability `POST /v1/workpapers:prepare` turns explicit invoice terms and confirmed assumptions into a reviewable schedule. It does not itself upload evidence, persist a job, grant accounting approval or post. Saved-close and agent flows call the same calculator after their evidence and authority checks.

Supported treatments include equal full calendar months and inclusive actual-day allocation, explicit prospective/multiple/retrospective credits, confirmed booked-balance adjustments and reviewed month-boundary cancellation methods. Each has different prerequisites; the client's basis, currency and approved procedure constrain use. Read the authoritative [method definitions and synthetic examples](accounting-methods.md) before confirming a treatment.

## User flow

Open **Prepare a prepaid** or start from a client close. Enter or review invoice terms, source references, service dates, period and accounts. The $12,000 example fills values but leaves confirmations unchecked. Confirm only supported facts and assumptions, calculate, then inspect every month and the proposed entry. Changing an input clears the earlier result. The $12,000 full-year example produces $1,000 monthly expense and $11,000 prepaid at the first month-end; cents are conserved by the method's rounding rule.

Standalone drafts remain in browser memory until exported as JSON/CSV. From a close, save a source-linked immutable draft or let the evidence-to-preparation job produce one. Saving, approving the workpaper, approving/executing a posting and signing off the close are distinct actions. [Evidence preparation](evidence-and-preparation.md), [review](workpaper-review.md), [posting](production-posting.md)

## Capability contract

Send the `PrepaidInput` shape from `packages/contracts/prepaid.ts` as JSON. The result includes normalized input/method, draft status, schedule, expense/prepaid balances and a nullable proposed entry. A method's confirmations record assumptions; they do not establish ledger truth.

The endpoint rejects malformed/unknown/trailing JSON, unsupported terms, missing confirmations and oversized/non-JSON bodies. Errors include code/message and a field when applicable. Domain arithmetic lives in `services/backend/internal/prepaids`; transport and saved preparation rerun it instead of trusting browser results.

See [onboarding](onboarding.md) for current local/hosted setup and [validation](validation.md) for historical synthetic checks. Live accountant/provider acceptance is recorded separately.
