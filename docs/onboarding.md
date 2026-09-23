# Onboarding and supported flows

**Client attachment requirement:** configure [Cloudflare WAF upload scanning](cloudflare-upload-scanning.md) before releasing client PDFs. PDFs need Cloudflare; local TXT/CSV use the separate scanner service.

## Local development

1. Install Node 24 and Docker Desktop (Compose 2.22 or later); start Docker.
2. Copy `.env.example` to `.env`. Set `WORKSPACE_TOKEN` to a privately generated `openssl rand -hex 32` value. Keep provider settings empty for a fully local synthetic workflow.
3. Run `npm run dev`. Open http://localhost:5173. The API runs on port 8080, worker on 8081 and PostgreSQL on 5432. Fresh volumes apply every migration automatically.
4. On an existing volume, stop API/worker first, back up the database, and apply only unapplied migrations in numeric order. For an installation already at migration 010, the current upgrade is 011–034:

   ```sh
   docker compose stop api worker
   docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/011_organizations.sql
   docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/012_close_signoff.sql
   docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/013_firm_procedures.sql
   docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/014_client_access.sql
   docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/015_email_delivery.sql
   docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/016_close_preparation.sql
   docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/017_quickbooks_connections.sql
   docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/018_coverage_requests_attachments.sql
   docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/019_email_followup.sql
   docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/020_accruals.sql
   docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/021_ledger_selections.sql
   docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/022_ledger_search.sql
   docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/023_production_posting.sql
   docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/024_enterprise_controls.sql
   docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/025_quickbooks_entity_sync.sql
   docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/026_forwarded_evidence.sql
   docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/027_inbound_replies.sql
   docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/028_vendor_credits.sql
   docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/029_agent_checkpoint.sql
   docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/030_dependency_review.sql
   docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/031_external_alerts.sql
   docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/032_bill_corrections.sql
   docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/033_source_coverage.sql
   docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/034_bill_line_correction.sql
   docker compose up -d --build api worker
   ```

   Do not rerun earlier migrations indiscriminately. Migration 011 assigns existing closes to `local-development`; it never guesses which hosted firm owns old records.
5. Check http://localhost:8080/readyz. Open Client closes and unlock with the private token. It stays in tab memory, so a refresh requires unlocking again.

Compose is the supported no-Go-install path. For native setup use the README, install PostgreSQL plus Poppler and Tesseract (including English data), and run API and worker separately. A frontend alone does not process jobs. Never point a development worker at production.

## First evidence-to-preparation flow

1. Create a client/month under Client closes. Upload a small TXT invoice containing explicit invoice amount, full-month service dates and source terms (or PDF/CSV evidence for inspection).
2. Open Work to see its durable job. Without OpenAI configuration, proposals are labeled local and require review; arbitrary document understanding is not promised.
3. Inspect linked originals and quoted facts. Answer specific questions in Work. Confirm the supported accounting method and booking assumptions; do not confirm facts that are absent from the evidence.
4. The worker prepares an immutable draft. Inspect its exact schedule, sources and blockers, then approve or request changes with an explanation. Workpaper approval does not post to a ledger or sign off a close; the separate readiness flow below records a scoped sign-off.
5. Replace changed evidence explicitly, then prepare the revised draft. Earlier reviews remain in history and cannot approve changed evidence.
6. Export the close review package. It contains saved work and available originals, not an assertion that all books are complete. After a client accountant signs off a defined, currently unblocked scope, download the separate accountant binder and verify `checksums.sha256` before release.

Supported prepaids require USD and accrual basis; the reviewer explicitly selects equal full months or inclusive actual days and confirms booking assumptions. One prospective credit, separately reviewed multiple/retrospective credits with a client-accountant rule reference, confirmed booked-balance adjustments, and full-refund or no-refund month-boundary cancellations are supported as drafts. Fixed incurred accruals and estimate-to-actual invoice true-ups are drafts; see [accounting methods](accounting-methods.md). CSV reconciliation starts with unique exact-date, signed-amount candidates. Reviewers can confirm cited groups, net splits, bounded tolerances, transfer pairs and outstanding-item bridges under the client accountant's approved rules; the agent's unmatched-row hints never confirm a match. On a large evidence inventory, model selection narrows sources; the reviewer sees uncited files before confirmation. Account-linked source references can search captured QuickBooks ledger rows, preserving exact report locations for review.

## Ask a client and resume work

Review the consolidated request wording and recipient. Approve the request, generate a private expiring link and share it manually. The client sees only that request and can submit one reply with up to five PDF/TXT/CSV attachments. Attachments stay quarantined until their required scan (Cloudflare for PDFs, isolated ClamAV for TXT/CSV) and firm release. A matching retry returns the existing receipt; changed replies require a new request. A link holder's identity is not verified. The reply becomes source evidence and resumes the original durable job. Revoke links when no longer needed; do not put them in logs or tickets.

## Carry into the following month

Preview carry-forward, then select current approved prepaid schedules or approved procedures in separate actions. Unresolved prior jobs and unapproved drafts can also become incomplete follow-up steps in the next close; they do not automatically resume as new-month jobs. Open the next close and review fresh drafts, evidence and readiness. Carried schedule sources and predecessor approvals remain linked. Changing a predecessor invalidates dependent work. The opening amount is a scheduled balance; historical booked balances are cleared, so a new-month ledger balance and posting must be independently confirmed.

## Onboard a hosted firm

The API accepts signed WorkOS organization sessions only; it does not trust an organization ID supplied in a form, URL or header. Create the organization and its user memberships in WorkOS and require your MFA policy there. Grant the first authorized administrator `org:enroll`, `members:manage` and `clients:all`, then sign in to that organization and use **Enroll a new firm** on Client closes. The API derives the exact organization and user IDs from the verified token and records the first membership. Existing registered organizations without membership records may be claimed only by an administrator with the same enrollment grant; verify their identity and ownership before doing so. All new client closes derive ownership from the verified session.

Set backend `WORKOS_CLIENT_ID` and exact `WORKOS_ISSUER`; set frontend `VITE_WORKOS_CLIENT_ID` and `VITE_WORKOS_API_HOSTNAME`. The local token is rejected when WorkOS is configured. For multi-firm access leave `WORKOS_ORGANIZATION_ID` empty. When set, it further restricts sessions to one organization and binds the legacy sandbox configuration to that firm; shared SaaS therefore keeps that sandbox disabled until company-scoped connectors replace it.

Grant `clients:all` only to intended firm staff; assign client accounting professionals to their clients instead. Configure `members:manage` for firm access administrators, `data:manage` for retention administrators, `operations:read` for operations readers, `org:manage` for firm suspension, `workpapers:review` for reviewers, and `posting:approve` / `posting:execute` for distinct posting roles. A hosted user must have both an active recorded firm membership and the relevant WorkOS permission. Use **Firm operations and membership** to enroll/revoke exact `user_` IDs with a reason; also revoke WorkOS memberships/sessions. Sign out and sign in to the intended organization before changing firms.

To suspend a firm, an authorized administrator uses `POST /v1/closes/organization/revoke` with `org:manage`, the exact organization ID, reason and confirmation. Subsequent workspace requests and client links fail closed; workers stop claiming queued work. Already-running OCR/model requests may finish and persist results; suspension cannot recall prior provider delivery. Re-enable only through an approved administrative change after incident review. App membership revocation blocks subsequent workspace calls even if a WorkOS token has not yet expired; revoke WorkOS access too.

## Cloudflare and GitHub activation

Follow [deployment](deployment.md) and [AuthKit/sandbox setup](auth-sandbox-staging.md). Provision managed PostgreSQL with TLS, apply all numbered migrations to a fresh database, enroll synthetic test organizations, configure the GitHub staging environment and deploy API/worker/frontend together. No deployment provisions provider accounts or a database automatically.

Before admitting client data, verify with **two** hosted organizations: sign-in, same-named client/month creation, denied cross-firm URLs, isolated Work, upload/extraction, preparation, review, client reply, carry-forward and export. Restart the worker mid-job and verify one durable outcome. Then rehearse database restore and verify access isolation again. Local mocks and a successful deployment are not substitutes for these checks.

Current controls include application-enforced organization ownership, authenticated roles and membership, same-close source validation, durable jobs, malware-scanning gates, firm resource caps, deletion preview/holds and an operational snapshot. Optional webhook alert delivery is implemented; PostgreSQL row-level security, contractual backup expiry and hosted recovery verification remain outstanding. Do not treat this guide or code as enterprise certification. See [operations and recovery](operations.md).

## Troubleshooting

- **401:** unlock the local tab, or sign in again to the correct WorkOS application. Development tokens never override hosted authentication.
- **403 organization not enabled:** verify the signed session's organization and its registered enabled row; never assign foreign data to solve login errors.
- **404 close/link:** unavailable, foreign or revoked records deliberately do not reveal which condition applies. Return to your own close list or request a fresh client link.
- **503 readiness/storage:** check PostgreSQL connectivity and applied migrations before retrying.
- **Work stays queued:** check worker health/logs, database URL and organization status. Do not create duplicate jobs to bypass a stalled worker.
- **Evidence changed:** refresh, inspect the replacement and produce/review a new revision.
- **Unknown posting outcome:** reconcile with the provider before retrying. Never equate a timeout with failure to create.

For detailed feature behavior follow the README links. For what remains and its acceptance evidence see [delivery backlog](implementation-backlog.md).

## Define readiness and sign off a supported scope

On a client close, open **Close readiness → Define or update scope**. **Replace selection with latest drafts** proposes the current end of each draft revision chain; inspect and change the selection before saving. The suggestion covers only drafts already in this close, not the client's complete ledger or missing documents. Describe the included work and exclusions, and list external steps such as an independently performed ledger tie-out or prior-month follow-up. A scope containing only external steps is permitted. Mark an external step done only with its completion reference. Save the scope revision.

Readiness explains unresolved reviews/calculation blockers, active or failed work, outstanding client jobs and uncertain sandbox attempts. Resolve or explicitly cancel work as appropriate; cancellation does not establish source completeness. Refresh readiness before deciding. A hosted signer needs both `workpapers:review` and `close:signoff`. Review the defined scope, record your reason, affirm your authority and choose **Sign off supported scope**.

The decision captures organization, client/month, required scope, evidence identities, reviews, job states and sandbox receipts. A changed snapshot shows **Changed since sign-off — reopen required**. Choose **Reopen with reason**, resolve the change, review again and make a new decision. Earlier decisions and their exact snapshots remain immutable in the database and exported manifest. Editing scope cannot silently overwrite a signed decision.

Apply migration 012 (`012_close_signoff.sql`) after 011 to use this flow; stop API/worker during migration and restart both on the same code revision. Fresh Compose volumes apply both. No real firm authority is inferred from the shared local token: local decisions explicitly identify development.

For approved procedures, client-side accountant access and permission setup, follow [firm procedures and assignments](firm-procedures.md). Apply migrations 013–014 as well when upgrading an existing database.

## Approved email delivery

Use [the Resend setup and recovery guide](email-delivery.md) for per-firm verified senders, role permissions, encryption, request approval and delivery-state boundaries. Manual links remain available without provider configuration.

For the close-level evidence scope, live question list and durable job coordination, follow [Prepare this close](close-coordinator.md).

Client-specific sandbox OAuth, period reports, linked evidence and coverage planning: [QuickBooks onboarding](quickbooks-onboarding.md).

For automatic coverage status, multi-job requests, mandatory scanning and attachment release, follow [Coverage and client evidence](coverage-requests.md).

For partial-month prepaids, prospective credits, prior-balance adjustments, month-boundary cancellations and fixed incurred accruals, follow [accounting methods and accountant acceptance](accounting-methods.md).

Separate production OAuth, client grants, review, one-attempt posting and readback: [production posting](production-posting.md). Leave production credentials unset during ordinary local development.
