# Scaffold validation — September 21, 2026

## Passed locally

- `npm run check`: frontend and both Worker TypeScript projects.
- `npm test`: queue handoff accepts only HTTP 204; 200, 503 and a lost response reject acknowledgement.
- `npm run build`: React production bundle.
- `go test ./...` and `go vet ./...` in `services/backend`: reference response, unavailable action route and unfinished worker boundary.
- `gofmt` applied to Go source.
- Wrangler dry run for both configurations with `--containers-rollout=none`: asset bundling, Worker bundling and binding configuration accepted. Nothing published.
- Browser: Work → Client close → Review; synthetic source/ledger/policy references, proposed debit/credit and disabled approval rendered. API outage showed a recovery message; Try again restored the workpaper after the API restarted.
- Original supplied PRD copy matches byte-for-byte.

Local Node was 26.5.0; project/CI target Node 24. Go 1.27.1 was temporarily downloaded from go.dev and its SHA-256 matched the official release metadata. Dependency install reported zero vulnerabilities at that time; this is not a security assessment.

## Not verified / not performed

- Docker and Compose are not installed on this host. Full Wrangler container dry run failed because Docker is missing. Container build, Compose boot/watch, local Cloudflare container execution and Postgres boot remain unverified here; CI includes image build and Compose config checks.
- GitHub Actions has not run: no remote or commits exist. No push, PR, deployment, database migration or infrastructure provisioning occurred.
- No live identity/tenant isolation, persistence, calculation service, agent/model run, MCP negotiation, evidence intake, connector, approval, client sending, posting or readback exists yet.
- No accountant validation, full accessibility audit, responsive device audit or customer usability study.

## Minimalism review

Ponytail review applied to the finished scaffold. Removed a redundant JSX fragment around the Review component (2 lines); retained separate runtime entry points and configs because the requested API/worker deployment boundary needs them. No custom orchestration framework, provider SDK, database abstraction or unused accounting engine was added. Framework-native routing/layout and Go stdlib hold this scope.

Cross-model roster review is not claimed: no PickupIssue or CommitPR was requested. It remains required at the CommitPR gate under the supplied project instructions.

## Preview

The native API and Vite development server were started on loopback for browser inspection. They contain only the synthetic fixture. The API binary is temporary under `/private/tmp/accounting-api`; durable local setup is the documented Docker Compose command. The preview processes may stop when the desktop session ends.

## Public SPA follow-up — September 21, 2026

- Public marketing, contact and login landing pages are separate from the lazy-loaded dashboard; all internal navigation uses React Router URLs.
- Frontend/edge TypeScript checks, production build, transport tests/vet and the edge Worker-only dry run passed.
- New exact-origin CORS check passed. Removing the allow-origin header made the check fail; restoring it returned green. Existing untrusted-origin and absent-origin cases remain without an allow header.
- Browser navigation verified homepage → contact → login → dashboard; dashboard data loaded using `VITE_API_BASE_URL=http://127.0.0.1:8080` from frontend origin `http://127.0.0.1:5173`. No proxy was needed on that path. Refreshing `/dashboard/review` and browser Back both restored the correct view.
- Contact details remain unset at the user's request. Login is a landing page only; no identity provider, session or route authorization is claimed.
- React Router is the sole added dependency: standard SPA routes/history replace a custom navigation implementation. Ponytail review retained the requested public/product split and avoided a fake login flow or contact submission backend.

## Prepaid preparation feature — September 21, 2026

- Focused Go tests and vet passed for `internal/prepaids` and `internal/transport`; frontend type-check and production build passed.
- Golden PRD calculation: $12,000 over 12 months gives $1,000 expense and $11,000 prepaid in September. Uneven $100/3 allocation gives $33.33, $33.33, $33.34 and no final-period adjustment.
- Negative verification: removing final-month residual handling made the test fail with $0.01 left over and an unwanted proposal; restoration returned green.
- Input checks cover partial periods, missing confirmations, fractional cents, oversized amounts, cash basis and an out-of-period close. HTTP checks cover the prepared response, allowed/denied cross-origin POST preflight and trailing JSON rejection.
- Browser verified the first preparation from entered/example inputs via the separate API URL. Editing the amount removed the previous result; fractional cents produced a field error and focused the amount input; correcting it prepared a new draft.
- Browser-triggered JSON and CSV downloads were read back from Downloads: JSON includes the user-supplied source reference and $11,000 proposal; CSV includes all 12 rows, totals $12,000 and ends at zero. The browser automation's download-event waiter timed out even though the file was saved; filesystem readback confirmed the export independently.
- Synthetic exports retained at `~/Downloads/prepaid-workpaper.json` and `~/Downloads/prepaid-schedule.csv` as sample output for inspection.
- No new dependency, accounting engine, model framework or job scheduler. The shared dashboard layout replaces repeated framing; calculation uses Go stdlib and integer cents. Ponytail review found no additional abstraction to remove.
- The native preview uses `/private/tmp/accounting-api-prepaids`. No deployment, CI run, persistence, identity verification, document verification, ledger read/write or professional accounting validation is claimed. Prior Docker/Cloudflare runtime limitations remain.

## Saved closes and statement reconciliation — 2026-09-21

Author: Codex (session model). Implemented in the existing initial-foundation checkout: Git still has no commit or remote from which to create an updated-origin worktree. No commit, external reviewer gate, PR, deployment, or hosted resource mutation was performed.

- Focused Go checks pass: `go test ./internal/transport ./internal/reconciliation ./internal/prepaids` with `TEST_DATABASE_URL` pointing only to a disposable local PostgreSQL 18 database; `go vet ./internal/transport ./internal/reconciliation` passes.
- The new round-trip test inserts a synthetic client close, saves the same prepaid twice, verifies stable identity, rejects the wrong client and reads back the expected $11,000 balance. Its initial readback assertion incorrectly depended on JSON whitespace; it was corrected to decode the typed result and passes.
- Reconciliation test proves two exact candidates, an unmatched $50 ledger item, source roll-forwards, retained missing-page/inconsistent-total/ambiguous-match blockers, and fractional-cent rejection. Token checks prove absent/wrong tokens do not access the database.
- Existing transport tests caught the initial broad OPTIONS route changing unknown-action status from 404 to 405. Preflight is now scoped to implemented endpoints; existing boundary checks pass unchanged.
- Browser verification: opened private closes, unlocked with a disposable synthetic test token, created a client/month, prepared reconciliation, saved the draft, returned to the close, and expanded the persisted workpaper. It retained both match candidates, original source references, unmatched L3 and the unresolved $50 closing difference.
- API was restarted with the private generated `.env` token. Authenticated readback still returns the saved draft; the temporary browser token is now rejected with 401. Browser was refreshed to clear the in-memory test credential.
- TypeScript checks and final production frontend build pass. Go API binary built successfully. Docker is unavailable locally, so Compose runtime/container build and GitHub Actions remain unexecuted locally. CI configuration now includes a migrated disposable PostgreSQL service; no remote CI run is claimed.
- Ponytail review applied: standard-library CSV/JSON/integer math, native database constraints, one PostgreSQL driver, reused form/layout/API patterns. No extra provider abstraction, repository layer, model call, queue path or speculative schema was added. No further deletion identified in this bounded slice.

Local preview retained: API on 127.0.0.1:8080, Vite on 127.0.0.1:5173, disposable synthetic PostgreSQL on 127.0.0.1:15432 with data in `/private/tmp/accounting-feature2-postgres`. This temporary preview database is not a long-term data store; documented Compose uses a persistent named volume. `.env` is ignored, mode 0600, and contains the generated private workspace token and native preview connection. One clearly labeled synthetic browser example close remains so the user can inspect it. Hosted identity (likely WorkOS AuthKit), document verification, accounting-policy review, multi-firm authorization and cloud deployment are not complete.

## AuthKit, sandbox posting and staging integration — 2026-09-21

Codex authored the integration in the same initial, uncommitted foundation checkout. AuthKit React and golang-jwt/v5 are the only new direct dependencies. Ponytail review retained SDK session handling, stdlib transport and native database state guards; the staging command stopped deploying the unused queue container. No cross-model implementation gate, commit, push, provider write, or deployment was run.

- Focused auth/transport tests pass against the disposable migrated PostgreSQL database. Signed test JWTs cover valid sessions and rejection of other organizations, wrong issuers, expired tokens and development-token bypass.
- The simulated QuickBooks transport verifies the attempt is durably `executing` before receiving any POST; unapproved execution and changed digest are rejected; successful independent readback reaches verified; a lost response remains unknown; both paths allow exactly one outbound write despite repeat execute calls.
- Later changed-entry readback invalidates verified status. Its regression check was observed failing when the invalidation update was removed, and passing after restoration.
- Go vet and API compilation pass. Frontend TypeScript and builds pass both without AuthKit configuration and with dummy public configuration to retain the SDK branch. The final local build was restored without dummy settings.
- Cloudflare Worker bundle dry-run passes with `--containers-rollout=none`. This does not build or run a Container. Docker is still unavailable locally. Ruby's standard YAML parser accepts Compose and both workflow files; GitHub Actions has not run.
- Browser shows the new login page accurately stating that AuthKit is not configured. No real OAuth redirect/session was tested. Local `/readyz` passes with both migrations; unauthenticated saved-close access returns 401.
- GitHub CLI is signed in, but the repository has no remote/commit. Cloudflare CLI reports unauthenticated. Local configuration contains only the existing database, private development token and frontend origin. WorkOS organization/app settings, a QuickBooks sandbox token/company/account mapping, and the staging destination are missing.

Live WorkOS authentication, live QuickBooks sandbox create/readback, hosted database readiness and staging deployment remain unverified and inactive. Setup details and required identifiers/secrets are in `auth-sandbox-staging.md`. Sandbox OAuth token refresh/onboarding and production posting are not implemented. No real financial mutation has occurred.

## Close review package — 2026-09-21

- Focused transport tests passed: `go test ./internal/transport -run 'TestPackage|TestSavedCloseRoundTrip' -count=1`, with `TEST_DATABASE_URL` targeting the disposable local database. The archive check covers two workpaper kinds, full schedule rows, unresolved blockers, unknown sandbox receipts, escaped HTML and formula-like CSV identifiers. The database round-trip exports 101 versions and rejects unauthenticated access.
- Transport vet, frontend TypeScript checks, production frontend build and API compilation passed.
- Live authenticated local API download was independently opened with Python's ZIP reader. Close 3 retains its saved reconciliation, both match candidates, unmatched L3 at -$50.00, $50 closing difference, both review blockers and a snapshot timestamp. An initial probe incorrectly expected the numeric difference in the HTML summary; it resides in the full JSON calculation and CSV. Corrected readback verifies those actual formats.
- Synthetic sample retained at `/private/tmp/accounting-close-review-example.zip` for inspection. The local API runs the rebuilt `/private/tmp/accounting-api-export`; no remote deployment or provider call occurred.
- Ponytail review: existing authentication and request helper reused; Go standard ZIP, CSV and HTML packages; browser-native download and Print / Save as PDF. No dependency, migration, job, PDF engine or new configuration was added. No further deletion identified. No cross-model implementation gate or commit was requested.

## Evidence and durable preparation — 2026-09-21

Codex authored this continuation in the same uncommitted initial checkout; there is still no remote/base commit for a fresh origin worktree. No commit, PR, hosted deployment, ledger write or cross-model implementation review occurred.

- Migration 003 was applied only to disposable local PostgreSQL. Focused transport tests (`TestEvidence`, `TestPackage`, `TestSavedClose`, `TestReferenceAndAction`) pass. They prove authenticated bounded uploads, rejection of PDF/binary/oversized input, exact original readback, close isolation, content deduplication, uploaded CSV substitution, cancellation, idempotent jobs and drafts, lock isolation and recovery after an interrupted claim transaction. ZIP readback includes the exact original source bytes.
- Frontend/Worker type checks, frontend production build, Go vet for preparation/transport/worker and API/worker compilation pass. Wrangler's preparation Worker dry-run passes without containers; an initial log-path permission error was resolved by using a temporary writable log path. Compose and deployment YAML parse successfully. Docker runtime and remote CI remain untested.
- Browser verification used isolated loopback API/frontend processes with a disposable token: created a synthetic close, uploaded invoice text, inspected script-like text safely as text, entered/confirmed prepaid terms, saved source-linked instructions, and queued a job while the worker was stopped. Starting the separate worker completed job 4 and saved draft 226 with source ID 3, the expected $1,000 monthly expense and $11,000 balance. The UI showed the persisted invoice link and calculation together.
- Final readback through the normal private-token API confirmed the original SHA-256, completed job, saved prepaid balance and original bytes in the ZIP. The disposable browser token is rejected. Temporary browser/API/frontend test sessions and upload/log scratch files were removed. Synthetic close 20 remains for inspection; the normal API and worker run on ports 8080 and 8081.
- Ponytail review removed duplicate strict JSON parsing and limited polling to job status while jobs are pending. Existing calculators/authentication, Go stdlib multipart/text/JSON and native Postgres locking hold this scope. No dependency, model framework, queue producer or orchestration framework was added. The existing Cloudflare worker gets a cron wakeup and database-only configuration; actual hosted execution is not claimed.
- TypeSafe skill was read; its live index was inaccessible through the web tool and no TypeSafe key is configured, so no Jev call was made. No model-driven extraction or accounting interpretation is claimed.

Limits and remaining work: text/CSV only, human-entered prepaid facts, no automatic invalidation when new evidence arrives, no PDF/OCR, no multi-firm/per-staff entity authorization, no retention/deletion workflow or production security certification. Jobs cover short deterministic preparation only; future slow tools and evidence waits require checkpoints. See `evidence-and-preparation.md`.

## Workpaper review and evidence replacement — 2026-09-21

Codex authored this continuation in the same initial uncommitted checkout; no origin exists for worktree isolation. Migration 004 was applied to disposable local PostgreSQL only. No provider writes, hosted deployment, commit/PR or cross-model implementation gate occurred.

- Focused tests pass for review decisions, concurrent/stale review rejection, hosted review permission enforcement, unauthenticated history rejection, close scoping, source replacement and preservation of unrelated approvals/history. An existing queued job fails after its source is replaced. Replacement is blocked while affected posting is executing and clears an unexecuted posting approval.
- Existing evidence, close export, saved-close and sandbox posting checks passed across the changed boundaries. The posting regression was observed failing with the requested-changes guard removed, then passing after restoration. Simulated provider writes only; no real QuickBooks request was made.
- Frontend/Worker type checks, production frontend build, transport/preparation vet, API/worker builds, Go formatting and deployment YAML parsing passed. Docker runtime, hosted AuthKit roles and remote CI remain unverified.
- Browser: recorded a development approval on synthetic draft 226; uploaded corrected invoice text; compared $12,000 and $6,000 sources side by side; explicitly replaced source 3 with 11; saw the draft become `source_changed`. Its earlier approval remains visible in decision history and the stale approval action is disabled. One temporary tab became unavailable during verification; a fresh tab completed the flow.
- Independent authenticated API/ZIP readback confirmed current stale state, the earlier approval bound to its workpaper digest, both original files, the replacement relationship and stale-state HTML summary. Synthetic close 20 retains this review example. Temporary browser-test servers/tab and upload scratch file were removed. Normal API and updated worker remain on loopback ports 8080 and 8081.
- Ponytail review: native foreign-key/order constraints prevent cross-close replacements and cycles; one SQL view computes current review state from existing source links and the latest decision; close locks serialize review/replacement/execution claims. No additional dependency, graph engine, custom approval framework or model call was introduced. No further deletion identified.

This adds explicit replacement-driven invalidation, superseding the earlier limitation above. Merely uploading new evidence still does not invalidate old work. Source comparison is text side by side; automatic extraction, calculation diffs, automatic revised preparation, accepted-exception approval, multi-firm access and firm sign-off remain outside this slice.

## September 22, 2026 — recalculation and PDF/OCR

- Mixed two-page PDF check passed with actual Poppler and Tesseract: embedded text on page 1 and image-only OCR on page 2 both retained. Malformed PDF rejected by extraction.
- Disposable PostgreSQL migration 005 applied. Focused evidence/review/sandbox/export/saved-close integration checks passed. Revision save automatically queues, completed retries reuse results, predecessor becomes out of date, fresh draft requires review; failed PDF retains original bytes and supports retry.
- Recalculation test observed failing with automatic enqueue removed, then passing after restoration. No remote provider writes.
- TypeScript/build, focused Go vet and API/worker compilation passed. Container build and Linux runtime limits await Docker/CI; Docker is not installed locally.
- Ponytail review: reused forms/queue/storage, native commands; removed unnecessary random-token hex formatting. No parser framework, model gateway or speculative storage abstraction added.
- GitHub authentication verified outside the sandbox; Cloudflare unauthenticated. No remote/initial commit, hosted configuration or deployment. See recalculation-and-pdf.md for activation requirements and extraction limits.
- Browser verification completed: synthetic invoice revision $12,000 → $6,000 created job 27 / draft 554, showing $500 monthly expense and $5,500 remaining prepaid, pending review; predecessor 226 retained. PDF upload source 26 completed, displayed both numbered pages, and exact original/extracted ZIP entries were independently read back.

## September 22, 2026 — change review and request drafts

- Migration 006 applied to the disposable local database. Added same-close exact predecessor comparisons and consolidated unsent request drafts with immutable revisions, context binding, idempotency and concurrent-edit checks.
- Focused `TestRequestDraftRevisions`, `TestPaperChangesPreserveExactAmountsAndAllSections` and existing package checks passed. Stale-context check was observed failing when evidence inventory binding was removed, then passed after restoration.
- Browser testing exposed numeric request IDs ordered as text. A numeric digit-boundary regression failed before qualifying both SQL ordering expressions, then passed. Edited wording now saves and reopens as the latest revision.
- Browser/readback verified synthetic draft 554 against 226: $1,000 → $500 September expense, with schedule changes shown by month and source revisions by role. Request draft 24 retained the edited question, and exported `request_drafts_latest_20` contained that exact text and history.
- TypeScript checks, production frontend build, focused Go vet/API build and deployment YAML parsing passed. Local API readiness requires the new table. No messaging, model calls, ledger writes, commit/PR or cloud deployment occurred.
- Ponytail review: reused authenticated routes, stored lineage and SQL transactions; native form submission avoids duplicating editor state. No new dependency, messaging framework, workflow abstraction or speculative client portal. Synthetic close 20 and its request history remain as an inspectable local example.

## September 22, 2026 — live Work inbox and evidence coordinator

- Migration 007 applied only to the disposable local database. Upload atomically creates/reuses an intake on the existing preparation queue. Replies and confirmations resume that same job; no second executor or queue was added.
- `TestWorkInboxCoordinator` passes: duplicate upload, quoted facts, no document-authorized calculation, unauthenticated and cross-close rejection, existing request draft approval, delivery-before-reply, original reply preservation, conflicting candidates, stale revision rejection, exact prepaid output, paired-CSV reconciliation on the original job, and replacement fencing. The original-reply preservation check was observed failing with trimming restored, then passing with the fix.
- Direct consumer checks `TestEvidencePreparationRoundTrip`, `TestRequestDraftRevisions`, and `TestPackagePreservesBothWorkpapersAndEscapesSources` passed. Final frontend typecheck/production build, focused Go vet, API/worker builds, gofmt and deployment YAML parsing passed. Remote CI and container builds were not run.
- Browser verified synthetic close 59, evidence 54, request 4 and reply evidence 55: upload → sourced proposals → approved/unsent question → explicitly synthetic delivery record → conflicting $1,200/$600 candidates → reviewed $600 confirmation → job 60 completed with draft 577. The draft shows $50 September expense and $550 remaining prepaid, still pending review. No client message or provider request was sent.
- Independent authenticated ZIP readback verified the same job/result IDs, original reply text, both evidence sources and approved request history in `work_history`. Normal local API readiness, worker health and frontend return 200; authenticated portfolio read includes both intake and pre-existing preparation jobs.
- Ponytail review removed full fact reparsing from source-freshness checks; those now compare existing source metadata. Native forms, SQL revisions/locks, existing calculators and request drafts cover the flow without another dependency, provider adapter or workflow framework. The existing unborn checkout still has no remote or initial commit; no CommitPR gate, push or deployment occurred.
- Temporary browser tab, test-token API/frontend and upload scratch file were removed. Synthetic close 59 remains as a usable demonstration. Updated normal API/worker continue on loopback ports 8080/8081, with the existing frontend on 5173. AI interpretation, external delivery/client links, and cloud activation remain unconfigured.

## September 22, 2026 — agent interpretation and portfolio decisions

- Migration 008 applied locally. OpenAI Responses integration uses strict proposal output, validated same-close source IDs and literal quotes, bounded context/output/time, expiring claims and inventory binding. Staff answers and existing client replies resume the same job; human confirmation remains mandatory before deterministic preparation.
- Mocked provider contract and real-database integration checks passed for prose proposals, missing-account questions, immutable staff answers, cross-client exclusion, evidence arriving during interpretation, and both prepaid and paired-CSV reconciliation. Disabling the inventory fence caused the expected failure: “Stale model output published needs_confirmation”; restoring it passed.
- Existing coordinator, evidence preparation, request revision and package tests passed. Frontend TypeScript/build and API/worker builds passed. These checks establish application behavior with mocked responses, not live model accuracy or account access. No OpenAI credentials were configured or live provider requests made.
- Browser verified server-filtered client work, actual source-change blockers, quoted proposal facts, specific questions, staff-answer controls and disabled confirmation while questions remain. The explicitly mocked browser fixture (close 82/job 120) and temporary tab/token server were removed. Normal API and worker were restarted; API readiness returned ready.
- Ponytail review: existing queue, calculators, evidence, request approval and review controls reused; standard-library HTTP/JSON only. Removed the manual work-type shortcut from model-selected proposals and surfaced workpaper blockers in collapsed rows. No new dependency, agent framework, provider abstraction or independent status cache. No roster gate was invoked because this was implementation only, without PickupIssue/CommitPR.
- Local API/worker binaries remain in /private/tmp for the running development services. Live OpenAI credentials/model, hosted activation, container/remote CI and deployment remain unverified; no commit, push or deployment occurred.

## September 22, 2026 — scoped client request links

- Continued the same uncommitted product foundation (unborn main, no origin/main); no independent worktree, commit, PR or hosted deployment. Scope: expiring/revocable text reply links for approved requests; manual distribution only. Attachments, verified-recipient login, reminders and email delivery are outside this slice.
- Applied migration 009 only to the disposable local database. Reused authenticated reviewer actions and one shared staff/client evidence writer; no new dependency. Link possession is explicitly unverified identity, not recipient authentication or accounting authority.
- TestScopedClientReplyResumesJob passed: issuance authentication, request scope, rotation, revocation, expiry, no workspace access or secret leakage, stale context, entity confirmation, original text retention, identical replay, conflicting replay, receipt, same-job resumption and withdrawal. Disabling the request-context guard produced the expected 200-versus-409 failure; restoring it passed.
- Direct consumer checks TestWorkInboxCoordinator, TestAgentAnswersResumeAndInventoryFence and package tests passed. Frontend TypeScript/production build, Go API/worker builds, focused transport vet and deployment YAML parsing passed. The final attribution change was verified with the focused link test.
- Browser verified synthetic close 96/job 149/request 19: reviewer approval → private link creation → scoped client page without workspace login → URL fragment removed → submitted service-end answer → receipt → same job returned to needs_confirmation with evidence 167 cited in the date field. No email, external model call or ledger write occurred.
- Authenticated export readback included issuance/consumption and reply provenance without link secrets or hashes. The synthetic close, its evidence/request/job and both browser tabs were removed. Normal private-token API and local worker were restored. Running development binaries remain in /private/tmp; intermediate API binary removed.
- Ponytail review: reused evidence, request-context freshness, native forms and durable jobs; standard-library token generation and hashing. No provider adapter, portal framework, separate queue, recoverable plaintext token or duplicate reply writer. The TypeSafe skill was read as instructed; its live documentation was inaccessible, and this deterministic capability/authentication flow needed no model judgment or new integration.
- Remote CI, Docker build, HTTPS deployment and real client distribution remain unverified. This is local functional validation, not a production security certification.

## September 22, 2026 — next-month prepaid carry-forward

- Scope: preview current reviewed prepaid schedules, create/reuse the immediately following client close, copy source evidence with lineage and queue fresh schedule-only drafts. Posting, reconciliations, policy changes and sign-off remain unchanged. Continued the same unborn repository foundation; no origin/main exists for a separate worktree.
- Migration 010 applied locally. Existing same-close source/review checks remain in a base view; a recursive lineage view invalidates dependent carried drafts when any predecessor source/review changes. The normal worker rechecks captured predecessor digest and review before calculation.
- TestCarryForwardRoundingLineageAndFreshReview passed: December/January rollover; two linked sources; exact 100.01 allocation with 33.35 final residual; reviewed-only eligibility; retry reuse; cancelled-job resumption; fresh review/no journal; end-of-term exclusion; two-generation invalidation; queued predecessor becoming stale. Removing worker predecessor validation caused the expected failure (“Worker accepted a stale predecessor completed”); restoration passed.
- Direct-consumer tests passed for source-linked preparation, workpaper review/replacement, mocked agent preparation, existing sandbox execution and close exports. Frontend TypeScript/build, API/worker compilation, focused preparation/transport vet and staging YAML parsing passed.
- Browser verified synthetic September close 121/draft 634 → October close 122/job 173/draft 635: opening scheduled prepaid 1100.00, expense 100.00, remaining 1000.00; predecessor link and copied source visible, review pending and no posting action. Export readback verified lineage, copied evidence, schedule-only warning and null journal proposal.
- Synthetic closes 121/122 are retained as an inspectable carry-forward example. Browser tab and temporary-token API were closed; normal private-token API/worker restored. Running binaries in /private/tmp support local development.
- Ponytail review: reused calculations, evidence deduplication, reviewer permissions and PostgreSQL queue; native selection form and SQL lineage avoid another scheduler, policy framework or service. Removed unnecessary formatted-error machinery. No dependency or provider call added.
- This is schedule carry-forward, not verified ledger balances or recurring journal posting. No live provider, remote CI, Docker, deployment, commit or PR validation occurred.

## 2026-09-22 · Organization isolation and scoped sign-off

- Added migrations 011–012 to the local development database; all prior records retain development ownership. No hosted data was reassigned.
- `TestOrganizationIsolation` passes with mocked signed WorkOS sessions for two registered firms: identical client labels stay separate, portfolio/Work/close children/export/carry routes reject foreign access, a foreign next-month close is not selected, unregistered/development claims are rejected in hosted mode, sandbox use remains restricted and disabled organizations are not claimed by the preparation worker.
- Observed this test fail both without organization admission and without close ownership enforcement, then restored each guard and observed green.
- Direct consumers passed: saved-close round trip, prepaid carry-forward, scoped client reply/resume, Work coordinator, agent answer/inventory boundary, evidence preparation and durable sandbox execution (provider mocked).
- `TestScopedSignoffInvalidationAndReopening` passes: two required papers, pending external step, queued work and stale snapshots block signing; new evidence invalidates a sign-off; explicit reopening and additional signer permission are required; decision history and signed scope appear in export. Observed the test fail when snapshot invalidation was removed, restored it and observed green.
- Browser check on a separate synthetic close #162: define scope/exclusions, select approved draft, record an external completion reference, sign the supported scope and reopen it; UI showed both decisions. Kept this clearly labeled synthetic example for review. No real firm or ledger action occurred.
- Frontend TypeScript/build, edge TypeScript, affected Go vet, API/worker builds and deployment YAML parse passed. Docker is unavailable on this host; Compose image startup and Cloudflare runtime were not executed. Providers and deployment remain unactivated.
- Ponytail review: reused existing auth/context, close routing, SQL transactions, review view, snapshot digest, export and native forms. No added dependencies or duplicate scheduler. Organization enforcement is at the application boundary; RLS, entity assignments, live revocation and operational controls remain in the delivery backlog.

## 2026-09-22 · Procedures and client assignments

- Applied local migrations 013–014. Approved procedure versions reuse immutable evidence, local proposals, bounded model inventory and existing source invalidation. No provider calls or new dependencies were added.
- `TestProcedureOverridesPinAndInvalidate` passes: client override precedence, foreign-organization exclusion, cited local account names, agent procedure context, pinned historical versions, explicit current-version application, idempotent reapplication and permission rejection. Observed the test fail without superseding the prior source, restored the update and observed green.
- Expanded `TestOrganizationIsolation` verifies an unassigned organization member cannot open a close, an exact client assignment enables it, another organization stays denied, unassigned client creation/admin access fail, and revocation removes access. Observed red without the assignment predicate and restored it.
- Strengthened the sign-off snapshot with job completion timestamps/plans and extraction attempts: a retried job returning to its old visible state cannot resurrect an old sign-off. Observed the check fail without completion freshness, restored it and observed green.
- Focused procedure, organization, sign-off, carry-forward, inbox, agent-contract and client-reply checks passed; frontend build and affected Go vet passed. Live WorkOS membership/role provisioning remains unverified.
- Browser-verified client procedure publication (#16, synthetic client #162), visible version/approval attribution, explicit application and resulting pinned source evidence. Access-administration form/history rendered correctly; grant/revoke behavior was verified through signed-session integration checks. Restored the normal private-token API and worker afterward.

## Approved Resend delivery (2026-09-22)

Synthetic local PostgreSQL test `TestApprovedEmailDurableRetryAndScope` passed: verified-domain gate, encrypted organization-bound credentials, explicit reviewer authority, durable attempt before send, stable payload/key across a lost response, one simulated provider creation, no terminal repeat, expired retry fence and stale-request cancellation. Removing the idempotency key made the test fail; restoring it passed. No real emails or provider accounts were used.

Direct consumers passed in the same focused run: organization isolation, scoped sign-off invalidation/reopening, client reply resume, Work coordinator, request revisions, package export and interpretation/citation contract. Delivery metadata is exported without secrets. Configuration and deployment YAML parsed; frontend workspace type checks passed. Live acceptance/delivery, production migration, Cloudflare activation and key recovery are not proven.

## Close-level preparation scope (2026-09-22)

`TestClosePreparationScopeReusesDurableJobs` passed with multiple source documents: a repeat start reused the same durable run and jobs; live child status appeared in the parent; adding evidence rejected the old confirmation; replacing a source marked the pinned scope stale; the next scope preserved history and excluded the replaced source. Removing the inventory digest fence produced the expected failure (200 instead of 409); restoring it passed. Direct Work, organization-boundary and export tests passed, as did focused Go vet, TypeScript checks and the production frontend build.

Browser check on synthetic close 162 uploaded `accounting-coordinator-ui.txt`, created run 10 linked to job 270, observed waiting-evidence → needs-confirmation, saw a real account-mapping conflict question, and followed the job link. No accounting facts were approved in this check. Retained synthetic evidence/run are labeled development. Earlier local-token login produced expected unauthorized requests before unlocking; no provider was called.

## Local recovery rehearsal (2026-09-22)

`PGHOST=127.0.0.1 PGPORT=15432 PGUSER=close infra/postgres/check-restore.sh` passed using native PostgreSQL custom-format dump/restore. Both disposable databases applied/restored all 16 migrations. Readback verified two firm owners with same-named clients, two source hashes, pending jobs/intakes and pinned scopes. Script cleanup removed its own databases/dump. No provider, worker, real credential or production backup was involved; hosted RTO/RPO remain unverified. Browser email settings also displayed the expected local unconfigured/manual-link state.

Minimalism review: retained the existing queue, request freshness function, source invalidation and calculation paths; no new runtime dependencies or workflow framework. Removed redundant joins from email claiming and limited coordinator polling to question/status metadata rather than repeating whole proposals and source text.

## Client-specific QuickBooks sandbox OAuth (2026-09-22)

Applied migration 017 only to local development. `TestQuickBooksScopedOAuthRefreshAndSnapshots` passed against synthetic PostgreSQL data and mocked Intuit HTTP: wrong state and wrong initiating user denied, callback routing checked, code replay rejected, explicit company confirmation required, foreign firm/role denied, encrypted token ownership checked, durable rotation persisted, two pages totaling 1001 accounts preserved including inactive accounts and exact large decimal values, exported snapshot hash matched the downloaded bytes, partial reads saved nothing, lost refresh response was not retried, disconnect removed credentials. Removing the OAuth state predicate caused the expected failure (200 instead of 409); restoring it passed.

Direct organization, export and legacy sandbox-posting checks passed. Focused Go vet, all workspace TypeScript checks and frontend production build passed. No live Intuit consent, account reads, postings or cloud deployment occurred. Account snapshots do not establish period-end balances or a complete ledger.

Browser verification on synthetic close 162 confirmed the unconfigured/local state, no connected company, no snapshot and unavailable OAuth actions. The full provider flow is verified by the mocked integration check, not by a live consent flow. Ponytail review retained native HTTP/PostgreSQL and existing auth/encryption; no SDK, background polling service or new runtime dependency was added.

## 2026-09-22 — bounded QuickBooks period evidence and coverage

Implemented sandbox TransactionList, GeneralLedger, TrialBalance and ProfitAndLoss reads for the saved month/accrual basis. Preserved raw report values; linked immutable hashed evidence, account-type coverage questions and trial-balance row references. Recapture supersedes prior ledger evidence; preparation scope pins snapshot/source/hash and rejects a stale confirmation. No migration or dependency added.

Focused PostgreSQL integration checks passed: QBO OAuth/refresh/account pagination plus period report capture, wrong-period rejection without persistence, exact decimal preservation, source hash/agent inventory, source supersession, two-account coverage, pinned close-run lineage, stale-scope rejection and cross-firm access. All HTTP calls intercepted by the existing synthetic transport. Wrong-period rejection was observed to fail (200 instead of 422) after temporarily removing the end-period fence; fence restored and focused test passed. Direct-consumer checks passed for close coordinator, agent answers/inventory fencing, organization isolation, scoped sign-off invalidation and ZIP export. Go vet for quickbooks/transport and frontend production build passed.

Browser check against the updated local API/Vite confirmed the close's new coverage section, explicit missing-capture state and preserved existing work/review UI. Authenticated live OAuth/capture UI was not exercised: no real provider credentials or hosted identity configured. No external account accessed, no email sent, no deployment performed. Reports remain sequential bounded reads, not proof of atomic completeness. Automatic coverage resolution and large-inventory evidence selection remain pending; semantic source selection requires the existing configured OpenAI provider. The period bundle limit is 96 KiB, and the existing combined agent input limit remains 20 files / 128 KiB.

## 2026-09-22 — coverage resolution, grouped requests and mandatory scanning

Applied migration 018 locally. Coverage resolves only from an accountant-confirmed complete job set and its current approved outputs; changed sources reopen affected requirements. Consolidated requests pin the ledger, bindings and target revisions. Replies resume only those jobs; quarantined attachments cannot enter agent inventory or preparation before a fresh, exact-byte clean scan and explicit firm release.

Focused PostgreSQL checks passed for coverage resolution, two-job replies, two-file idempotency, quarantine/release fences, disconnected-ledger rejection, requests before any job exists, source-change reopening, organization isolation, existing request/email flows, sign-off and export. Temporarily removing the clean-scan release guard made the integration test fail (200 instead of 409); restoring it passed. An unexpected multipart file-field regression likewise failed before its parser guard and passed afterward. Go vet for changed backend packages, workspace TypeScript checks and frontend production build passed.

Native ClamAV 1.5.4 with official signatures 28131 accepted a clean file and rejected the EICAR test file. Missing signature storage failed closed. A real local API/worker walkthrough on temporary synthetic close 255 created a two-requirement request in the browser, verified the scoped client attachment form, submitted a TXT attachment through HTTP, observed the durable clean scan, released it, and independently read back matching scan/content digests, released evidence and job 313 in needs_confirmation. The temporary close and its records were removed afterward. No actual accounting facts were approved.

Ponytail review reused existing PostgreSQL claims, request freshness, evidence and intake paths; removed a redundant request-key column. ClamAV is the necessary native scanner dependency; no new workflow framework or JS/Go package was added. Local signature files and scanner installation are retained for development. Docker is unavailable on this host: Compose/container execution and Cloudflare deployment were not verified. Cloud activation, live OpenAI/Intuit/Resend calls and group email delivery were not performed; grouped links remain manually shared. See coverage-requests.md for onboarding, limits and scanner operations.


## 2026-09-22 — hybrid scanning, separate on-demand text service

Replaced embedded/polling ClamAV with Cloudflare WAF for client PDFs and, following the user's clarification, a separate on-demand ClamAV service for TXT/CSV. Removed antivirus packages from the API/preparation image and the preparation worker's 6 GiB allocation. The isolated scanner has its own image/entrypoint and a Cloudflare container that sleeps after two idle minutes; only its server credential is supplied. Local Compose uses a private scanner listener, with signature updates and engine execution triggered by authorized uploads.

Focused PostgreSQL integration checks passed for trusted PDF receipts, wrong/missing attestation, exact-byte/request binding, rejection before a scan, TXT service authorization and rejection, and link revocation during scanning. The API releases database locks while waiting and rechecks authorization afterward. Removing the edge verdict check and origin trust check produced expected failures; removing the post-scan revocation predicate also produced a failure (200 instead of 409), then restoration passed. Existing scoped reply behavior remains covered.

The isolated native scanner HTTP service returned 403 for an invalid credential, 200 for clean synthetic text and 422 for EICAR. Its focused test also verifies missing signature storage fails closed. These are actual local engine checks; the Cloudflare verdict is mocked in application tests. Workspace TypeScript checks, frontend production build, focused Go vet and API/worker/scanner compilation passed. Edge and scanner Wrangler dry-run bundles passed; YAML parsing and Compose service-boundary checks passed. Docker is unavailable here, so container-image execution and actual Compose startup remain unverified. No hosted WAF rules, cloud resources, emails or accounting providers were activated.

Onboarding and deployment now require UPLOAD_SCAN_HOST, UPLOAD_SCAN_KEY, TEXT_SCANNER_URL, the WAF entitlement, managed result header and explicit blocking rules. See cloudflare-upload-scanning.md for installation and hosted acceptance checks. A private local scan key was generated in ignored .env; no key is present in source or test output. Native API, preparation worker and scanner run separately on loopback ports 8080, 8081 and 8082. Signature files are retained for scanner development. Peak scanner memory remains necessary; reduced cloud idle cost is a design expectation, not a measured billing result.

Minimalism review: reuse request links, quarantine, byte hashes and firm release; one small HTTP service with native ClamAV, no provider abstraction, queue framework or new package dependency. Legacy scan columns remain for historical records; no new schema migration was needed.

## Consolidated email, tracking and individually approved reminders (2026-09-22)

Migration 019 applied to the local development database. Reused the encrypted outbox for consolidated requests, with one initial send per request and one uniquely linked successor per reminder approval. Signed Resend events remain separate from send outcomes; no open/read inference is made.

Focused PostgreSQL checks passed for two-job consolidated delivery, immutable retry payload/key, early and out-of-order delivery events, tampered signatures, duplicate/conflicting event IDs, duplicate reminder approvals, original-link reuse, staff-reply blocking and a bounce cancelling an already queued reminder. Removing the worker's adverse-delivery guard produced the expected failing assertion; restoration passed. The official Svix signature vector and stale/tampered checks passed. Direct inbox and close-readiness/export regression checks passed, as did frontend TypeScript/build, focused Go vet and API/worker builds.

Ponytail review reused PostgreSQL uniqueness, the existing sender settings, outbox, request freshness and reply-resumption flow. Standard-library HMAC verifies provider signatures; no new dependency, scheduler or delivery framework was added. Removed a redundant worker join and duplicated UI status paragraph.

All provider requests were mocked; no client email was sent. Hosted identity, actual Resend callbacks/delivery and Cloudflare/Compose runtime remain unverified. Docker is unavailable on this host. See email-delivery.md for migration, webhook onboarding, recovery and staging acceptance. The native local API was restarted with updated code; browser verification confirmed the unconfigured email UI preserves manual sharing. No hosted send/reminder form was browser-tested.

## Explicit prepaid methods, credits/prior balances and incurred accruals (2026-09-22)

Applied migration 020 to the local database with API/worker stopped. Added inclusive actual-day allocation, one prospective month-boundary credit, differences against confirmed booked prepaid balances, and fixed incurred accrual amounts with an explicitly selected next-month reversal proposal. Existing equal-full-month outputs remain compatible; new methods do not reach sandbox posting.

Focused checks passed for exact-cent conservation, leap day/month boundaries, one-day service, credit limits, unchanged pre-credit expense, booked-balance no-op/correction, accrual differences and reversal directions, unconfirmed-method rejection, cited/contradictory agent facts, durable preparation, pending-before-review status, accountant review records, source-change invalidation and exported results. Removing inclusive day counting and booked-liability subtraction separately produced the expected failing assertions; both fixes were restored. Existing direct-consumer checks for agent answers, Work, carry-forward, procedures, evidence, saved closes, sign-off/export, stateless preparation and sandbox attempts passed. Frontend typecheck/build, focused Go vet and native API/worker builds passed.

Synthetic examples are visible in the forms and documented in accounting-methods.md. Browser verification confirmed the $100 January 16–March 15, 2028 actual-day schedule ($26.66/$48.33/$25.01) and February remaining prepaid $25.01. No live provider, email or ledger action was performed. The client's accounting professional has not yet accepted these methods for a real client. Cloudflare/Docker runtime activation remains unverified.

Ponytail review reused exact-cent money parsing, existing prepaid rows/journal shape, evidence revisions, durable jobs, procedures, review permissions and export. No new dependencies, scheduler, calculation framework or posting path. Accruals are a separate small calculator; the methods share the current approval flow. Multiple credits, retrospective corrections, mid-month cancellations, automatic estimates, multi-currency and automatic accrual carry-forward remain unsupported.

Browser follow-through also verified the prospective-credit example's February prepaid $21.60 and $8.40 debit-expense/credit-prepaid adjustment, plus the fixed accrual's February 29 $1,000 proposal and March 1 $1,000 opposite-direction reversal. These were unsaved synthetic previews; no accounting approval was asserted. Updated native API/worker are running locally. Worker restart checked zero email connections, zero pending messages and no email encryption key before execution.

## Investigation hints and coordinated stop — 2026-09-22

The reconciliation fixture has multiple statement and ledger rows. A focused check verified one exact candidate remains a match while ambiguous exact keys, same signed amounts on another date, same-date different amounts and unpaired rows remain unmatched with source-row questions. Removing the investigation calls made that check fail; restoring them passed. The close package writes these hints to a separate CSV.

The synthetic PostgreSQL coordinator check passed for a two-job scope, stale stop digest rejection, cancellation of both unresolved jobs, revocation of an unused client reply link, preservation of the parent record and explicit restart of one stopped job. Replacing the cancellation transition and disabling link revocation separately made the relevant assertions fail; restoring both passed. Direct Work and export checks, frontend typecheck/build and focused Go vet passed. No actual client message or ledger operation was triggered. Live Cloudflare, OpenAI and email-provider behavior were not exercised.

The updated local API and frontend were opened on temporary loopback ports with a synthetic close. The browser showed a proposed prepaid item with missing ledger coverage, rendered the stop review control, stopped one unresolved job, showed `0 blocked · 1 stopped`, and exposed **Restart this stopped job** in Work. The reconciliation example rendered one unmatched ledger row with its source-row investigation question. The temporary browser tab and servers were closed. Synthetic close #301 remains in the local development database for inspection; it is not a real client close.

Ponytail review of the final diff found no new dependency, status store, policy engine or second calculation path to remove. The change reuses the existing unmatched rows, durable jobs, review permissions and source-link controls.

## Cancellation, large-evidence selection and recurring follow-up — 2026-09-22

Focused Go tests and vet, the frontend production build, and disposable-PostgreSQL integration checks passed. Tests covered both explicit month-boundary cancellation calculations and their blocked cases; large-inventory OpenAI selection used a mocked provider and rejected a missing primary source. The agent integration check rejected confirmation without review of unused evidence, then accepted explicit acknowledgment. The carry-forward integration check verified that historical booked balances do not leak into new-month drafts, approved procedure application is idempotent, unresolved prior work becomes an incomplete sign-off step, and repeating the same carry does not duplicate that step. The ZIP check verified two captured ledger payloads are included.

The global Work inbox now previews per-close stop counts and reports each selected close's outcome. This uses the existing scoped stop endpoint; no new bulk writer was added. No live OpenAI selection, QuickBooks/Resend operation, accountant policy acceptance, hosted migration or Cloudflare deploy was performed. The local test database is disposable and separate from the development database.

Final focused follow-up: the stop endpoint now locks approved outbox rows before changing job state. The synthetic PostgreSQL test rejects a stop while delivery is `sending`, then stops the same work and cancels a `queued` delivery. The frontend production build, transport Go vet and diff whitespace check passed after the follow-up. Captured QBO report cells are not normalized into authoritative current balances; a new-month ledger comparison remains pending accountant-approved mapping and live provider validation.

## Stripe settlement reconciliation (2026-09-24)

Author: Claude Opus 5.5 (session model). Built in a fresh worktree and rebased onto `origin/main` 31b9402 (after PRs #17 and #18); all 37 migrations, including 037, were applied only to the disposable local database `accounting_stripe_test`.

- `TestSettlementTracesSourcePayoutsToBankRows` uses a 13-row, seven-payout Stripe fixture (charges, refunds, a dispute, a fee row, `other_adjustment`) with a BOM, extra columns and quoted commas. It covers: unique candidates, a negative payout matched to a withdrawal, a Sep 30 payout whose deposit is dated Oct 2, a decoy of the same amount outside the window, equal amounts competing for the same rows, a payout with no deposit, and a row with no payout ID (blocks and is never grouped). Reviewer choices resolve every blocker. Nine rejection paths are checked, including in transit while a candidate exists, a missing rule reference, non-USD, net ≠ gross − fee, and a payout outside the month.
- Observed red, then restored green: widening the candidate window made po_A ambiguous; grouping by row instead of `automatic_payout_id` broke the batch totals.
- `TestStripeExportIsRecognizedPreparedAndExported` (PostgreSQL) covers: header recognition under the local engine and, before any model call, under the OpenAI engine (a counting transport saw zero calls); a wrong source role rejected; the Work job continued with processor_report + bank evidence; worker output bound to evidence names and digests; `workpapers/{id}-payouts.csv` in the package. Observed red with recognition removed (proposal stayed `prepaid`), with the agent short-circuit disabled (one model call), and with the export case removed (HTTP 500).
- Direct consumers passed after the rebase: procedure learning and model drafting (all three tests), Work coordinator, agent inventory fence, card preparation gate, evidence preparation, saved-close round trip, package export, close preparation scope, procedures, reference boundary. Existing reconciliation tests pass after `lines()` gained a date-range variant. Go vet, gofmt, frontend typecheck and production build passed.
- Live local processes (isolated API, worker and Vite on alternate ports, same disposable database): the uploaded example export was recognized ("5 automatic payouts expected 2026-09-03 to 2026-09-30 from 8 balance transactions"). The continued job completed with draft 7 and no blockers, the review endpoint recorded `approved`, and the exported payout table marked the Oct 1 deposit as after month-end.
- Browser: the standalone page traced the synthetic example (3 of 5 traced, 2 needing a decision). Choosing B-0924 for one $193.90 payout recalculated, the other then matched B-0925, and Undo was shown. The close-mode selectors, Work card and saved-draft rendering were not browser-tested, because unlocking the workspace means typing its token into the page; those paths are covered by the typecheck, the integration test and the live API walkthrough.
- Plan reviewed once by GPT-6 Sol (medium). Accepted: an accountant rule for in transit, bank rows up to four days past month-end, explicit export-coverage confirmation. Rejected: a full-inventory digest for recognized exports, since the proposal depends only on the intake file inside one locked transaction, and the chosen bank evidence is re-verified at confirmation and in the worker.
- GPT-6 Sol (medium) reviewed the committed diff and requested changes. Three findings were accepted and fixed: a stale file choice during loading, the sourceless direct save (removed from the UI and `/workpapers`), and a headline that ignored unassigned rows. Delta review closed two. The remaining A→B→A selection concern was rejected: evidence text cannot change once extracted. `documents/extract.go:140` is the only content write; retry is allowed only from `failed` (`transport/evidence.go:205`), and only `ready` evidence is served.

Limits: exports only (no live Stripe API or MCP). Automatic USD payouts only. No fee or revenue journal proposal. A bank CSV uploaded to the close still gets the existing local prepaid proposal in the Work inbox. No accountant has accepted this method on real client data.

## Workspace redesign and review canvas (2026-09-24)

Author: Claude Opus 5.5. Worktree from `origin/main` cd96e9d; disposable database `accounting_workspace_test` with all 37 migrations.

- Design: the product shell follows the supplied reference (light sidebar with grouped sections and client search on `/`, card KPIs, segmented filters, avatar rows, status dots and chips). Styles are scoped under `.app-shell`, so the marketing site and the client request page are unchanged. No charts: none answers an accounting question better than a sentence.
- IA: Work first. Firm administration moved to `/dashboard/firm`. The close page is split into URL-addressable sections that reuse every existing panel unchanged. A single access gate replaces the duplicated unlock forms; enrollment stays reachable from the firm/membership error.
- Review canvas: evidence as received (source tabs, numbered lines, cited lines highlighted and scrolled into view) beside the calculation, blockers, decision (auto-loaded) and existing posting actions, with previous/next through drafts ready for review.
- `TestWorkCountsAndReviewQueueMatchTheirLists`: every Work view's count equals its list; client search spans matching closes; the queue holds only the current, linked, unblocked draft; close search filters server-side. Observed red when counts ignored the view predicate and when the no-blockers rule was removed. Removing an explicit "no newer revision" rule stayed green because the review view (migration 005) already marks revised predecessors as changed, so that rule was deleted from SQL and UI.
- Direct consumers passed: organization isolation (the boundary allowlist gained `/review-queue`), Work coordinator, saved-close round trip, close preparation scope, agent answers. Frontend typecheck and build passed.
- Browser (isolated API/worker, seeded synthetic closes): gate, sidebar, Work KPIs/queue/segmented list, sidebar search, close KPIs and sections, Firm settings, and the canvas (cited lines 3–5 highlighted), including an approval that updated the pill and emptied the queue. Authenticated pages were reached through a local test proxy that injected the disposable instance's token; the browser only received a placeholder.

## Close planning, ranking and bulk approval (2026-09-24)

Author: Claude Opus 5.5. Stacked on the workspace redesign; migration 038 applied only to the disposable database.

- `TestClosePlanningRanksWorkAndBulkApprovalChecksEachDraft`: rejects an invalid due date; self-assignment works; taking or releasing a colleague's close returns 409; a due-soon close I own ranks ahead of an older undated one in the same bucket; Mine and due-soon counts match; the queue flags a ledger-adjusting draft for individual review; a batch approves the routine draft and separately refuses a stale digest, the adjusting draft and another firm's draft, writing exactly one approval. Observed red with the per-item access check removed (the foreign draft was approved) and with the new ordering removed.
- Browser (proxy-injected disposable token, seeded closes): five KPIs including Due in 7 days, ranked rows with due/priority/Mine chips, a disabled queue checkbox with its reason, a bulk approval of one settlement draft with the full confirmation panel, and the close header planning controls.

## Human effort measurement (2026-09-24)

Author: Claude Opus 5.5. Stacked on close planning; migration 039 applied only to the disposable database.

- `TestEffortCapsCreditAndSeparatesWaiting`: three 30-second beats in one minute credit at most 60 seconds (robust to a minute boundary); unknown activities, oversized beats and another firm's close are rejected; a recorded 15-minute entry adds to human minutes; client wait counts only the answered request (delivery to first reply ≈ 2 h) and reports one delivered request without a reply while ignoring an undelivered one; the monthly report's median uses the measured close only and the quiet close reports no total. Observed red with the 60-second cap removed (app and constraint) and with unmeasured closes counted as zero.
- Browser: Measure page (median 21 min, 1 of 3 measured, unmeasured rows) and the close's Human effort card with tracked, recorded and waiting figures.

## Posting safety: closing date and independent review (2026-09-24)

Author: Claude Opus 5.5. Stacked on human effort measurement; migration 040 applied only to the disposable database.

- `TestOpenPeriodFollowsTheCompanyClosingDate`: refuses a date equal to the closing date, allows the day after, allows no closing date, and blocks a missing Preferences block or malformed date. Observed red when a date on the closing date was allowed.
- `TestProductionPostingSingleDurableAttempt` now also proves that a closing date on or after the journal date refuses execution (422) and a changed but earlier closing date rejects the stale intent (409), both with zero writes, before the original single verified write. Books that close after that write still verify: readback does not recheck the closing date (observed red when it did). `TestVendorCreditRefusesAClosedPeriodBeforeAnyWrite` stops before any vendor, account or write request. Sandbox posting and Bill mocks serve Preferences.
- `TestIndependentReviewPolicyRefusesPreparersAndUnknownProvenance`: under the policy the job's creator, the direct saver and drafts without provenance are refused; another member approves; the posting gate refuses a preparer's approval; policy changes need org:manage on a hosted firm, are recorded, and the local workspace cannot enable the policy (database constraint). Observed red with the check disabled.

## Exception investigation and reconciliation CSV recognition (2026-09-24)

Author: Claude Opus 5.5. Stacked on posting safety. No migration.

- `TestFindExceptionsRaisesQuestionsWithoutConcluding`: flags only the nearby same-vendor pair (not the same payment 26 days later, another vendor, or another currency), the Uncategorized Expense line, and the payee-less Purchase; a JournalEntry without a payee is not flagged. A third equal payment six days after the first starts a new cluster, and a EUR amount is shown as EUR, not dollars. Observed red with a 31-day window, with currency dropped from the grouping, with the window measured from the previous payment, and with every amount printed in dollars.
- `TestExceptionsScanTheWholeLatestCapture` (PostgreSQL): no capture returns `capture: null`; a capture with three rows reports `scanned: 3` and one possible duplicate.
- `TestRecognizeExportLeavesTheReconciliationRoleToTheReviewer`: an `id,date,amount` CSV becomes a `reconciliation` proposal with its row count, date range and net total; a header-only CSV is zero movement (observed red when it was rejected); a row outside the month stays unsupported with the reason; an invoice is not recognized; the agent path does not short-circuit CSVs, so `TestAgentAnswersResumeAndInventoryFence` keeps its paired statement, ledger and balances (it failed while the agent path recognized them). Observed red with the CSV check removed. `TestStripeExportIsRecognizedPreparedAndExported` still passes.
- Browser, with a local API, worker and Vite on the disposable database: the Overview card listed a no-payee purchase, a CloudSuite pair two days apart and an Uncategorized Bill, and left the monthly rent payment alone. A close without a capture showed the capture prompt. An uploaded bank CSV showed **Recognized reconciliation CSV** with no confirm form, and its button opened the reconciliation page with the job continuation.

## Machine-readable error codes (2026-09-24)

Author: Claude Opus 5.5. Stacked on exception investigation. No migration; messages and statuses are unchanged.

- Every `problem()` response now carries a status-derived `code`. `SOURCE_STALE` is used only where the branch proves a replaced or changed source (9 sites plus the replaced-source branch of Work actions, split from its not-yet-extracted case). `EXECUTION_UNKNOWN` covers the 7 uncertain-outcome or unverified-readback provider writes. At the stale-source sites, a failed lookup returns `UNAVAILABLE`, a missing source stays `SOURCE_STALE`, and a period with no evidence yet is `CONFLICT`.
- `TestWorkInboxCoordinator` asserts `CONFLICT` on a repeated confirmation and `SOURCE_STALE` after the source is replaced. `TestProductionPostingSingleDurableAttempt` asserts `EXECUTION_UNKNOWN` on the lost response. Observed red with `code` removed from `problem()`, with the replaced source reported as `CONFLICT`, and with the production site left on the status default.
- Plan reviewed once by GPT-6 Sol (medium). Adopted: no generic Idempotency-Key cache (replay would bypass revoked access and could release an unresolved write), named codes only where the branch proves them, and schemas deferred to the first exposed capability.

## MCP access (2026-09-24)

Author: Claude Opus 5.5. Adds `github.com/modelcontextprotocol/go-sdk` v1.8.0 and migration 041 (`approval_requests`).

- `TestMCPToolsUseTheSignedInPersonsAccess` runs against a real HTTP server. The official SDK client connects on protocol 2026-07-28 and lists the tools; none approves, signs off or posts. `list_closes` returns the person's close, while another firm's close returns `NOT_FOUND`. `add_evidence` starts one intake job. `request_approval` reaches the review queue without recording a review, and is refused (`CONFLICT`) for a draft with blockers that the queue hides. A configured origin listed after a comma and space is served. A raw `initialize` for 2025-06-18 is served, with `Cache-Control: no-store` (observed red without the override, since the SDK sets `no-cache`). Mismatched MCP headers get 400, no token gets 401 with `WWW-Authenticate`, and an untrusted `Origin` gets 403.
- Observed red with the caller's credentials not forwarded to the replayed call, with the origin check off, with stateful mode (the client fell back to 2025-11-25), and with MCP authentication off.
- Full `internal/transport` and `internal/auth` packages pass; frontend typecheck passes.
- Browser, local services: an agent's `request_approval` sent over MCP with curl appeared in Work → Ready for your review as "Approval requested" with its note. Long queue lines now truncate instead of running under the status column.

## Loan schedules (2026-09-24)

Author: Claude Opus 5.5. Migration 042 adds the `loan` workpaper and procedure kinds.

- `TestLenderSplitIsCheckedAndOnlySupportedEntriesAreProposed` (two payments in a month): note-rate interest is recomputed on the principal outstanding (620.00, 276.50). The lender's 280.00 is a question, and the split is unchanged. Accrued interest is 19.67 for one day, and the current portion reclass is 2,000.00. A 1,000.00 book difference is a question that blocks the reclass, and an over-accrual reverses 5.33. An untied statement blocks and proposes no entry (observed red when entries survived a blocker). An unconfirmed rate, or an ambiguous "30_360", is refused.
- `TestDayCountConventions` covers Actual/360, Actual/365, 30/360 bond basis (31st-day rule) and 30E/360.
- `TestLoanDraftsNeedTheClientsApprovedLoanProcedure` (PostgreSQL): a disabled client loan procedure refuses the save, an enabled one allows it, and disabling it again refuses new drafts. A saved loan with an entry is ineligible for bulk approval. Publishing a loan procedure requires the `lender_statement_v1` method.
- Observed red with the 30/360 bond D2 rule changed, the tie-out off, accrual counted from the paid-through date instead of the last payment, the reclass gate off, interest computed on opening principal, the procedure gate off, bulk exclusion off, and the publish method rule off.
- Browser: published a firm-default loan procedure in Settings, which showed the approved method boundary. The calculator example showed the schedule, question, accrual and reclass. Saving was refused while the procedure was disabled and saved once enabled, and the draft opened on the review canvas.

## Accounting basis (2026-09-24)

Author: Claude Opus 5.5. Migration 043 adds the `basis` procedure kind. Stacked on loan schedules (#28).

- `TestTheClientsBasisDecidesWhichWorkCanBeSavedAndApproved`:
  - a firm-default cash basis refuses accruals, and a client override (accrual) wins over it;
  - a cash client's accrual, prepaid, and interest-accruing loan saves are refused, while a loan without accrual saves;
  - an accrual saved before the change can't be approved;
  - the close returns `basis: cash, basis_scope: client`;
  - publishing refuses a basis without a method, a disabled basis, and a method on a prepaid procedure.
- Observed red with precedence reversed, the save check off, the approval check off, and the always-enabled rule off.
- Browser: published a cash-basis client override for Northstar in Settings. The close header showed "Cash basis · client override", and the prepaid and accrual actions were hidden.

## Owner-related reclassifications (2026-09-24)

Author: Claude Opus 5.5. Migration 044 adds the `owner` workpaper and procedure kinds. Stacked on accounting basis (#29).

- `TestOwnerReclassesFollowTheConfirmedPurpose`:
  - personal → draw (Dr Owner's draw / Cr Meals 100.01);
  - 33% business use → 67.01 personal share;
  - a business cost paid personally → Dr Software / Cr Owner's contribution 33.00, refused unless confirmed not already booked;
  - 100% business → no entry;
  - "Due from owner" with a repayment confirmation → receivable;
  - an open purpose → a question with a blocker and no entry;
  - refused: due-from without repayment, an undeclared C corp distribution, a partner draw without a partner, no stated purpose, a date outside the month, and a contribution for a personal cost.
- `TestOwnerDraftsNeedTheClientsOwnEntityType`: a firm-level entity type, even inserted directly, doesn't count. A mismatched client type is refused and the recorded type saves. A question-only draft saves. Publishing refuses a client override without a type, a firm default with one, and an unknown type.
- Observed red with the split inverted, the business-cost direction reversed, the C corp and repayment rules off, the firm default counted, the type check off, and the publish rule off.
- Browser: recorded Harbor Dental as an S corporation under Owner treatments. The owner item prefilled "Shareholder distributions", prepared Dr Shareholder distributions / Cr Meals 86.40, and saved.

## Agent access for hosted MCP (2026-09-24)

Author: Claude Opus 5.5. Migration 045 adds `agent_access`, `current_agent_access` and `effective_agent_access`.

- `TestOAuthResourceTokensMustNameTheResource`: an AuthKit token is refused without the MCP audience, with another audience, from another issuer, or without `sid`.
- `TestAgentsActOnlyUnderAnApprovedMemberGrant` runs against a real HTTP server, with AuthKit keys served through a swapped transport:
  - no grant, or only a pending request → 403; a token for another resource → 401 with the metadata pointer;
  - an unknown scope and self-approval are refused; an administrator can't widen a request; the administrator list shows each member's access in force (observed red with the earlier invalid SQL);
  - read approval → only read tools, and `list_closes` shows the assigned client but not the other one;
  - a pending upgrade keeps read access; an approved upgrade adds the prepare tools;
  - re-enrollment doesn't revive the old grant;
  - protected resource metadata is served.
- Observed red with the membership binding off, the pending-request rule off, widening allowed, scope filtering off, the grant requirement off, and the audience check off. Agents never receive firm-wide access; decisions are serialized with the firm row lock.
- Full `internal/transport` and `internal/auth` packages pass; frontend typecheck passes. The Firm settings card couldn't be exercised in a browser: hosted sign-in isn't available locally, where the card shows the workspace-token note.

## Row-level security (2026-09-24)

Author: Claude Opus 5.5. Migration 047.

- `TestRowLevelSecurityIsolatesFirms` (PostgreSQL, through the API connection wrapper as a non-superuser, non-owner role): with firm A signed in, closes, workpapers, reviews (through their workpaper), the review-state view and organizations return only firm A's row; with no firm nothing is visible; a system-marked handler sees both; writing a close into firm B fails; after a rolled-back transaction changed the firm, the next statement sets it again; a prepared statement is refused (it could later run as another firm); the readiness query is true for that role and false for a superuser. Observed red with the wrapper not setting the firm, with the system mark ignored, without the rollback reset, with prepared statements allowed, with RLS disabled on the reviews table, with the view running as its owner and with an open organizations policy.
- One-off readiness check: with the probe role granted a table owner's role WITH INHERIT FALSE, the readiness query using `MEMBER` reports not enforced; with `USAGE` it wrongly reported enforced.
- One-off probe (not committed): the full transport suite with its pools opened through the wrapper as a non-owner role, and with the API's own pool failing closed. 50 of 51 tests passed. The one failure calls the posting handler directly as firm `org_fixture` against a `local-development` close; the database refused that cross-firm read, as intended (the application boundary would refuse the same request). Removing the system mark from the client-request route made two client-request tests fail, showing the probe exercised it.
- Backup expiry verification is not included: no PostgreSQL provider is provisioned, so retention cannot be checked against a provider or contract.

## Year-end handoff (2026-09-24)

Author: Claude Opus 5.5. No migration.

- `TestYearEndHandoffRollsForwardApprovedWork` (PostgreSQL): January to May with April missing; a prepaid with a February credit rolls forward without a break; a prepaid whose schedule disagrees with the prior approved balance, a loan whose opening differs from the prior closing, a prepaid with a prior-month balance but no prior paper, and a pending latest version (its older approved version is not used), a prepaid whose schedule omits a month that has an approved paper, and a loan with opening principal but no prior-month paper each become open questions (a loan opening at zero is new and is not asked about); a bank and a card reconciliation with one name stay separate; a verified posting on the older version stays in `postings.csv`; the prior year, another client and another firm are excluded; checksums verify. Observed red with the first version kept instead of the latest, with a closing-minus-expense rule in place of the saved schedule, with each continuity or missing-paper check removed, with loans opening at zero asked about, with the client, firm or year boundary removed, with reconciliations keyed by account alone and with unapproved versions included.

## Deeper exception finding (2026-09-24)

Author: Claude Opus 5.5. No migration.

- `TestAbnormalBalancesReadTheTrialBalanceColumns`: from a Trial Balance in either Debit/Credit column order, flags a receivable credit, a credit-card debit and an overdrawn bank; skips accumulated depreciation, a retained-earnings deficit, an owner draw, a normal expense and an account of unknown type. A EUR report yields a EUR question, and an unreadable amount fails the check instead of passing as normal. Observed red with expenses treated as credit-normal, with contra subtypes, owner draws or unknown types not skipped, with the Debit column not found, with the currency dropped and with unreadable amounts skipped.
- `TestFindExceptionsRaisesQuestionsWithoutConcluding` now also flags one vendor whose three purchases and one bill went to two accounts (each account named once) without mixing in a customer with the same ID, flags two accounts that share a name, and does not flag one split purchase or the same split in a different line order. Observed red with transaction counts in place of distinct account sets, with unsorted sets, with repeated account names, with the payee type dropped from the key, with VendorRef not treated as a vendor and with sets compared by account name.
- `TestExceptionsScanTheWholeLatestCapture` (PostgreSQL): without a snapshot `balances.snapshot` is null; with two period snapshots and no transaction capture, balance items come from the newer snapshot. Observed red with the oldest snapshot chosen and with balances dropped when no capture exists.
- No personal-spending check: nothing in the captured data separates a personal purchase from a business one, so purpose stays a client question.

## Posting beyond prepaids (2026-09-24)

Author: Claude Opus 5.5. Migration 046 moves production grants to account and kind lists.

- `TestApprovedLoanAndOwnerEntriesPostUnderTheirGrant` (mocked production QuickBooks):
  - loan posting is refused until `PRODUCTION_POSTING_KINDS` enables it, and a grant covers only the kinds it names;
  - the grant limit applies to the journal's 2,019.67 total debits;
  - the loan's two entries post as one four-line journal. Projected and verified balances are 119.67 for the expense and 19.67, 98,000.00 and 20,000.00 for the liabilities;
  - an owner account name matching two granted accounts is refused, with no write.
- `TestProductionPostingSingleDurableAttempt` and the rehearsal tests pass on the new grant and observation shapes, including closing-date and readback cases.
- The migration's intent rewrite was checked on a synthetic legacy row: `{prepaid, expense}` became `accounts` with Asset and Expense classifications, in both `ledger_before` and `ledger_after`.
- Observed red with liability signs treated as debit-normal, ambiguous names allowed, the limit on the largest line instead of the total, the release gate off, and the grant-kind check off.
- Not verified live: how QuickBooks moves `CurrentBalance` per account type. New kinds stay off until the sandbox check in production-posting.md.

## Xero month-end Trial Balance (2026-09-24)

Author: Claude Opus 5.5. Migration 048.

- `TestBalancesJoinReportRowsToAccountsByID`: a multi-section Trial Balance with a summary row yields four account rows in the report currency; columns follow the header titles; an account renamed in the report takes its name and code from the chart by account ID; an account missing from the chart keeps its report label and no class. Observed red joining by name.
- `TestXeroConnectsOneOrganisationAndCapturesTheTrialBalance` (PostgreSQL, Xero mocked): read-only granular scopes; the public callback returns to its close's Evidence tab, where the Xero panel lives; action responses report the hosted session; another user cannot finish the sign-in and a consumed sign-in cannot be replayed; only this sign-in's organisations are offered (not an earlier consent or a practice); an unlisted organisation is refused; the capture reads `date=2026-09-30` with the chosen organisation's tenant header and stores a digest that matches its payload; two expiries in a row succeed only because each rotated refresh token was saved; a snapshot from the next month's close blocks deleting the opening close along with the live connection; disconnecting clears the credential. Observed red without the sign-in or organisation-type filter, without the listed-organisation check, without saving the rotated token, with the wrong month-end, without the callback user check, without either retention blocker, with the return landing on the Overview tab and without `hosted` in action responses. The panel clears the returned code from the address only after the exchange succeeds (typecheck only; no frontend test harness).
- One-off key rotation: a Xero credential sealed with the old key was re-encrypted by `cmd/rotate-secrets` ("Re-encrypted and checked 1 records") and opened with the new key.
- One-off row-level security check: the Xero test passed as a non-superuser, non-owner role through the tenant connection wrapper, and `TestRowLevelSecurityIsolatesFirms` confirms both Xero tables have policies for the runtime role.

## QuickBooks return tab and posting fixture (2026-09-24)

Author: Claude Opus 5.5. No migration.

- `TestQuickBooksScopedOAuthRefreshAndSnapshots`: a sandbox authorization returns to the close's Evidence tab and a production authorization to its Settings tab, where each panel lives. Observed red with each tab removed or swapped.
- `TestSandboxAttemptIsDurableAndNotRepeated` now creates its close in the same firm as its actor. It still calls the posting handler directly on a plain connection, so it proves posting behavior, not row-level security; the dedicated RLS test covers that.

## QuickBooks change hints (2026-09-24)

Author: Claude Opus 5.5. Migration 049.

- `TestQuickBooksWebhookMarksOnlyActiveSandboxCompanies` (PostgreSQL): without a verifier the endpoint returns 503; a wrong signature returns 401; a body that is not an array, or has no event with CloudEvents 1.0 fields and a `qbo.<entity>.<operation>.v<n>` type, returns 400; none of these mark anything. A valid event marks only the active sandbox connection for its company, not a production or disconnected one with the same company, and an invalid event beside it is ignored; one notification marks two companies. Observed red without the signature check, the sandbox filter, the active filter or the event validation.
- `TestChangeHintsRefreshAfterTheCaptureThatMissedThem` (PostgreSQL): a change before the last capture started queues nothing inside the daily window; a later change queues one `quickbooks-change-hint` refresh, not duplicated; a change after a refresh was queued but before it started is not repeated; a change during a running capture queues a refresh once it finishes. Observed red without the hint condition and when comparing with queue time instead of start time.
