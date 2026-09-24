# Approved procedures and client accounting authority

The client’s accounting professional approves accounting treatment. This feature records approved instructions and optional account names for the supported prepaid, accrual and reconciliation methods, and enables loan schedules per client (a loan procedure pins the `lender_statement_v1` method; see accounting-methods.md). It does not enable new arithmetic methods, invent materiality thresholds or treat instructions as proof of ledger booking.

## Use the flow

1. Open a client close → **Approved procedures and client overrides**.
2. Choose a firm default or a client override and the supported work kind. Enter approved instructions, optional prepaid account names, and the accounting professional/approval reference and reason.
3. Select **Review affected closes** before publishing. The preview shows the scoped client periods, pinned procedure sources and draft counts (latest 100, with a total). Confirm authority and publish against that exact impact digest. The server stores a new immutable version, with the authenticated actor; stale editors or changed close inventories cannot silently reuse the preview.
4. A client override replaces the firm default for that work kind. A disabled client override suppresses the default for new context. Disabling does not erase existing pinned records.
5. On initial evidence intake, effective procedures attach as immutable source text when the close has no pinned version. Local proposals and model inventory include that context. Any proposed account mapping is cited and still needs review and booking confirmation.
6. Existing closes stay pinned when a new version is published. To change one, inspect the effective version, acknowledge the impact and choose **Apply version to this close**. The previous procedure source is superseded; dependent reviews/sign-off become stale. Recheck the job or prepare a revised draft from current evidence. Reapplying the same version does not duplicate sources.

## Learn a reviewed method rule

Open **Learn a procedure from evidence or reviewer edits** inside a client close. Upload a firm checklist or policy there; it is stored as a procedure source without starting an accounting preparation job or entering the agent's ordinary evidence inventory. Choose a supported work kind, method and either that current document or the client's prior revised workpapers. With OpenAI configured on the API, the model receives only the selected extracted document text (at most 16 KiB) or up to 30 approved field edits from this client (at most 16 KiB of serialized context), plus the chosen kind and method. It returns draft instructions and, for documents, an exact unique supporting quote. The API rejects incomplete, oversized, uncited or invalid output without saving it. With no model settings, the local review prompt highlights one exact document passage or edited fields. Either output is inactive and labeled by origin. PDF text must finish extraction before selection. An identical file already uploaded as ordinary work evidence keeps its original purpose; use a distinct source for policy drafting.

Rewrite the proposal as the accountant's actual eligibility, evidence, period and exclusion instructions. Typed learned rules currently cover prepaid and accrual methods. Reconciliation account types continue to use reviewed guidance because a single method rule would incorrectly block the other account type. Select an already approved workpaper that uses the chosen method, inspect its source and result, and record the accountant's approval reference. The server replays that exact saved input through the current calculator and rejects any changed output, workpaper digest, review decision or source revision. An accountant can create and approve a synthetic workpaper first when no suitable example exists. Once an example is approved, start a new proposal if the rule text needs changes.

Choose **Preview affected clients** to inspect every scoped close (latest 100 shown, total counted), then record the accountant's approval and activate against the current preview digest. Activation creates an immutable procedure version. A method rule is enforced on new manual and queued workpapers; a different method is blocked until the approved procedure context changes. Existing pinned closes keep their prior version and require the usual reviewed **Apply version** action. Legacy free-text guidance remains available where no active learned method rule applies. A learned method rule cannot be replaced by enabled guidance without a new proposal and example.

The replay proves the existing calculator output and selected method for the approved examples. It cannot validate the meaning of free-text instructions; the accountant must inspect the policy source, proposed boundaries and examples. Model output cannot grant approval or post to a ledger. Responses requests use strict structured output, `store: false`, and no tools; do not claim that this controls the provider's separate data-retention terms. The proposal records its model name, but never a credential.

An active rule can restrict other supported methods for the same work kind and client scope. Use a client override or a new reviewed rule before preparing a different method. Retention deletion blocks a close while it supplies an active rule's document, prior edit or approved example. An accountant can publish a disabled version to retire that rule; deletion then removes the example approvals and source links from retained draft history.

Conflicting invoice/reply/procedure values remain questions, not silently resolved preferences. Uploaded documents cannot publish procedures, grant permissions or authorize posting. Carry-forward preserves procedure source links and still requires fresh review. Exported originals contain the pinned procedure text/version/approval reference.

## Permissions

WorkOS organization membership is required in hosted mode. Grant `clients:all` only to intended firm staff. A client accounting professional should instead receive a client assignment, plus only needed action permissions:

- `workpapers:review`: review and preparation decisions.
- `procedures:manage`: record approved procedures; assigned users can change only their client overrides, never firm defaults.
- `close:signoff`: scoped sign-off/reopening, in addition to review authority.
- `members:manage` plus `clients:all`: administer assignments.

On the Client closes page, **Client access administration** records grants/revocations for exact WorkOS user IDs and existing client names. Obtain the ID from the verified membership in WorkOS. Assignment alone grants no action permission or organization membership. It covers every period of that client. Later requests enforce revocation; already downloaded data and valid firm-wide role tokens are not recalled. Remove `clients:all` in WorkOS when an account should become restricted.

The local token is a shared development identity. It can exercise these flows on synthetic data but cannot establish who a real accountant is. Invitations and WorkOS role changes remain operator-managed. Client identity currently uses the organization plus a case-insensitive client label; rename/merge workflows are not implemented.

## Setup and limits

Apply migrations through 036 in order, with API/worker stopped, then restart both on the same revision. Fresh Compose startup applies them automatically. Add the above exact permission names to hosted WorkOS roles before admitting users; a valid organization session without `clients:all` or an assignment has no client data access.

For model drafts, set both `OPENAI_API_KEY` and `OPENAI_MODEL` privately on the API and worker. Local Compose reads the root `.env`; staging passes the same server-only secret and model to the Cloudflare API container and queue worker. With only one setting, drafting fails visibly. With neither, local prompts remain available. Apply migration 036 before deploying the updated API. No new package is needed. The version-history screen supports 200 versions and fails explicitly beyond that; larger histories need a paginated view before broader rollout. New calculation methods and materiality thresholds remain separately reviewed accounting work.

API: `GET/POST /v1/closes/procedures`, `POST /v1/closes/{id}/procedures/apply`, `GET/POST /v1/closes/access`. Publishing/applying/access changes require explicit confirmation and current version references. Caller-supplied organization or actor metadata cannot change ownership.

## Accounting basis per client

Choose **Accounting basis** in **Approved procedures and client overrides** to record the basis the client's accountant approved: accrual, modified cash or cash. Set it once as the firm default and override it for individual clients. A basis version is always enabled; to change a client's basis, publish a new override.

- **Cash and modified cash clients:** new prepaid and accrued-expense drafts are refused, and so is a loan draft that accrues interest. Drafts saved before a basis change can't be approved, and prepaid schedules can't be posted to production. Reconciliations, Stripe settlements and loans without accrued interest are unaffected.
- **Accrual clients:** today's behavior, with per-draft accrual confirmations.
- **The close header shows the effective basis** and whether a client override sets it. Prepaid and accrual actions are hidden for cash and modified-cash clients.
- **Rollout exception:** a firm that hasn't set any basis keeps today's behavior (each prepaid or accrual draft confirms accrual basis) until it publishes a firm default.

## Owner treatments per client

Record each client's entity type under **Owner treatments** (a client override): sole proprietor or single-member LLC, partnership, S corporation, or C corporation. The firm default can hold the firm's owner policy and account naming, but never an entity type; that is a fact about one client. Owner reclassification drafts are refused until the client's entity type is recorded, and must use it.

