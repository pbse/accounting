# Staging and production setup

The recommended starting stack is Cloudflare Workers/Containers, WorkOS AuthKit, Neon PostgreSQL, Resend and OpenAI. Keep the existing integrations; replacing auth is not necessary to use Neon for the database. [Onboarding](onboarding.md) covers firm/client setup and acceptance tests.

## Environment layout

| Resource | Staging | Production |
| --- | --- | --- |
| Public origin | Dedicated staging hostname | Separate production hostname |
| API, worker, scanner | Staging Workers/Containers | Separately named Workers/Containers |
| PostgreSQL | Dedicated Neon project with synthetic data | Dedicated project with restricted runtime role and backups |
| WorkOS | Staging environment | Production environment |
| QuickBooks | Sandbox companies | Explicitly connected client production companies |
| Email/model credentials | Test sender/recipients and separate model project | Verified firm sender and separately monitored model usage |
| GitHub environment | `staging` | Protected `production` environment; workflow still to implement |

Use one public origin for SPA and API initially. Leave `VITE_API_BASE_URL` empty; the browser calls the same origin. If hosting the frontend separately, set it at build time to the API origin (no `/v1`) and configure exact `FRONTEND_ORIGIN`. Keep SPA fallback for deep links. All `VITE_*` values are public.

The current Wrangler files configure one API container, one preparation worker and one on-demand scanner. A one-minute cron wakes the worker; PostgreSQL owns delivery and leases. There is **no deployed Cloudflare Queue, Workflow or R2 bucket** to provision for this path. Original documents occupy PostgreSQL storage.

## Provider choice and cost boundary

Checked against provider documentation on 2026-09-26; recheck prices and entitlements before purchase.

- **WorkOS AuthKit:** free up to 1 million monthly active users. Social login, membership and permissions already fit this app. Start with the WorkOS-provided auth domain; custom domains ($99/month) and enterprise SSO (starting at $125/connection/month) are separate products, not requirements for Google login. [Pricing](https://workos.com/pricing)
- **Neon:** use Launch for continuously running production. At $0.106/CU-hour, 0.25 CU running for 730 hours is about $19.35 before storage/restore; two such environments are about $38.70. Free currently includes 100 CU-hours/project/month and 0.5 GB, so a constantly polled staging database can exhaust it. Worker polling prevents assuming scale-to-zero savings. [Plans](https://neon.com/docs/introduction/plans)
- **Neon Auth:** a possible alternative, not a drop-in replacement. Its free allowance is 60,000 MAU; managed organization support is partial and MFA is on the roadmap. Migrating would require frontend, Go identity/permissions, enrollment and MCP changes. Keep WorkOS unless a concrete requirement justifies that work. [Plugins](https://neon.com/docs/auth/guides/plugins), [roadmap](https://neon.com/docs/auth/roadmap)
- **Cloudflare:** Workers Paid starts at $5/month, with container memory/disk/runtime and other usage billed separately. The operator reports $5,000 in credits; verify eligible products, expiration and balance in the account before budgeting against them. Paid Workers and credits do not establish Enterprise WAF scanning entitlement. [Container pricing](https://developers.cloudflare.com/containers/platform/pricing/)
- **Resend:** start within its free sending/domain limits; each firm configures its verified sender and credential. Pipes pricing and any provider-specific charges must be confirmed separately; free AuthKit does not establish free connector usage. [Resend pricing](https://resend.com/pricing), [Pipes](https://workos.com/docs/pipes)
- **OpenAI:** separate API usage. Set a small pilot call allowance, monitor tokens/spend and retain manual review. An application call cap is not a dollar budget or evidence of accuracy.

## Database setup

Create a fresh staging project in an appropriate region, then follow [migrations and runtime roles](../infra/postgres/migrations/README.md#fresh-hosted-database). Use a **direct Neon endpoint**, not the `-pooler` endpoint: current tenant isolation uses session-level `set_config` and caches the tenant on the connection. Transaction pooling is not compatible with that implementation.

Use the owner credential only for reviewed migrations; run API/worker with a separate non-owner role without superuser or BYPASSRLS. Require TLS in hosted `DATABASE_URL`. Apply all numbered migrations on a fresh database; an existing database receives only unapplied migrations. CI and deployment do not maintain a migration ledger or apply hosted migrations for you. Back up before upgrades and verify readiness afterward.

## Configure staging

1. Choose a proxied custom hostname and enable Containers in the Cloudflare account. Keep the API origin private. The edge's `workers.dev` and preview URLs are disabled intentionally.
2. Configure WorkOS allowed origins, `/login` redirect/login URI and sign-out URL for that origin. Copy the environment's exact issuer/client/hostname; leave `WORKOS_ORGANIZATION_ID` empty for multi-firm use. See [auth setup](auth-sandbox-staging.md).
3. Create GitHub environment `staging`, configure required-reviewer protection where supported, and store the settings below. Restrict the Cloudflare API token to the resources needed by this deployment.
4. Configure [Cloudflare PDF scanning and the isolated text scanner](cloudflare-upload-scanning.md). The workflow requires scanner settings. Without Enterprise malicious-upload scanning, client PDFs cannot receive a release receipt; extending the isolated scanner to this PDF path is future code, not a bypass setting.
5. Dispatch **Deploy staging** from `main` at the reviewed revision. It invokes CI on that revision, checks schema/settings, builds/deploys scanner, API/assets and worker, and requests `/readyz`. Local `npm run deploy:staging` deploys only the API/assets, so use the workflow for the complete environment.
6. Complete [staging acceptance](onboarding.md#staging-acceptance-matrix), including a real advancing worker job and two-firm access tests. Record results; successful upload/deployment alone is not acceptance.

### GitHub environment settings

Names below match `.github/workflows/deploy.yml`. Never put secrets in `VITE_*`, committed Wrangler vars or chat.

| Purpose | Variables | Secrets |
| --- | --- | --- |
| Required hosting | `STAGING_URL`; optional `VITE_API_BASE_URL` | `CLOUDFLARE_ACCOUNT_ID`, `CLOUDFLARE_API_TOKEN`, runtime `DATABASE_URL` |
| Required auth | `WORKOS_CLIENT_ID`, `WORKOS_API_HOSTNAME`, `WORKOS_ISSUER`; optional single-firm `WORKOS_ORGANIZATION_ID` | — |
| Required scanning | `UPLOAD_SCAN_HOST` (API hostname only), `TEXT_SCANNER_URL` | `UPLOAD_SCAN_KEY` (separate random secret, at least 32 characters) |
| Encrypted connectors/email | — | `SECRETS_KEY` (32 random bytes, 64 hex characters; same on API/worker, backed up separately) |
| OpenAI proposals | `OPENAI_MODEL` | `OPENAI_API_KEY` (configure both or neither; API and worker) |
| Sandbox QuickBooks OAuth | `QBO_CLIENT_ID`, `QBO_REDIRECT_URI` | `QBO_CLIENT_SECRET`; optional `QBO_WEBHOOK_VERIFIER` |
| Production QuickBooks OAuth | `QBO_PRODUCTION_CLIENT_ID`, `QBO_PRODUCTION_REDIRECT_URI` | `QBO_PRODUCTION_CLIENT_SECRET` |
| Xero OAuth | `XERO_CLIENT_ID`, `XERO_REDIRECT_URI` | `XERO_CLIENT_SECRET` |
| Optional selected-file Drive | — | API-only `WORKOS_API_KEY` |
| Optional inbound email | `EMAIL_INTAKE_ADDRESS` | `EMAIL_INTAKE_KEY` (independent secret, at least 32 characters) |
| Optional external alerts | — | Worker-only `OPERATIONS_ALERT_WEBHOOK_URL`, optional `OPERATIONS_ALERT_WEBHOOK_TOKEN` |
| Legacy single-firm sandbox journal | `QBO_SANDBOX_REALM_ID`, `QBO_SANDBOX_ENTITY`, `QBO_SANDBOX_PREPAID_ACCOUNT_ID`, `QBO_SANDBOX_EXPENSE_ACCOUNT_ID` | `QBO_SANDBOX_ACCESS_TOKEN` |

The workflow derives `PUBLIC_APP_URL` and `FRONTEND_ORIGIN` from `STAGING_URL`, and frontend WorkOS values from the auth variables. API and worker receive both QBO environment credentials and Xero credentials for scheduled refresh. Resend and Stripe credentials are entered through their scoped in-app settings and stored encrypted, not as frontend/GitHub variables. Leave legacy global sandbox credentials empty for multi-firm testing; use per-client connectors.

## Google and email onboarding

Google sign-in identifies a person; it does not grant Drive or Gmail access. Ask for source access only when that person chooses it.

The implemented Drive picker uses WorkOS Pipes and `drive.readonly`, importing one selected PDF/TXT/CSV at a time. Configure that scope honestly; it is a restricted Google scope despite the product selecting one file. A narrower Google Picker + `drive.file` integration is recommended future work, not a configuration-only change. Google Docs export, automatic folder sync and Gmail mailbox sync are not implemented. [Current Drive flow](source-sync-and-forwarding.md#google-drive-evidence-optional), [Google scope guidance](https://developers.google.com/workspace/drive/api/guides/api-specific-auth)

Start client communication with [Resend](email-delivery.md), secure request links and optional Cloudflare Email Routing. Use a dedicated inbound subdomain to avoid replacing the firm's Google Workspace MX records. Replies are held for verification and release; inbound reply attachments use the secure request link. Forwarded-document intake is a distinct reviewed/scanned flow. Gmail reading requires separate OAuth consent and restricted-scope verification/security requirements; Pipes handles credential plumbing, not those obligations or synchronization. [Gmail scopes](https://developers.google.com/workspace/gmail/api/auth/scopes), [Cloudflare subdomains](https://developers.cloudflare.com/email-service/configuration/subdomains/)

## Production and remaining deployment work

**There is no production workflow/configuration yet.** Do not rename or repoint the staging resources to production. Before a live release:

1. Add separate production Worker/container names, hostname, protected GitHub environment, secrets, concurrency and a reviewed migration/release procedure. Promote a reviewed revision after staging acceptance.
2. Provision a separate production database/auth environment, matching encryption-key backup, sender, connector apps and approved client grants. Keep test and real-company credentials apart.
3. Verify runtime RLS, restore a backup into isolation with workers disabled, reconcile external outcomes, and validate alert routing and retention. See [operations](operations.md).
4. Validate accountant-approved examples and each enabled provider write in the sandbox first, then authorize only the intended production methods/clients.

Two backend features need deployment wiring before they can be advertised on Cloudflare: `/mcp` currently returns 404 at the edge, OAuth discovery is not forwarded, and `WORKOS_AUTHKIT_DOMAIN`, `MCP_RESOURCE_URL`, `MCP_ALLOWED_ORIGINS` are not passed to the container. Also, `PRODUCTION_POSTING_KINDS` and `XERO_POSTING_KINDS` are read by Go but are not forwarded by Compose/edge/deployment; the deployed defaults remain prepaid-only for QuickBooks and no enabled journal kinds for Xero. These require code/configuration changes, not merely adding GitHub secrets. [MCP](mcp.md#deployment-boundary), [posting activation](production-posting.md#enabling-accrual-loan-owner-and-stripe-fee-posting)

Local Compose is the supported development path. `npm run dev:cloudflare` checks the edge/container adapter with separately supplied local settings; it does not emulate WAF scan entitlement, hosted identity, external providers or the complete staging deployment.
