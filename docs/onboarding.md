# Onboarding and supported flows

**Client attachment requirement:** configure [Cloudflare WAF upload scanning](cloudflare-upload-scanning.md) before releasing client PDFs. PDFs need Cloudflare; local TXT/CSV use the separate scanner service.

## Local development

1. Install Node 24 and Docker with Compose 2.22 or later; start Docker.
2. Copy `.env.example` to `.env` without overwriting an existing file. Generate separate private values for `WORKSPACE_TOKEN` and `UPLOAD_SCAN_KEY` (for each, `openssl rand -hex 32`). Keep provider settings empty for a fully local synthetic workflow.
3. From the repository root run `npm run dev`. Open http://localhost:5173. The API is on localhost:8080, worker on localhost:8081 and PostgreSQL on localhost:5432. A fresh Compose volume applies all numbered migrations. Existing volumes are not upgraded automatically: follow [database migration instructions](../infra/postgres/migrations/README.md#existing-database-upgrades).
4. Check http://localhost:8080/readyz. Open Client closes and unlock with the private token from `.env`; it remains in tab memory. Create a synthetic client and month.
5. For real model interpretation, set both `OPENAI_API_KEY` and `OPENAI_MODEL` privately in `.env`, then recreate API/worker. Both processes have model features. Without them, local proposals recognize labeled fields and require review.

Compose includes Poppler/Tesseract for staff-uploaded PDF extraction and an isolated scanner for TXT/CSV client attachments. Client PDF **release** still needs real Cloudflare scanning; local extraction is not a clean scan receipt. Use staging for the complete client PDF flow. Provider OAuth, real sending, separate human roles and multi-firm isolation need hosted identity; the shared local token does not test them.

For native setup use the README; Go does not load `.env` automatically. Never point a development worker at production. Stop with Ctrl-C; `docker compose down` preserves the database volume. Do not delete volumes to upgrade an existing workspace.

## First evidence-to-preparation flow

1. Create a client/month under Client closes. Upload a small TXT invoice containing explicit invoice amount, full-month service dates and source terms (or PDF/CSV evidence for inspection).
2. Open Work to see its durable job. Without OpenAI configuration, proposals are labeled local and require review; arbitrary document understanding is not promised.
3. Inspect linked originals and quoted facts. Answer specific questions in Work. Confirm the supported accounting method and booking assumptions; do not confirm facts that are absent from the evidence.
4. The worker prepares an immutable draft. Inspect its exact schedule, sources and blockers, then approve or request changes with an explanation. Workpaper approval does not post to a ledger or sign off a close; the separate readiness flow below records a scoped sign-off.
5. Replace changed evidence explicitly, then prepare the revised draft. Earlier reviews remain in history and cannot approve changed evidence.
6. Export the close review package. It contains saved work and available originals, not an assertion that all books are complete. After a client accountant signs off a defined, currently unblocked scope, download the separate accountant binder and verify `checksums.sha256` before release.

Supported prepaids require the client's home currency and accrual basis; the reviewer explicitly selects equal full months or inclusive actual days and confirms booking assumptions. One prospective credit, separately reviewed multiple/retrospective credits with a client-accountant rule reference, confirmed booked-balance adjustments, and full-refund or no-refund month-boundary cancellations are supported as drafts. Fixed incurred accruals and estimate-to-actual invoice true-ups are drafts; see [accounting methods](accounting-methods.md). CSV reconciliation starts with unique exact-date, signed-amount candidates. Reviewers can confirm cited groups, net splits, bounded tolerances, transfer pairs and outstanding-item bridges under the client accountant's approved rules; the agent's unmatched-row hints never confirm a match. On a large evidence inventory, model selection narrows sources; the reviewer sees uncited files before confirmation. Account-linked source references can search captured QuickBooks ledger rows, preserving exact report locations for review.

## Ask a client and resume work

Review the consolidated request wording and recipient. Approve the request, generate a private expiring link and share it manually. The client sees only that request and can submit one reply with up to five PDF/TXT/CSV attachments. Attachments stay quarantined until their required scan (Cloudflare for PDFs, isolated ClamAV for TXT/CSV) and firm release. A matching retry returns the existing receipt; changed replies require a new request. A link holder's identity is not verified. The reply becomes source evidence and resumes the original durable job. Revoke links when no longer needed; do not put them in logs or tickets.

## Carry into the following month

Preview carry-forward, then select current approved prepaid schedules or approved procedures in separate actions. Unresolved prior jobs and unapproved drafts can also become incomplete follow-up steps in the next close; they do not automatically resume as new-month jobs. Open the next close and review fresh drafts, evidence and readiness. Carried schedule sources and predecessor approvals remain linked. Changing a predecessor invalidates dependent work. The opening amount is a scheduled balance; historical booked balances are cleared, so a new-month ledger balance and posting must be independently confirmed.

## Get started

Work opens with **Get started**, the first-client journey: enroll the firm, add a team member, open a client close, connect the client's books, confirm the period and rules, and receive the first prepared draft. `GET /v1/closes/setup` reads each step from the firm's own records, so progress is kept by doing the work and nothing else is stored:

- **Team:** members enrolled besides you. Invite them by email in the **Security center** ([below](#invite-by-email)).
- **Client:** closes you can open. The next steps are about your newest such close.
- **Books:** that client has an active Xero connection, or its QuickBooks captures read an active connection (production is read before sandbox, so an active sandbox company does not count while the production one needs reconnecting). When neither provider is configured on the server, the step says to upload ledger reports instead. The step also completes when the close's current uploaded exports carry a historical exports label (below), and then says the close uses labeled exports, not a live ledger connection.
- **Period and rules:** the close's month, and whether an enabled approved procedure applies to its client for any kind, including the accounting basis (a client override wins, and a disabled one blocks the firm's).
- **First draft:** any draft in a close you can open; until then the step points to that close's Evidence tab. Once the checklist's close has a draft, the step also says how many minutes its first draft came after the first document added there (a procedure document does not count, nor one added later), and how many minutes of tracked or recorded human time the close had before it; `GET /v1/closes/setup` returns both as `first_draft`. This is an upload-to-draft estimate, not the PRD's time from full access and usable evidence, and it excludes our own team's assistance; record those in the pilot record.

Each open step links to the control that completes it. A member who is not enrolled sees **Enroll a new firm** instead, even while the rest of Work cannot load. **What you can do here** lists your permissions in plain words, and "only the clients assigned to you" when you lack `clients:all`.

**Reconnect** lists your clients' QuickBooks or Xero connections that need authorizing again, with the provider's error and a link to the panel that reconnects it: production QuickBooks on the close's Settings tab, sandbox QuickBooks and Xero on Evidence. Reconnecting (disconnect, connect and confirm) needs `integrations:manage`; without it the list says who can. Afterwards this and last month's open closes are captured again without waiting for the daily refresh: QuickBooks on the next scheduler pass (every five minutes), Xero on the next sweep once 30 minutes have passed since the close's last scheduled attempt. A new QuickBooks capture sends the agent's proposals that were waiting on the ledger back for a fresh reading. Other jobs and blockers stay as they are, and posting grants belong to the old connection, so they must be granted again.

## Onboard a hosted firm

The API accepts signed WorkOS organization sessions only; it does not trust an organization ID supplied in a form, URL or header. Create the organization and its user memberships in WorkOS and require your MFA policy there. Grant the first authorized administrator `org:enroll`, `members:manage` and `clients:all`, then sign in to that organization and use **Enroll a new firm** on Client closes. The API derives the exact organization and user IDs from the verified token and records the first membership. Existing registered organizations without membership records may be claimed only by an administrator with the same enrollment grant; verify their identity and ownership before doing so. All new client closes derive ownership from the verified session.

Set backend `WORKOS_CLIENT_ID` and exact `WORKOS_ISSUER`; set frontend `VITE_WORKOS_CLIENT_ID` and `VITE_WORKOS_API_HOSTNAME`. The local token is rejected when WorkOS is configured. For multi-firm access leave `WORKOS_ORGANIZATION_ID` empty. When set, it further restricts sessions to one organization and binds the legacy sandbox configuration to that firm; shared SaaS therefore keeps that sandbox disabled until company-scoped connectors replace it.

Grant `clients:all` only to intended firm staff; assign client accounting professionals to their clients instead. Configure `members:manage` for firm access administrators, `data:manage` for retention administrators, `operations:read` for operations readers, `org:manage` for firm suspension, `workpapers:review` for reviewers, and `posting:approve` / `posting:execute` for distinct posting roles, and `executive:read` alone for a client's executives, who read only their assigned clients' published reports and cash views ([executive access](management-reports.md#executive-access)). A hosted user must have both an active recorded firm membership and the relevant WorkOS permission. Use **Firm operations and membership** to invite members by email ([below](#invite-by-email)), or to enroll or revoke an existing WorkOS user by exact `user_` ID with a reason; also revoke WorkOS memberships/sessions. Sign out and sign in to the intended organization before changing firms.

### Invite by email

With the server's `WORKOS_API_KEY`, an administrator with `clients:all` and `members:manage` invites a teammate by email in the **Security center**, under **Firm operations and membership** (migration 075). The role list is the firm's WorkOS roles (`GET /authorization/organizations/{id}/roles`), each with its permissions in plain words (a permission the application does not check is shown by name). Only roles whose every permission the administrator holds are offered or accepted (a role WorkOS lists without permissions is not), so nobody can invite into more authority than they have.

Sending reserves the address, then WorkOS creates the invitation (`POST /user_management/invitations`, with the role and the administrator as inviter) and emails it. A refusal from WorkOS releases the address. An unconfirmed send keeps the address reserved: opening the list looks in WorkOS for exactly one invitation to that address in this firm, with the same role and inviter, created around the send, and adopts it; absence or ambiguity settles nothing, and revoking the reservation releases the address. An address with an open invitation cannot be invited again until it is revoked or expires. **Revoke** withdraws the app's authority first, then revokes the invitation in WorkOS and says if WorkOS did not confirm; a send revoked before WorkOS confirmed it is revoked in WorkOS when its confirmation arrives or when the list finds it.

The invitee joins on their first request after accepting. The app looks them up only while the firm has a pending invitation and only if it has never recorded a decision about them: their WorkOS address must be verified and match a pending, unexpired invitation, WorkOS must show that invitation accepted by them for this firm, and their WorkOS membership in the firm must be active. Then, holding the firm's row (as an administrator's member decision does) and the invitation's, and checking again that the invitation is unexpired and that no decision about them exists, their membership is recorded under the inviter with the invitation's reason. Any doubt, or any WorkOS error, leaves them out. A member the firm revoked is never re-enrolled by an invitation; restore them explicitly. Client assignments are separate: a role without `clients:all` sees no clients until they are assigned.

To suspend a firm, an authorized administrator uses `POST /v1/closes/organization/revoke` with `org:manage`, the exact organization ID, reason and confirmation. Subsequent workspace requests and client links fail closed; workers stop claiming queued work. Already-running OCR/model requests may finish and persist results; suspension cannot recall prior provider delivery. Re-enable only through an approved administrative change after incident review. App membership revocation blocks subsequent workspace calls even if a WorkOS token has not yet expired; revoke WorkOS access too.

## Cloudflare and GitHub activation

Follow [deployment](deployment.md) and [AuthKit/sandbox setup](auth-sandbox-staging.md). Provision managed PostgreSQL with TLS, apply all numbered migrations to a fresh database, enroll synthetic test organizations, configure the GitHub staging environment and deploy API/worker/frontend together. No deployment provisions provider accounts or a database automatically.

Before admitting client data, verify with **two** hosted organizations: sign-in, same-named client/month creation, denied cross-firm URLs, isolated Work, upload/extraction, preparation, review, client reply, carry-forward and export. Restart the worker mid-job and verify one durable outcome. Then rehearse database restore and verify access isolation again. Local mocks and a successful deployment are not substitutes for these checks.

Current controls include application-enforced organization ownership, authenticated roles and membership, same-close source validation, durable jobs, malware-scanning gates, firm resource caps, deletion preview/holds and an operational snapshot. Optional webhook alert delivery and PostgreSQL row-level security for the runtime role are implemented; contractual backup expiry and hosted recovery verification remain outstanding. Do not treat this guide or code as enterprise certification. See [operations and recovery](operations.md).

## Troubleshooting

- **401:** unlock the local tab, or sign in again to the correct WorkOS application. Development tokens never override hosted authentication.
- **403 organization not enabled:** verify the signed session's organization and its registered enabled row; never assign foreign data to solve login errors.
- **404 close/link:** unavailable, foreign or revoked records deliberately do not reveal which condition applies. Return to your own close list or request a fresh client link.
- **503 readiness/storage:** check PostgreSQL connectivity and applied migrations before retrying.
- **Work stays queued:** check worker health/logs, database URL and organization status. Do not create duplicate jobs to bypass a stalled worker.
- **Evidence changed:** refresh, inspect the replacement and produce/review a new revision.
- **Unknown posting outcome:** reconcile with the provider before retrying. Never equate a timeout with failure to create.

For detailed feature behavior follow the README links. For what remains and its acceptance evidence see [delivery backlog](implementation-backlog.md).

## Take over an existing client

A client's first close in the firm is its **opening month** (migration `118_opening_baselines.sql`). Open the month-end you take over at first: for a September start, open August, capture its Trial Balance and bring in the prior accountant's schedules there; September then carries them forward as usual. A month opened for an earlier period before the opening baseline is reviewed becomes the opening month instead. A name that is a confirmed alias of more than one client is refused until it is opened under the client's own name. Closes that existed before this change are not opening months.

**Prior schedules.** On the opening month, upload the prior accountant's schedule of prepaid expenses or of deferred revenue (CSV or text, or PDF; one kind per document, at most 20 rows) as evidence. The agent reads it row by row: each row's label, amount, service dates, accounts and the balance the schedule states at this month-end are quoted from inside that row, so a figure from another row cannot be attached to it. On Work, decide the whole schedule at once (**Import schedule**): import each row with a method (equal full months or actual days) or set it aside with a reason, and confirm the schedule is the client's and that the listed rows are all of its rows. An imported row becomes a schedule-only draft (no journal, like a carried schedule), named "Prior schedule #<first version> · <label>", and is refused if the calculation does not give the balance the schedule states. The opening baseline then ties the schedules to the Trial Balance, and the next month carries them forward. A row is corrected by uploading the corrected schedule as a replacement: it must list every row imported before, and each such row revises its earlier draft. The ordinary preparation form does not revise an imported row. Other months, depreciation, loans and spreadsheet (XLSX) uploads are not read this way.

On the opening month's **Close & sign-off** tab, **Opening baseline** shows the tie-out at that month-end (`GET`/`POST /v1/closes/{id}/opening-baseline`). Each account that does not tie (it differs, has no supporting schedule, or ties only after proposed entries), each draft the tie-out could not match and each unclassified account needs a note on how it will be cleaned up; accounts that tie or differ within the firm's materiality need none. The review is bound to what the reviewer saw: a recapture with the same balances and support keeps it, a changed balance or support asks for it again. The tie-out cannot be reviewed without a current, readable Trial Balance or while a posting is uncertain.

Until it is reviewed, the opening month and every later month of the client wait for it. Reviewed, the opening month no longer waits for the differences and coverage of the accounts it noted, so it can be signed off; those notes are **inherited cleanup**. Every later month of the client waits until each item is resolved with a reference (`POST /v1/closes/{id}/opening-baseline/resolve`, on the opening month); an item noted in an earlier review stays open until it is resolved, even after a new review. A month earlier than the opening month does not wait for it, and a month opened under one of the client's current confirmed aliases is the same client, unless that name was already another client's own before the alias was confirmed, or aliases of more than one client claim it. While later months wait for the baseline or its cleanup, the opening month cannot be deleted; its review and resolutions are kept as they were.

## Define readiness and sign off a supported scope

On a client close, open **Close readiness → Define or update scope**. **Replace selection with latest drafts** proposes the current end of each draft revision chain; inspect and change the selection before saving. The suggestion covers only drafts already in this close, not the client's complete ledger or missing documents. Describe the included work and exclusions, and list external steps such as an independently performed ledger tie-out or prior-month follow-up. A scope containing only external steps is permitted. Mark an external step done only with its completion reference. Save the scope revision.

Readiness explains unresolved reviews/calculation blockers, active or failed work, outstanding client jobs and uncertain ledger attempts, including Xero. Resolve or explicitly cancel work as appropriate; cancellation does not establish source completeness. Refresh readiness before deciding. A hosted signer needs both `workpapers:review` and `close:signoff`. Review the defined scope, record your reason, affirm your authority and choose **Sign off supported scope**.

The decision captures organization, client/month, required scope, evidence identities, reviews, job states and saved posting receipts. A changed snapshot shows **Changed since sign-off — reopen required**. Choose **Reopen with reason**, resolve the change, review again and make a new decision. Earlier decisions and their exact snapshots remain immutable in the database and exported manifest. Editing scope cannot silently overwrite a signed decision.

Use the complete current schema; see [migration instructions](../infra/postgres/migrations/README.md). No real firm authority is inferred from the shared local token: local decisions explicitly identify development.

For approved procedures, client-side accountant access and permission setup, follow [firm procedures and assignments](firm-procedures.md). Apply only unapplied migrations in numeric order when upgrading an existing database.

## Approved email delivery

Use [the Resend setup and recovery guide](email-delivery.md) for per-firm verified senders, role permissions, encryption, request approval and delivery-state boundaries. Manual links remain available without provider configuration.

For the close-level evidence scope, live question list and durable job coordination, follow [Prepare this close](close-coordinator.md).

Client-specific sandbox OAuth, period reports, linked evidence and coverage planning: [QuickBooks onboarding](quickbooks-onboarding.md).

For automatic coverage status, multi-job requests, mandatory scanning and attachment release, follow [Coverage and client evidence](coverage-requests.md).

For partial-month prepaids, prospective credits, prior-balance adjustments, month-boundary cancellations and fixed incurred accruals, follow [accounting methods and accountant acceptance](accounting-methods.md).

Separate production OAuth, client grants, review, one-attempt posting and readback: [production posting](production-posting.md). Leave production credentials unset during ordinary local development.

## Multi-client model and first pilot

One WorkOS organization is one accounting firm, not one accounting client. Within that firm, each client has monthly closes, its own ledger connection and approved procedures. Current client identity is the organization plus a case-insensitive client label; choose stable names that match the confirmed ledger company. A rename/merge flow is not implemented. A connection is reused across periods of that client, never across firms.

For the first pilot create Firm A and Firm B, each with a same-named synthetic client, and a second client in Firm A. Invite a preparer, a separate reviewer and a client-side accountant assigned only to the first client. A request-only recipient can answer through a scoped secure link without a product account; that link cannot approve accounting treatment or posting.

Onboarding is currently operator-assisted: WorkOS invitations/roles, in-app enrollment/memberships, client assignments, connector consent and accounting-procedure approval are separate steps. The desired first-use flow is sign in → join firm → create client/month → connect ledger or upload evidence → receive one reviewable proposal. A unified invitation/setup wizard and narrower Google file picker remain product work, not current features.

## Staging acceptance matrix

Record the deployed commit, environment, fixture, expected result, observed result and any blocked step. Keep client documents, tokens and private request URLs out of public tickets/logs. Use synthetic documents and approved test recipients; a code test or green deploy is not a live-provider result.

| Flow | Steps and required result |
| --- | --- |
| Enrollment and isolation | Sign into both firms. Create same-named client/month closes; copied foreign close, evidence and export URLs must fail, and Work/search must not leak the other firm's counts or names. |
| Client access and revocation | Assign an accountant only to Client A; Client B remains inaccessible. Revoke app membership/assignment and WorkOS access, then verify subsequent requests fail. |
| Evidence to prepared draft | Upload a labeled invoice and a PDF. Verify extraction, cited facts, specific unanswered questions, confirmed method and exact calculator output; missing evidence stays blocked. |
| Review and procedures | Separate preparer/reviewer approve the supported example. Enable independent review and prove self-approval is refused. Draft a procedure, approve its example, preview affected clients and activate/apply it explicitly. |
| Changed evidence | Replace a source. Only dependent work becomes stale; prepare/review a new immutable version while earlier history remains inspectable. |
| Client follow-up | Approve a consolidated request to a test recipient, verify Resend delivery, approve one reminder, reply through the link, scan/release an attachment and observe the original affected jobs resume. A reply or bounce prevents further reminders. Set a reminder rule (for example one day, one reminder) and confirm approval waits until due and stops at the cap. |
| Inbound email | Use the configured reply-to address. Replies remain held until verified/released; unauthorized senders cannot resume work. Keep reply attachments on the secure link. |
| Scan boundary | Exercise clean PDF and TXT/CSV plus the provider's harmless malware-test fixture. Failed, stale, mismatched or missing scan receipts never permit release. Confirm direct-origin/spoofed-header refusal. |
| Per-client ledger | Connect/confirm each sandbox company, capture reports/transactions and trace a proposed fact back to the source. Check scheduled refresh and visible failures. Use supported per-client native sandbox corrections for write/readback; the legacy global prepaid journal is a separate single-firm demo. |
| Posting controls | Approve an exact preview, change a precondition and confirm refusal; execute one supported fresh sandbox action and verify canonical readback. Repeated clicks must not produce a second effect. Investigate an uncertain attempt rather than retrying. |
| Xero and Stripe | Connect a Xero demo company, register the webhook and confirm an invoice change brings an early capture; check Xero's reconciled flags beside a reconciliation. On the demo company, recode one bill line end to end before enabling `bill_correction`. Capture Stripe's Payouts and balance activity reports for a month with manual payouts and check the roll-forward against Stripe's Balance summary. |
| Recurring close | Carry forward reviewed schedules, refresh new-period sources/booked balances and review fresh work. Prior-period approval does not prove this month's balance. |
| Close and binder | Define supported scope, resolve blockers and sign off. Confirm journal decisions are refused until reopen; export the binder and verify its checksums, originals and posting receipts. |
| Recovery | Restart the worker during preparation and observe one durable result. Restore an isolated database with workers disabled; check both firms and evidence hashes, then reconcile external attempts before enabling delivery. |
| Operations and UX | Check aged jobs, resource/model limits and a test alert. Walk through empty, unavailable and incomplete-source states; none may falsely claim completion or no exceptions. Record human time and accountant corrections. |

Production entry needs this matrix on the released revision, accountant acceptance of the intended methods, and [production deployment/recovery setup](deployment.md#production-and-remaining-deployment-work). Hosted MCP also needs the WorkOS Connect setup in [MCP](mcp.md#hosted-oauth-setup), and each posting kind beyond prepaid its sandbox check before it is listed.

## Try a historical period

A close with no live QuickBooks or Xero connection can work from uploaded ledger exports (PRD section 6). Upload them on the close's Evidence tab, then open **Historical exports** beside the QuickBooks and Xero panels and label them: which uploaded documents they are (1–20 current ones), the date they were exported, whether they cover the whole month and whether they already include the final close adjustments, with a note (migration 096, `POST /v1/closes/{id}/historical-exports`). The label is what the person reports, never a verified ledger: the close brief's ledger line says they are uploaded historical exports, who reported the label and that it is not verified, asks for the exports to be verified, and warns when a labeled document has since been replaced; the close package carries the label (`historical_exports`) and the same sentence in `ledger_completeness`. The latest label is part of the readiness digest, so changing it after sign-off needs a new sign-off. Exports that already include the final adjustments mean a proposed entry may repeat one already in the books.

