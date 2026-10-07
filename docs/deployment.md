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
| GitHub environment | `staging` | Protected `production` environment, **Deploy production** workflow |

Use one public origin for SPA and API initially. Leave `VITE_API_BASE_URL` empty; the browser calls the same origin. If hosting the frontend separately, set it at build time to the API origin (no `/v1`) and configure exact `FRONTEND_ORIGIN`. Keep SPA fallback for deep links. All `VITE_*` values are public.

The current Wrangler files configure one API container, one preparation worker and one on-demand scanner. A one-minute cron wakes the worker; PostgreSQL owns delivery and leases. There is **no deployed Cloudflare Queue, Workflow or R2 bucket** to provision for this path. Original documents occupy PostgreSQL storage.

## Provider choice and cost boundary

Checked against provider documentation on 2026-09-26; recheck prices and entitlements before purchase.

- **WorkOS AuthKit:** free up to 1 million monthly active users. Social login, membership and permissions already fit this app. With `AUTH_SESSION=server` the WorkOS-provided auth domain is enough, because the API holds the session; with the default `AUTH_SESSION=client`, WorkOS's browser session needs a custom authentication domain in production ($99/month). Enterprise SSO (starting at $125/connection/month) is a separate product, not a requirement for Google login. See [sign-in session](auth-sandbox-staging.md#sign-in-session). [Pricing](https://workos.com/pricing)
- **Neon:** use Launch for continuously running production. At $0.106/CU-hour, 0.25 CU running for 730 hours is about $19.35 before storage/restore; two such environments are about $38.70. Free currently includes 100 CU-hours/project/month and 0.5 GB, so a constantly polled staging database can exhaust it. Worker polling prevents assuming scale-to-zero savings. [Plans](https://neon.com/docs/introduction/plans)
- **Neon Auth:** a possible alternative, not a drop-in replacement. Its free allowance is 60,000 MAU; managed organization support is partial and MFA is on the roadmap. Migrating would require frontend, Go identity/permissions, enrollment and MCP changes. Keep WorkOS unless a concrete requirement justifies that work. [Plugins](https://neon.com/docs/auth/guides/plugins), [roadmap](https://neon.com/docs/auth/roadmap)
- **Cloudflare:** Workers Paid starts at $5/month, with container memory/disk/runtime and other usage billed separately. The operator reports $5,000 in credits; verify eligible products, expiration and balance in the account before budgeting against them. Paid Workers and credits do not establish Enterprise WAF scanning entitlement. [Container pricing](https://developers.cloudflare.com/containers/platform/pricing/)
- **Resend:** start within its free sending/domain limits; each firm configures its verified sender and credential. Pipes pricing and any provider-specific charges must be confirmed separately; free AuthKit does not establish free connector usage. [Resend pricing](https://resend.com/pricing), [Pipes](https://workos.com/docs/pipes)
- **OpenAI:** separate API usage. Set a small pilot call allowance, monitor tokens/spend and retain manual review. An application call cap is not a dollar budget or evidence of accuracy.

## Database setup

Create a fresh staging project in an appropriate region, then follow [migrations and runtime roles](../infra/postgres/migrations/README.md#fresh-hosted-database). Use a **direct Neon endpoint**, not the `-pooler` endpoint: current tenant isolation uses session-level `set_config` and caches the tenant on the connection. Transaction pooling is not compatible with that implementation, so the deploy workflows and the API and worker refuse a `-pooler` host.

Use the owner credential only for reviewed migrations; run API/worker with a separate non-owner role without superuser or BYPASSRLS. Require TLS in hosted `DATABASE_URL`. Apply all numbered migrations on a fresh database; an existing database receives only unapplied migrations. The deploy workflows apply pending migrations with the ledger-backed runner before deploying, as the owner in `MIGRATION_DATABASE_URL` (see [migrations](../infra/postgres/migrations/README.md#existing-database-upgrades)). Back up before upgrades and verify readiness afterward.

## Configure staging

1. Choose a proxied custom hostname and enable Containers in the Cloudflare account. Keep the API origin private. The edge's `workers.dev` and preview URLs are disabled intentionally.
2. Configure WorkOS allowed origins, `/login` redirect/login URI and sign-out URL for that origin. Copy the environment's exact issuer/client/hostname; leave `WORKOS_ORGANIZATION_ID` empty for multi-firm use. See [auth setup](auth-sandbox-staging.md).
3. Create GitHub environment `staging`, configure required-reviewer protection where supported, and store the settings below. Restrict the Cloudflare API token to the resources needed by this deployment.
4. Configure [Cloudflare PDF scanning and the isolated text scanner](cloudflare-upload-scanning.md). The workflow requires scanner settings. Without Enterprise malicious-upload scanning, client PDFs cannot receive a release receipt; extending the isolated scanner to this PDF path is future code, not a bypass setting.
5. Dispatch **Deploy staging** from `main` at the reviewed revision. It invokes CI on that revision, checks schema/settings, builds/deploys scanner, API/assets and worker, and requests `/readyz`. Local `npm run deploy:staging` deploys only the API/assets, so use the workflow for the complete environment.
6. Complete [staging acceptance](onboarding.md#staging-acceptance-matrix), including a real advancing worker job and two-firm access tests. Record results; successful upload/deployment alone is not acceptance.

### Environment settings

Names below match `.github/workflows/deploy.yml`. Never put secrets in `VITE_*`, committed Wrangler vars or chat.

| Purpose | GitHub variables | Cloudflare secrets (unless explicitly marked GitHub) |
| --- | --- | --- |
| Required hosting | `STAGING_URL`, verified non-secret `DATABASE_RUNTIME_ROLE`; optional `VITE_API_BASE_URL` | GitHub: `CLOUDFLARE_ACCOUNT_ID`, `CLOUDFLARE_API_TOKEN`, owner `MIGRATION_DATABASE_URL`; Cloudflare: runtime `DATABASE_URL` (direct endpoint, TLS) |
| Required auth | `WORKOS_CLIENT_ID`, `WORKOS_ISSUER`; optional single-firm `WORKOS_ORGANIZATION_ID` | — |
| Sign-in session | `AUTH_SESSION`: `client` (default, WorkOS's browser session; needs a custom `WORKOS_API_HOSTNAME` in production) or `server` (the API holds the session) | With `server`: `WORKOS_API_KEY` and `SECRETS_KEY` |
| Required scanning | `UPLOAD_SCAN_HOST` (API hostname only), `TEXT_SCANNER_URL` | `UPLOAD_SCAN_KEY` (separate random secret, at least 32 characters) |
| Encrypted connectors/email | — | `SECRETS_KEY` (32 random bytes, 64 hex characters; same on API/worker, backed up separately) |
| Welcome emails (optional) | `PLATFORM_EMAIL_FROM`, Krith's sender on a domain verified in Resend ([sign-in](sign-in.md#welcome-emails)) | Queue worker: `PLATFORM_EMAIL_KEY`, a send-only Resend key for that domain (set both or neither) |
| OpenAI proposals | `OPENAI_MODEL` | `OPENAI_API_KEY` (configure both or neither; API and worker) |
| Sandbox QuickBooks OAuth | `QBO_CLIENT_ID`, `QBO_REDIRECT_URI` | `QBO_CLIENT_SECRET`; optional `QBO_WEBHOOK_VERIFIER` |
| Production QuickBooks OAuth | `QBO_PRODUCTION_CLIENT_ID`, `QBO_PRODUCTION_REDIRECT_URI` | `QBO_PRODUCTION_CLIENT_SECRET` |
| Xero OAuth | `XERO_CLIENT_ID`, `XERO_REDIRECT_URI` | `XERO_CLIENT_SECRET`; optional `XERO_WEBHOOK_KEY` |
| WorkOS directory: teammates, invitations, background role checks, optional Drive | — | `WORKOS_API_KEY` (API and worker; without it a hosted firm's delegated closes and monthly hand-overs wait) |
| Contact-page forwarding (edge only) | `CONTACT_FORWARD_ADDRESS`, a destination address verified in the zone's Email Routing; the edge's `send_email` binding `CONTACT_EMAIL` is in `wrangler.jsonc` ([public site](marketing-site.md#the-contact-form)) | — |
| Optional inbound email | `EMAIL_INTAKE_ADDRESS` | `EMAIL_INTAKE_KEY` (independent secret, at least 32 characters) |
| Optional external alerts | — | Worker-only `OPERATIONS_ALERT_WEBHOOK_URL`, optional `OPERATIONS_ALERT_WEBHOOK_TOKEN` |
| Legacy single-firm sandbox journal | `QBO_SANDBOX_REALM_ID`, `QBO_SANDBOX_ENTITY`, `QBO_SANDBOX_PREPAID_ACCOUNT_ID`, `QBO_SANDBOX_EXPENSE_ACCOUNT_ID` | `QBO_SANDBOX_ACCESS_TOKEN` |
| Optional hosted MCP (OAuth) | `WORKOS_AUTHKIT_DOMAIN` (HTTPS origin only) and `MCP_RESOURCE_URL` (exactly `/mcp` on the API origin), set together; optional `MCP_ALLOWED_ORIGINS` for browser-based clients | — |
| Posting kinds (API only) | Optional `PRODUCTION_POSTING_KINDS` (default `prepaid`) and `XERO_POSTING_KINDS` (default none); enable a kind only after its sandbox check | — |

The workflow derives `PUBLIC_APP_URL` and `FRONTEND_ORIGIN` from `STAGING_URL`, and frontend WorkOS values from the auth variables. API and worker receive both QBO environment credentials and Xero credentials for scheduled refresh. Resend and Stripe credentials are entered through their scoped in-app settings and stored encrypted, not as frontend/GitHub variables. Leave legacy global sandbox credentials empty for multi-firm testing; use per-client connectors.

## Cloudflare service credentials

Cloudflare Worker secrets are the authoritative store for service credentials. Routine production/staging deploys read names-only secret metadata and update non-secret configuration; they never copy credentials from GitHub, supply empty credential values or reseed Codex login. The configuration-only `--secrets-file` flag is intentional: pinned Wrangler preserves existing secret bindings with this flag. Do not remove it. Refreshed Codex login remains in the private context-machine Durable Object across restarts and ordinary redeploys.

GitHub retains `CLOUDFLARE_API_TOKEN`, `CLOUDFLARE_ACCOUNT_ID` and `MIGRATION_DATABASE_URL` for deployment/migrations. Set `DATABASE_RUNTIME_ROLE` to the verified role from the runtime connection; migrations grant it privileges without reading its password. Temporary database-provisioning credentials are administration inputs and must be deleted after use. Per-client connector credentials entered through the product remain encrypted in the application database; this change governs service credentials, not that product data.

On a trusted operator computer, run `npm ci` and authenticate Wrangler for the target account (`npx wrangler login`, or scoped deployment credentials). Confirm the account before any write. The target Workers must already exist; for a fresh environment, bootstrap the reviewed Worker code with its Wrangler config before installing credentials and dispatching the normal workflow. The installer refuses missing/inaccessible destinations and interactive stdin:

```sh
# The private file must be outside the repository, with mode 0600.
node scripts/cloudflare-credentials.mjs CONTEXT_MACHINE_DATABASE_URL production < /path/to/private/reader-url
node scripts/cloudflare-credentials.mjs OPENAI_API_KEY production < /path/to/private/openai-key
# Use staging explicitly to target staging Workers.
```

The installer validates values, checks all destinations before writing, sends JSON over stdin and verifies secret names afterward. It prints only credential names and destinations; raw provider error output is suppressed. A later provider/write failure can leave a shared rotation partially installed: correct the failure and rerun with the same value before resuming service traffic. Keep an operator-controlled credential copy until installation succeeds; Cloudflare does not return stored Worker secret values. Never put credentials in command arguments, chat, public `VITE_*` settings or committed files. Shared token/DB/scanner rotations must be coordinated across their services; stop affected traffic, install, redeploy and verify readiness before resuming. A running container retains its startup environment, so secret installation alone is not rotation acceptance; verify a fresh container start as described in [Cloudflare's environment documentation](https://developers.cloudflare.com/containers/examples/env-vars-and-secrets/). Scanner deployment metadata drift deliberately triggers a reviewed rollout on the next push, while unchanged code and metadata reuse the existing scanner image. Encryption-key rotation requires the transaction procedure in [operations](operations.md#offline-encryption-key-rotation), not just changing a Worker secret.

| Worker | Credential scope |
| --- | --- |
| API (`close-production`) | Runtime DB, scanner/email keys, encryption key, WorkOS API key, connector secrets, optional OpenAI key, shared context-machine token |
| Queue (`close-queue-production`) | Runtime DB, scanner/encryption/WorkOS keys, connector secrets, optional OpenAI key, shared context-machine token, optional alert webhook credentials |
| Scanner (`close-upload-scanner-production`) | `UPLOAD_SCAN_KEY` only |
| Context machine (`close-context-machine-production`) | Read-only DB, shared token, OpenAI/Anthropic API keys, Codex managed login or account token, Claude OAuth token |

The helper's credential allowlists enforce these scopes. API mode and context-machine mode may retain credentials for both routes in Cloudflare; Worker forwarding exposes only the active route to app containers. Explicit standby removes the research service bindings and hides the token, without deleting installed credentials. Full runtime readiness validates database isolation and selected CLI configuration. Names-only preflight cannot verify stored values or live provider entitlement.

### Inspect and remove obsolete bindings

Old deploys installed empty credentials as real Cloudflare bindings. During cutover, remove only placeholders whose emptiness is established from the old deployment/transfer record, before activating optional features. Secret-name metadata cannot distinguish an empty legacy binding from a usable credential; do not delete a credential merely because its name appears here.

The main-only **Inspect or remove Cloudflare credential bindings** workflow uses only Cloudflare deployment access. With an empty `plan`, it performs no writes and exports `cloudflare-credential-metadata`, containing names and current deployment IDs, never values. For an explicitly reviewed removal, supply a non-secret JSON plan mapping Worker scope names (`edge`, `queue`, `scanner`, `context-machine`) to the inspected `deployment` and `remove` credential-name list. Core database, scanner, encryption, identity, intake and service-authentication credentials are protected from deletion by this job. Inspection brackets secret-name reads with matching deployment revisions. Every scope/name/revision is checked before the first deletion; a secret installation or deployment since inspection refuses the operation. The job shares deployment concurrency. Avoid dashboard edits during it. A partial failure requires fresh inspection and a reviewed plan for the remaining names, not blind retries. No credential values belong in workflow inputs. Staging must have its own provisioned Workers and credentials before inspection.

```json
{"context-machine":{"deployment":"INSPECTED_DEPLOYMENT_ID","remove":["CODEX_AUTH_JSON"]}}
```

This example is deletion, not installation: use it only when that managed-login binding is confirmed obsolete/empty. Installing a new token/login remains the stdin helper above. Legacy cleanup is a required cutover step before removing GitHub runtime copies or enabling research; successful name metadata alone is not activation acceptance.


## Google and email onboarding

Google sign-in identifies a person; it does not grant Drive or Gmail access. Ask for source access only when that person chooses it.

The implemented Drive picker uses WorkOS Pipes and `drive.readonly`, importing one selected PDF/TXT/CSV at a time. Configure that scope honestly; it is a restricted Google scope despite the product selecting one file. A narrower Google Picker + `drive.file` integration is recommended future work, not a configuration-only change. Google Docs export, automatic folder sync and Gmail mailbox sync are not implemented. [Current Drive flow](source-sync-and-forwarding.md#google-drive-evidence-optional), [Google scope guidance](https://developers.google.com/workspace/drive/api/guides/api-specific-auth)

Start client communication with [Resend](email-delivery.md), secure request links and optional Cloudflare Email Routing. Use a dedicated inbound subdomain to avoid replacing the firm's Google Workspace MX records. Replies are held for verification and release; inbound reply attachments use the secure request link. Forwarded-document intake is a distinct reviewed/scanned flow. Gmail reading requires separate OAuth consent and restricted-scope verification/security requirements; Pipes handles credential plumbing, not those obligations or synchronization. [Gmail scopes](https://developers.google.com/workspace/gmail/api/auth/scopes), [Cloudflare subdomains](https://developers.cloudflare.com/email-service/configuration/subdomains/)

## Production and remaining deployment work

**Deploy production** (`.github/workflows/deploy-production.yml`) runs on every push to `main` that changes more than documentation (and by hand), and mirrors the staging workflow against GitHub environment `production`, using the `production` Wrangler environments (`close-production`, `close-queue-production`, `close-upload-scanner-production`). Unlike staging it does not run CI again: a push deploys only when the pull request it merged passed CI (found through the merge commit's second parent or GitHub's commit-to-pull-request link), and the image and app builds then compile what was merged. A hand run skips that check and always redeploys the scanner. A push redeploys the scanner only when its current Cloudflare deployment version (which changes on direct secret rotation), `cmd/scanner`, `internal/scanning`, the scanner Worker or Dockerfile, the Go modules, the lockfile or this workflow changed since the scanner was last deployed (recorded in an Actions cache entry each time it is). Key rotation therefore needs no hand run. Do not rename or repoint the staging resources to production. It reads `PRODUCTION_URL` instead of `STAGING_URL`; the other settings match the table above.

`close-production` serves only on the custom domain `getkrith.com`, declared in `workers/edge/wrangler.jsonc` (`routes`, `custom_domain`), so every deploy keeps it; Cloudflare manages its DNS record and certificate. Its `workers.dev` URL is off (`workers_dev: false`), so the WAF and zone rules on `getkrith.com` cannot be bypassed through it. `www.getkrith.com` redirects to it (a zone redirect rule). Leave `UPLOAD_SCAN_HOST` empty until the WAF scan is set up there: the edge then refuses every PDF scan, so client PDFs stay quarantined while TXT/CSV use the text scanner (`TEXT_SCANNER_URL` = `https://close-upload-scanner-production.<your-workers-subdomain>.workers.dev/scan`). The deploy passes no `--route`: a command-line route would replace the configured custom domain.

Production moved to `https://getkrith.com` on 2026-10-04 in this order, so sign-in never pointed at an address WorkOS did not know; follow it again for any later domain:

1. Check `https://getkrith.com/readyz` answers 200.
2. In WorkOS production, add the redirect URIs `https://getkrith.com/login` and `https://getkrith.com/admin/` (and `https://getkrith.com/v1/auth/callback` for `AUTH_SESSION=server`), the sign-out URIs `https://getkrith.com`, `https://getkrith.com/` and `https://getkrith.com/admin/`, and `https://getkrith.com` as an allowed web origin. Keep the `workers.dev` entries until the move is verified.
3. Update every URL setting that names the old host in the same change, since they are set separately: `PRODUCTION_URL=https://getkrith.com`; `VITE_API_BASE_URL` cleared (same origin) or set to `https://getkrith.com`, since it is compiled into the frontend; `MCP_RESOURCE_URL=https://getkrith.com/mcp` if hosted MCP is on (the deploy refuses any other value once `PRODUCTION_URL` changes); and `QBO_REDIRECT_URI`, `QBO_PRODUCTION_REDIRECT_URI` and `XERO_REDIRECT_URI` on the new host, each registered with Intuit or Xero first. Then run Deploy production. (On 2026-10-04 production set none of `VITE_API_BASE_URL`, the MCP or the connector settings.)
4. Sign in and out of the workspace (and `/admin/` with server sessions). Before turning the old host off, make sure nothing outside still points at it: provider webhooks (Resend, QuickBooks, Xero) must be moved to the new host, and unexpired client request links on the old host must be replaced, since links already sent by email keep their host. (On 2026-10-04 Resend had no webhooks, and production had no email connections and no open request links.) Then set `workers_dev` to `false` for production and remove the `workers.dev` entries from WorkOS.

Once the WAF scan is set up on `getkrith.com` ([upload scanning](cloudflare-upload-scanning.md)), set `UPLOAD_SCAN_HOST=getkrith.com`. Before a live release:

1. Every merge to `main` migrates and deploys production, so review before merging and merge only after CI passes; a migration must work with the code already running. A merge that main has moved past since CI ran is compiled but not re-tested, so update a pull request whose CI is older than a conflicting change.
2. Provision a separate production database/auth environment, matching encryption-key backup, sender, connector apps and approved client grants. Keep test and real-company credentials apart.
3. Verify runtime RLS, restore a backup into isolation with workers disabled, reconcile external outcomes, and validate alert routing and retention. See [operations](operations.md).
4. Validate accountant-approved examples and each enabled provider write in the sandbox first, then authorize only the intended production methods/clients.

The edge forwards `/mcp` and OAuth discovery to the API, and Compose, the API container and both deploy workflows pass the MCP settings and `PRODUCTION_POSTING_KINDS` and `XERO_POSTING_KINDS` (see the table above). Hosted MCP still needs the WorkOS Connect configuration in [MCP](mcp.md#hosted-oauth-setup); a posting kind still needs its sandbox check in [posting activation](production-posting.md#enabling-accrual-loan-owner-and-stripe-fee-posting).

Local Compose is the supported development path. `npm run dev:cloudflare` checks the edge/container adapter with separately supplied local settings; it does not emulate WAF scan entitlement, hosted identity, external providers or the complete staging deployment.
