# AuthKit, sandbox posting and staging

## Implemented scope

One private firm workspace authenticated with WorkOS AuthKit; a human-controlled sandbox prepaid journal flow; and a manually dispatched GitHub Actions deployment to Cloudflare staging. No production ledger endpoint exists in the posting client. Marketing, deterministic accounting methods, and statement matching remain unchanged.

Acceptance: valid organization sessions access saved work; wrong organizations/issuers/expired tokens and development-token bypasses fail; approval binds a saved prepaid, sandbox company, account mapping, date, amount and digest; a durable attempt precedes any outbound write; repeats never create a second attempt; ambiguous calls remain unknown; independent journal readback is required for verified status. Staging deployment checks identity configuration and migrated database readiness before/after deployment.

Non-goals: multi-firm tenancy, production posting, source ingestion verification, autonomous approval, managed QBO OAuth onboarding/refresh, live-provider certification. This sandbox integration uses manually supplied short-lived access tokens from Intuit's developer tools. Refreshing an expired sandbox token is an operator task. No token is returned to the browser. The API has no production hostname option.

Ponytail ladder: the official React SDK handles OAuth/PKCE and refresh; the maintained Go JWT package validates signatures/claims; stdlib handles HTTP, JSON, bounded JWKS caching and exact readback comparisons; PostgreSQL conditional updates/uniqueness protect state transitions. No auth-provider adapter layer, ORM, retry engine, or background queue was introduced. The unconsumed queue worker remains in the repository but is not deployed by the staging command.

## WorkOS configuration

Configure the WorkOS application before enabling the integration:

- Add the frontend origin to allowed origins, `/login` on that origin as redirect URI and login URI, and the origin as a sign-out URI.
- Configure an organization, invite the authorized human users, and assign `posting:approve` and `posting:execute` permissions only to the appropriate reviewer role.
- Backend: `WORKOS_CLIENT_ID` and exact `WORKOS_ISSUER` for the environment. The expected issuer is explicitly configured because custom authentication domains and environment formats differ; do not accept arbitrary issuers from incoming tokens.
- Frontend: `VITE_WORKOS_CLIENT_ID` and, for hosted use, `VITE_WORKOS_API_HOSTNAME`. The SPA uses the official React SDK and sends bearer access tokens to the configured API URL. No WorkOS API key is needed for this public-client flow or server JWKS verification.
- Native development: put frontend values in `apps/frontend/.env.local` and export backend settings before starting Go. Compose forwards root `.env` values `WORKOS_CLIENT_ID` and `WORKOS_API_HOSTNAME` to Vite automatically.

The backend retrieves signing keys only from the configured client's WorkOS JWKS path, accepts RS256, validates expiry and issuer, requires user/session identity and the one configured organization, and checks audience when present. Access-token validity lasts until expiry; this is not an immediate session-revocation check. Keep session access-token lifetime short in WorkOS. Reconfiguring the allowed organization does not migrate data: this is one workspace per database, not multi-tenant hosting.

When a WorkOS client is configured, the development workspace token is rejected. Without WorkOS, native local development still supports the existing private token, but that token cannot authorize posting. Cloudflare does not forward this development token and rejects hosted workspace requests when WorkOS configuration is missing.

## Sandbox posting

Apply `infra/postgres/migrations/002_sandbox_posting.sql` to the intended development database before testing. Fresh Compose databases apply both migrations; existing ones require the second migration explicitly. A migration is not applied automatically by the API or deployment workflow.

Configure `QBO_SANDBOX_REALM_ID`, `QBO_SANDBOX_ACCESS_TOKEN`, `QBO_SANDBOX_ENTITY`, `QBO_SANDBOX_PREPAID_ACCOUNT_ID`, and `QBO_SANDBOX_EXPENSE_ACCOUNT_ID` on the server. Use the sandbox's company name exactly as the client name. Prepaid account names must match the active QBO accounts; debit must classify as Asset and credit as Expense. The current source/initial-booking assumptions remain manually confirmed; use synthetic test data only.

Open a saved prepaid in a client close and use **Check posting status**:

1. Prepare a preview. The server reads company/account metadata and persists the exact journal and digest.
2. Approve the current source-linked workpaper review, then type **APPROVE SANDBOX DRAFT**. A pending, changed or rejected workpaper cannot be approved for posting. Posting approval records the WorkOS user and expires after ten minutes.
3. Type **POST TO SANDBOX**. The same human reviewer needs execution permission. A conditional database update records the attempt before the HTTP write. Expired approvals, changed mappings, newer saved versions of the source, and existing attempts stop execution.
4. A successful create response records the external ID. An independent GET compares ID, date, currency, note and both exact account/amount lines before marking verified. This verifies the journal fields, not an accounting close or full ledger balances.

A database-generated UUID is sent as `requestid`; correctness does not depend on assuming provider idempotency. The application permits only one attempt. A lost response becomes `unknown`; a crash can leave `executing`, which must also be treated as uncertain. If an external ID is available, **Verify saved entry** performs read-only recovery. If no ID was recorded, manual provider investigation is required; there is no blind retry/reset button. A failed later readback changes verified status to unknown. An intent is unique per close/source reference; changed source versions cannot create another adjustment under the same reference. This is not cross-period invoice deduplication.

API: `GET` or `POST /v1/closes/{close}/workpapers/{paper}/posting`. POST accepts `{action, digest, confirmation}` with action `preview`, `approve`, `execute`, or `verify`. This capability is shared by UI/API callers, but agent-only identities cannot approve writes; a verified WorkOS user session is required.

## Staging delivery

The repository currently has no remote or initial commit. Choose the GitHub repository and Cloudflare account/staging URL before activation. The GitHub `staging` environment needs its reviewer protection configured by an owner; a YAML environment name alone does not create an approval policy.

Environment variables:

- `WORKOS_CLIENT_ID`, `WORKOS_ORGANIZATION_ID`, `WORKOS_ISSUER`, `WORKOS_API_HOSTNAME`, `STAGING_URL`.
- Optional `VITE_API_BASE_URL` for a separately hosted API; empty uses the same Cloudflare origin.
- Sandbox company/name/account IDs above when enabling test posting.

Environment secrets:

- `CLOUDFLARE_ACCOUNT_ID`, least-privilege `CLOUDFLARE_API_TOKEN`, `DATABASE_URL` (TLS required).
- `QBO_SANDBOX_ACCESS_TOKEN` when enabling test posting.

The workflow runs CI, checks settings, verifies both migrations exist, then deploys frontend/API and server secrets together with Wrangler's `--secrets-file`. The temporary secret file is private and removed at step exit. No secrets enter frontend `VITE_*` variables. `/readyz` checks database access and required tables after deployment. Cloudflare account/plan/Containers availability, DNS and the WorkOS custom authentication domain must be configured and live-tested separately. No cloud resource, domain or external journal was created by the local implementation work.

## Validation boundary

Local checks use signed test JWTs, a disposable PostgreSQL database and a simulated QuickBooks HTTP transport. They prove wrong-organization/issuer/expiry denial, no development-token bypass, required approval, digest binding, durable attempt ordering, no repeated execution, successful readback and uncertain outcomes. A changed-entry regression was observed failing with its fix removed and passing after restoration. These are not successful live WorkOS or QuickBooks sessions, and no deployed staging result is claimed until those actual endpoints are tested.

References: [WorkOS React SDK](https://github.com/workos/authkit-react), [WorkOS sessions](https://workos.com/docs/authkit/sessions), [Intuit JournalEntry example](https://www.postman.com/intuit-developer/intuit-developer-quickbooks-online-accounting-api/request/4884662-4d0c72cc-0585-4485-9221-9f3551b6e493), [Cloudflare code and secret deployment](https://developers.cloudflare.com/workers/configuration/secrets/#upload-secrets-alongside-code).

## Multi-firm organization migration

Migration 011 adds registered organizations and scoped closes. Follow [firm onboarding](onboarding.md) before enabling hosted access. `WORKOS_ORGANIZATION_ID` is optional for authentication; when set it restricts login to that one firm and owns the global sandbox settings. Leave it empty for shared multi-firm SaaS, where the legacy sandbox posting flow remains disabled until per-client connections are implemented. Existing local data is not automatically assigned to any hosted firm.
