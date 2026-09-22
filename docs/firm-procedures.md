# Approved procedures and client accounting authority

The client’s accounting professional approves accounting treatment. This feature records approved instructions and optional account names for the existing supported prepaid and reconciliation methods. It does not enable new arithmetic methods, invent materiality thresholds or treat instructions as proof of ledger booking.

## Use the flow

1. Open a client close → **Approved procedures and client overrides**.
2. Choose a firm default or a client override and the supported work kind. Enter approved instructions, optional prepaid account names, and the accounting professional/approval reference and reason.
3. Confirm authority and publish. The server stores a new immutable version, with the authenticated actor; stale editors cannot overwrite a newer version.
4. A client override replaces the firm default for that work kind. A disabled client override suppresses the default for new context. Disabling does not erase existing pinned records.
5. On initial evidence intake, effective procedures attach as immutable source text when the close has no pinned version. Local proposals and model inventory include that context. Any proposed account mapping is cited and still needs review and booking confirmation.
6. Existing closes stay pinned when a new version is published. To change one, inspect the effective version, acknowledge the impact and choose **Apply version to this close**. The previous procedure source is superseded; dependent reviews/sign-off become stale. Recheck the job or prepare a revised draft from current evidence. Reapplying the same version does not duplicate sources.

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

Apply migrations 013 and 014 after 001–012, with API/worker stopped, then restart both on the same revision. Fresh Compose startup applies them automatically. Add the above exact permission names to hosted WorkOS roles before admitting users; a valid organization session without `clients:all` or an assignment has no client data access.

No additional dependency or cloud service is needed. Procedures use PostgreSQL, existing evidence validation, existing calculators and the existing durable worker. The version-history screen supports 200 versions and fails explicitly beyond that; larger histories need a paginated view before broader rollout. Accounting policy learning, regression-approved method changes and materiality handling remain separate work.

API: `GET/POST /v1/closes/procedures`, `POST /v1/closes/{id}/procedures/apply`, `GET/POST /v1/closes/access`. Publishing/applying/access changes require explicit confirmation and current version references. Caller-supplied organization or actor metadata cannot change ownership.
