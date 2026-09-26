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

## Wrong-side balances from Xero (2026-09-24)

Author: Claude Opus 5.5. No migration.

- `TestWrongSideReadsYTDColumnsWithXeroRules`: from Xero's YTD columns, flags an overdrawn bank as a balance and an expense credit as a year-to-date balance; skips a receivable on its normal side, an account with equal YTD debit and credit, accumulated depreciation, retained earnings (system account), owner drawings and an account missing from the chart. A report without YTD Credit and an unreadable amount both fail the check. Observed red with each skip removed, without the year-to-date wording, without the YTD Credit requirement and with the credit column ignored.
- The QuickBooks decision moved into `internal/ledger`; `TestAbnormalBalancesReadTheTrialBalanceColumns` and `TestFindExceptionsRaisesQuestionsWithoutConcluding` pass unchanged.
- `TestExceptionsScanTheWholeLatestCapture` (PostgreSQL): a Xero snapshot captured after the QuickBooks one supplies the balances with source `xero`; when the QuickBooks snapshot is newer it supplies them instead; a later Xero row captured earlier does not replace the most recent capture. Observed red in each case.

## QuickBooks return tab and posting fixture (2026-09-24)

Author: Claude Opus 5.5. No migration.

- `TestQuickBooksScopedOAuthRefreshAndSnapshots`: a sandbox authorization returns to the close's Evidence tab and a production authorization to its Settings tab, where each panel lives. Observed red with each tab removed or swapped.
- `TestSandboxAttemptIsDurableAndNotRepeated` now creates its close in the same firm as its actor. It still calls the posting handler directly on a plain connection, so it proves posting behavior, not row-level security; the dedicated RLS test covers that.

## QuickBooks change hints (2026-09-24)

Author: Claude Opus 5.5. Migration 049.

- `TestQuickBooksWebhookMarksOnlyActiveSandboxCompanies` (PostgreSQL): without a verifier the endpoint returns 503; a wrong signature returns 401; a body that is not an array, or has no event with CloudEvents 1.0 fields and a `qbo.<entity>.<operation>.v<n>` type, returns 400; none of these mark anything. A valid event records its own change time (never earlier than a time already recorded; a future time is refused) for only the active sandbox connection for its company, not a production or disconnected one with the same company, and an invalid event beside it is ignored; one notification marks two companies. A replayed or late event therefore cannot trigger a capture that already started after the change. Observed red without the signature check, the sandbox filter, the active filter, the event validation or the event-time requirement, with the delivery time recorded instead of the change time, when the time could move back and when future times were accepted.
- `TestChangeHintsRefreshAfterTheCaptureThatMissedThem` (PostgreSQL): a change before the last capture started queues nothing inside the daily window; a later change queues one `quickbooks-change-hint` refresh, not duplicated; a change after a refresh was queued but before it started is not repeated; a change during a running capture queues a refresh once it finishes. Observed red without the hint condition and when comparing with queue time instead of start time.

## Loan rate changes and fees (2026-09-24)

Author: Claude Opus 5.5. No migration.

- `TestRateChangesSplitTheInterestWindow`: a 1 September change from 6.000% to 6.500% explains the lender's 645.00 exactly (16 days at the old rate, 15 at the new) and lists both rates; without it the same statement is a question naming 6.000%; a change effective on the payment date covers that one day (621.67); on Actual/365 the window rounds once to 530.14 where segment-by-segment rounding gives 530.13; on 30/360 bond basis a 1 February change keeps 29 January → 15 February at 16 days (343.33). Observed red when the rate starts a day late, when segments are rounded separately and when 30/360 segment days are counted separately.
- `TestFeesAddedToTheBalanceBearInterestAndAreProposed`: a 250.00 fee on 5 September bears interest for 10 days (620.42), ties opening − principal + fees to the lender's closing principal and is proposed as debit Loan fees, credit Loan payable with no book difference; a fee on the payment date follows the payment; a fee after the last payment still ties; booking 300.00 proposes 50.00 back. The fixed method, a missing or loan-named fee account, missing booked fees, a fee in the next month, a rate change before the paid-through date, one without its notice and changes out of order are refused. Observed red without fees bearing interest, with the entry reversed, without the fees in the book difference or the statement tie, and without the variable-method requirement.
- `TestLoanDraftsNeedTheClientsApprovedLoanProcedure` (PostgreSQL): a variable-rate draft is refused while only the fixed procedure is approved and saves once the client's variable procedure is. Observed red when the approved method need not match and when the variable procedure is not accepted.
- Year-end `loans.csv` gains a `fees_added` column; fixed-rate months report 0.00 (observed red when left blank). The loan form offers both approved procedures and switches to the variable one when a rate change or fee is added.

## Daily books (2026-09-24)

Author: Claude Opus 5.5. Migration 050.

- `TestTransactionChecksReuseEvidenceAndRefreshIndexedRows` now checks that a completed capture records its exceptions and an unchanged capture carries the same ones, and that a capture finished before exceptions were recorded is re-indexed rather than reused. Observed red without each.
- `TestDailyBooksShowsExceptionsNewSinceTheLastReview` (PostgreSQL, hosted firm actor through the organization boundary): unreviewed closes list every finding, most new first; signed-off and other firms' closes are left out; reviewing an older capture is refused; reviewing the latest clears what it covered; the same IDs reported under another transaction kind, and a new type, are new; after reconnecting, the old baseline no longer applies. Observed red without the kind in the finding key, without the same-connection baseline rule, without the signed-off and firm filters, when an older capture could be reviewed and without ranking by new findings.
- Browser, with a local API and Vite on the disposable database: Work showed Daily books with one new finding out of two for a client whose change-hint capture added a payment without a payee; **Mark reviewed** cleared it to "No new exceptions since the last review."

## Cash operations: overdue receivables and payables (2026-09-24)

Author: Claude Opus 5.5. No migration.

- `TestOpenItemExceptionsAskAboutOldInvoicesAndBills`: at 30 September, invoices 75 and 120 days past due and a bill 45 days past due are asked about (the policy pointer only past 90 days); an invoice 30 days past due, a bill 20 days past due, an item without a due date and a zero balance are not. Observed red with each threshold and the 90-day pointer changed.
- `TestPeriodEntitiesRequiresCompletePagedRevisions` now checks that the capture pages open bills and invoices into `open_items`, and that a refused open-bill query still completes the transaction capture with a completeness note. Observed red when they are not stored and when the refusal failed the capture. `Balance` is documented as filterable for invoices; the bill filter is not verified against a live company.
- `TestTransactionChecksReuseEvidenceAndRefreshIndexedRows` now checks that a capture differing only in open items stays unchanged, keeps its open items and records a past-due bill beside the reused transaction findings. Observed red with open items in the evidence digest, dropped from the unchanged payload, or left out of its findings.
- `TestExceptionsScanTheWholeLatestCapture` now checks that the close shows the latest finished capture's recorded findings, including an unchanged one, as Daily books does. Observed red when the endpoint ignores stored findings or unchanged captures.

## Stripe fee entry (2026-09-24)

Author: Claude Opus 5.5. No migration.

- `TestStripeFeeEntryCoversFeesOfPayoutsDepositedThisMonth`: with every payout traced, the fees of payouts deposited in September (4.95 + 21.10 + the 2.00 billing fee row + 1.17 = 29.22) are proposed as debit Stripe fees, credit Sales on 2026-09-30; the 4.46 of fees of a payout in transit and one deposited 2 October are named and left for October; booking 30.00 proposes 0.78 back; booking 29.22 proposes nothing; an untraced month proposes nothing and names the fees of untraced payouts separately; a payout deposited on 31 August is not a September fee; without the entry nothing is proposed. A missing rule, the same account twice, missing or negative booked fees and a blank fee account are refused. Observed red without the fee rows, without deferring after-month-end or in-transit payouts, with the entry never reversed, without blocker suppression, without the rule requirement, without the booked-fees check, when an August deposit counted and when untraced fees were described as deferred.
- `TestYearEndHandoffRollsForwardApprovedWork` now includes an approved Stripe settlement's fee entry in `adjustments.csv`. Observed red with settlements excluded.

## Client currency and fiscal year (2026-09-24)

Author: Claude Opus 5.5. Migration 051. Review: GPT-6 Sol (medium) plan and code review; adopted the full ISO 4217 minor-unit list, the currency check before production posting, and keeping bill corrections labeled as the USD sandbox.

- `TestQuickBooksScopedOAuthRefreshAndSnapshots` now checks that connecting reads Preferences and the client profile becomes CAD with a July fiscal start. Observed red when the month or the stored profile is dropped.
- `TestXeroConnectsOneOrganisationAndCapturesTheTrialBalance` now checks that a snapshot records NZD and a financial year ending in March (start April). Observed red with the end month used as the start and with the profile not stored.
- `TestLearnedRuleRequiresApprovedExampleBeforeActivation` now checks that a USD prepaid is refused for a client whose books are kept in CAD and a CAD one passes. Observed red without the guard.
- `TestYearEndHandoffRollsForwardApprovedWork` now checks that a July fiscal year anchored in May 2026 covers 11 months from 2025-07 and is named FY2026. Observed red without the prior-year rollback and with the calendar-year query bound.
- `TestProductionPostingSingleDurableAttempt` now posts a CAD prepaid: refused while the company's currency is unknown, then posted with a CAD journal once it is detected. Observed red with a USD journal, with the client currency not passed and without the posting check.
- `TestTrialBalanceAccountRequiresUniquePrepaidRow` refuses a Trial Balance in another currency (observed red without the check); `TestCurrencyNeedsThreeLettersAndTwoDecimals` accepts USD, CAD and GBP and refuses JPY, KWD, XOF, IQD and lowercase codes (observed red with the refusal list shortened).

## Deferred revenue and depreciation (2026-09-24)

Author: Claude Opus 5.5. No migration. Review: GPT-6 Sol (medium) plan and code review; adopted the reversed deferred revenue entry, required accumulated depreciation, schedule-only carry, schedule-aware Trial Balance accounts and labels, and draft-only posting. Not adopted: rewording the procedure account hints, because only intake-prepared prepaids read them and the rule-based intake parses those labels.

- `TestDeferredRevenueRecognizesTheEarnedMonth`: 12,000 billed for 12 months with 12,000 booked proposes debit deferred revenue, credit revenue 1,000; with no booked balance, 11,000 is deferred. Observed red without the reversed entry.
- `TestDepreciationTruesUpAccumulatedDepreciation`: a 3,600 asset over 36 months depreciates 100.00 (90.00 with 360 salvage), proposes debit depreciation expense, credit accumulated depreciation, needs no entry when 100.00 is already booked, refuses a missing balance, salvage at cost and credits, and carries without a ledger balance. Observed red without the accumulated conversion, the salvage exclusion, the required balance and the carry exception.
- `TestDeferredRevenueAndDepreciationStayDraftOnly`: a deferred revenue draft is refused before any QuickBooks call. Observed red without the refusal.
- `TestRecurringComparisonRefreshesOnlyCurrentPeriod` (PostgreSQL) now runs for a prepaid and a depreciation schedule: the refresh finds the AccumulatedDepreciation account, shows 80.00 expected accumulated depreciation after January, proposes 10.00 against 70.00 booked and nothing against 80.00. Observed red with the prepaid subtype only and without the accumulated target.
- `TestYearEndHandoffRollsForwardApprovedWork` checks the new `schedule` column in `prepaids.csv`. Observed red when blank. The columns `period_expense` and `closing_balance` are renamed `period_amount` and `scheduled_remaining`, since they hold revenue or depreciation for the new schedules (code review finding).

## Holding and clearing balances (2026-09-24)

Author: Claude Opus 5.5. No migration. Review: GPT-6 Sol (medium) plan review; adopted clearing only on balance-sheet accounts, reconciliation wording that allows deposits in transit, the opposite-side clue, updated labels, the check placed after the existing account filters, and gathering the account's month ledger rows as evidence. Receipt-level missing documents stay out of this slice: the product has never read QuickBooks attachments live, and missing statements are handled by coverage requests.

- `TestHoldingAccountsWithABalanceNeedReconciliation`: from a QuickBooks Trial Balance, Undeposited Funds (1,200.00 debit), Ask My Accountant (35.00) and a Stripe clearing credit (15.00, also flagged as opposite its normal side) are one reconciliation item each; a zero payroll clearing account, a normal checking account and a "Clearing Revenue" income account are not. Observed red without the check, with "clearing" matched on every account, without the opposite-side clue and without the undeposited-funds next step.
- `TestWrongSideReadsYTDColumnsWithXeroRules` now includes a Xero Suspense liability holding 80.00. Observed red without the Xero check.

## Reading production QuickBooks books (2026-09-24)

Author: Claude Opus 5.5. No migration. Review: GPT-6 Sol (medium) plan review; adopted the environment-aware company read in snapshots, the reading connection at admission, capture start and current-result selection, the server-side exact company-name check for production confirmation, the Evidence-tab production path (connect and confirm under Settings) and a lateral scheduler choice that keeps the close lock. Code review: adopted the reading-connection recheck when a snapshot is saved and the latest-capture flag scoped to the reading connection.

- `TestPeriodEntitiesRequiresCompletePagedRevisions` now checks that a production capture reads only quickbooks.api.intuit.com.
- `TestTheProductionConnectionIsReadFirst` (PostgreSQL): with a sandbox and a production connection for one client and only production OAuth configured, the scheduler queues the production connection only, and a sandbox capture already queued is refused when it starts.
- `TestQuickBooksScopedOAuthRefreshAndSnapshots` now checks that a production company whose name differs from the client cannot be confirmed through the API, and that once production is connected the sandbox company can no longer be snapshotted.
- `TestExceptionsScanTheWholeLatestCapture` now checks that a sandbox month-end snapshot and transaction capture are not shown as the client's current or latest books once a production connection exists. Observed red without the snapshot and latest-capture scoping.
- `TestQuickBooksScopedOAuthRefreshAndSnapshots` also connects production while a sandbox snapshot is being read: the snapshot is not saved (code review finding). Observed red without the recheck at save. A client-level advisory lock taken by both the connect insert and the snapshot save orders a production connection started on another close of the same client against the save; the save takes it before any row lock, as the connect insert does, so the two cannot deadlock (final review round); this concurrency guard has no automated test. After every connection is disconnected, the most recent capture and snapshot stay visible and flagged stale, as before.

## Stripe fee entry posting (2026-09-24)

Author: Claude Opus 5.5. Migration 052.

- `TestApprovedLoanAndOwnerEntriesPostUnderTheirGrant` (PostgreSQL) now posts an approved settlement's 29.22 fee entry: refused until `settlement` is in `PRODUCTION_POSTING_KINDS`, refused while a newer draft of the same Stripe report exists, then previewed as Stripe fees 10.00 → 39.22 and Sales 5,000.00 → 5,029.22, approved, posted once and verified. The newer draft cites the same Stripe report file under a different location and is still recognized, because settlements are keyed on the report evidence (code review finding). Observed red without the kind, without the settlement entries and without the report-file match.

## Production Bill corrections and supplier credits (2026-09-24)

Author: Claude Opus 5.5. Migration 053. Review: GPT-6 Sol (medium) plan review; adopted hosted actor and client access checks, the grant limit and a grant recheck under the attempt lock, both sides of a Bill recode in the grant, grant membership instead of the sandbox prepaid mapping, the reading connection for captures, and the connection's recorded currency. Journal posting now shares the grant and credential lookup (`productionClient`) with unchanged behavior.

- `TestProductionBillCorrectionStaysInsideItsGrant` (PostgreSQL, mocked production host): refused until `bill_correction` is enabled; refused at preview when the line's current account is outside the grant and when the line exceeds the grant limit; an approval under a different grant than the preview is refused; under a grant for both accounts it previews, approves, updates the Bill once and verifies. Observed red without the kind gate, the preview account check, the limit and the grant binding.
- `TestBillCorrectionPreservesSimpleBillAndChecksReadback` and `TestVendorCreditChecksAccountsAndCanonicalReadback` now check that production writes stay within granted accounts (both Bill accounts; A/P and prepaid for a credit) and that the granted prepaid account is found by name. Observed red with either account unchecked.
- Not covered by an end-to-end test: the production supplier-credit route (its account, currency and grant rules are covered at the QuickBooks layer), its sign-off recheck under the close lock (code review finding; the sandbox route lacked it too), and the grant recheck under the attempt lock (a race).

- `TestApprovedLoanAndOwnerEntriesPostUnderTheirGrant` (PostgreSQL) now posts an approved settlement's 29.22 fee entry: refused until `settlement` is in `PRODUCTION_POSTING_KINDS`, refused while a newer draft of the same Stripe report exists, then previewed as Stripe fees 10.00 → 39.22 and Sales 5,000.00 → 5,029.22, approved, posted once and verified. The newer draft cites the same Stripe report file under a different location and is still recognized, because settlements are keyed on the report evidence (code review finding). Observed red without the kind, without the settlement entries and without the report-file match.

## Sign-off carries the month forward (2026-09-24)

Author: Claude Opus 5.5. No migration. Plan and code reviewed together (small change); adopted a distinct carry failure in the sign-off reply, "queued" wording and the standard library's response recorder.

- `TestCarryForwardRoundingLineageAndFreshReview` now signs off the origin close after approving its schedule: the sign-off reports one schedule carried and the preview shows its next-month job; the later manual carry reuses that job. Observed red without the carry on sign-off.

## Confirming a client alias (2026-09-24)

Author: Claude Opus 5.5. Migration 054. Review: GPT-6 Sol (medium) plan review; adopted the attempt reset shared with retry, offering and accepting an alias only when the source names exactly one other entity, one shared normalizer, and no surrogate id. Code review: adopted never offering or storing an empty normalized name, and failing evidence inspection when aliases cannot be loaded.

- `TestConfirmedAliasesAcceptOnlyAnUnambiguousSource`: an unconfirmed other name blocks and is offered; once confirmed it is the same client; a source naming two entities stays blocked and offers nothing. Observed red when any differing name is offered.
- `TestConfirmingAClientAliasRechecksBlockedWork` (PostgreSQL): a document naming "<client> LLC" is blocked with that name; a different name and a stale revision are refused; confirming it requeues the job, which is then prepared. Observed red without the offered-name check, without alias acceptance and with aliases not loaded by the local intake.

## Plain-language Work requests (2026-09-24)

Author: Claude Opus 5.5. No migration. Review: GPT-6 Sol (medium) plan review; adopted the route allowlist entry, passing the returned command straight to the parser once, validating action/target pairs, and an inline model call instead of a new shared helper. Code review: adopted a single model fallback when a matched command names no exact visible close, resolving rehearsal from the visible work first, and removing restating comments. Two delta rounds tightened the busy state; after the second, the ask box is read-only while a request runs so a late answer cannot overwrite a newer request (this last change was not re-reviewed; the frontend has no test runner).

- `TestPlainLanguageWorkRequestsBecomePreviewedCommands`: action/target pairs map to the Work box's own commands and mismatched pairs are refused; with a mocked model, a plain request becomes "why is Acme · 2026-09 waiting?", only the request and visible close labels are sent with `store:false`, a mismatched or unknown answer is refused (422), and a missing model is 503. Observed red when views accept a close, when explain accepts all visible, and when the request is stored.
- `TestWorkInboxCoordinator` now reaches the route through the organization boundary (503 without a model). Observed red without the allowlist entry.

## Unapplied payments and unused credits (2026-09-24)

Author: Claude Opus 5.5. No migration. Review: GPT-6 Sol (medium) plan review; adopted `RemainingCredit` for credit memos, reading a year by date and selecting positive unused amounts locally instead of undocumented filters, showing the completeness note, and asking what a payment settles.

- `TestUnappliedPaymentsAndOldCreditsAreAskedAbout`: a 500.00 unapplied payment two days old and a credit memo with 120.00 unused for 45 days are asked about; a supplier credit 10 days old is not. Observed red without the payment question and without the 30-day wait for credits.
- `TestPeriodEntitiesRequiresCompletePagedRevisions` now checks that open payments are read by date for the year to month-end, never filtered on UnappliedAmt, and that only the payment with money unapplied is kept. Observed red with zero amounts kept and without payments read. A payment without a readable unapplied amount is named in the completeness note instead of being treated as applied (code review finding; observed red without the check), and unchanged captures now show that note too.

## Balance sheet tie-out (2026-09-24)

Author: Claude Opus 5.5. No migration. Review: GPT-6 Sol (medium) plan review; adopted marking matches against unapproved or blocked support provisional, treating entries posted before the capture as already in the balance and unknown postings as their own state, capping the loan's current portion as the calculator does, listing unclassified Trial Balance accounts, and computing the summary from the rows. Code review: adopted checking an unknown posting before calling a match a tie, listing support that names a non-balance-sheet account or another currency instead of counting it; rejected showing zero-balance accounts with no support (nothing to tie, and noise on every board).

- `TestTieOutComparesBalancesWithLatestSupport` (PostgreSQL): from a QuickBooks Trial Balance, prepaid insurance (1,000.00 from the latest versions of two policies), accumulated depreciation (600.00 credit from cost 3,000.00 less salvage 600.00 less 1,800.00 remaining), deferred revenue, checking, a card reconciled with a missing page (provisional), accrued payroll whose entry was verified before the capture, and a loan whose 2,000.00 due within a year is capped at its 1,500.00 principal all tie; accrued liabilities (400.00 booked, 500.00 target, 100.00 proposed) tie after entries; an accrual whose proposal is stale differs; an accrual whose posting outcome is unknown waits on it even though its balance already matches; the loan payable and office equipment have no support, with a hint; equity and expense accounts are left off; a prepaid naming "Prepaid rent", one naming the Utilities expense account and one prepared in EUR are listed, not compared, and an account missing from the directory is unclassified. A newer Xero Trial Balance ties the same way. Observed red without latest-version selection, without the posted-before-capture boundary, the unknown-posting state, provisional marking, the deferred revenue sign, salvage, the loan cap, the card sign, unclassified accounts, the balance-sheet filter, the exact after-entries match, Xero class mapping, QuickBooks rows missing from the directory, the currency check, the balance-sheet check on support, and with a tie checked before an unknown posting.
- Existing abnormal- and holding-balance tests pass unchanged on the shared Trial Balance reader.

## Where this close stands (2026-09-24)

Author: Claude Opus 5.5. No migration. Review: GPT-6 Sol (medium) plan review. It found that the proposed "run the close" orchestration was unsafe: carry-forward runs from a close into its next month; repeating a month-end snapshot replaces ledger evidence that reviews and approvals depend on; a 90-second provider read cannot extend the outer request's deadline through internal dispatch; and agent OAuth lacks the integration permission. So the orchestration was dropped and this read-only brief shipped. Also adopted from the plan review: sources that cannot be read are marked not checked, the tie-out narration covers every status, and review counts are labelled as current drafts. Code review adopted:

- An unreadable Trial Balance is not reported as read.
- "Ready to sign off" and entries still to post are actions for the person.
- Drafts with no evidence are counted and named.
- A failed balance check keeps the exception scan from reading as done.
- The card reloads with the close and has a Refresh button with an "as of" time, because some Work actions do not reload the page (second delta round).
- Unclassified ledger accounts get their own action.

- `TestCloseBriefNarratesWhatNeedsAPerson` (PostgreSQL):
  - An empty close asks for a snapshot and documents; once its scope is set and nothing blocks sign-off, it asks for sign-off.
  - A close with a snapshot, a document needing confirmation, two ready drafts (one the latest of two versions), a draft with no evidence, a blocked reconciliation, a differing prepaid balance, an accrual that ties once its entry posts, an unsupported loan and an abnormal checking balance reports each area's state. Its headline reads "8 things need you: confirm 1 document, approve 2 drafts, add evidence to 1 draft, fix 1 draft, explain 1 balance difference, support 1 balance, post the proposed entries for 1 balance, send 1 client question."
  - A snapshot from a connection needing reconnection is out of date.
  - An unreadable workpaper makes the ledger and balance sheet "not checked", named in the headline. So does an unreadable Trial Balance, which also marks the exception scan not checked.
  - Observed red without latest-version drafts, without the null-safe blocker check, without the unreadable-source state, without naming unchecked areas in the headline, without the approve need, when provisional counts include differing rows, without balance questions, without the confirmation state, without the stale check, without the sign-off, no-evidence and post-entries actions, without the unreadable Trial Balance state, and without the failed balance check.
- `TestMCPToolsUseTheSignedInPersonsAccess` now lists `close_brief` and `tie_out`.

## The first month-end snapshot captures itself (2026-09-24)

Author: Claude Opus 5.5. No migration. Review: GPT-6 Sol (medium) plan review. Adopted:

- re-checking the period, the ended month, sign-off, "no period snapshot yet" and the snapshot limit under the close lock;
- a separate three-minute budget after the transaction capture, with a failed read left for the next scheduled capture;
- binding organization, client, company and reading connection in one join, repeated under the lock;
- tests for a person's capture that wins the race and for another firm's close.

The period-report assembly was extracted once (`quickbooks.ReadSnapshot`) and the manual handler uses it unchanged; `TestQuickBooksScopedOAuthRefreshAndSnapshots` still passes.

- `TestTheFirstEndedMonthSnapshotIsCapturedOnce` (PostgreSQL, provider mocked):
  - Last month's close gets exactly one period snapshot with reports and coverage. Its evidence is recorded as `scheduled-quickbooks-refresh`.
  - A second attempt makes no provider read.
  - The current month, a signed-off month and another firm's close are not read.
  - A changed company name saves nothing.
  - A person's snapshot saved during the read wins.
  - Observed red without the signed-off check, the "no period snapshot" check, the ended-month check, the locked re-check, the company check and the evidence link.
- Not covered by a test: the five-line call from `ProcessSync` after a completed transaction capture. It runs `captureFirstPeriod` with the sync's own refreshed credentials.

## Schedules discovered from the ledger (2026-09-24)

Author: Claude Opus 5.5. No migration. Review: GPT-6 Sol (medium) plan review. Adopted:

- netting additions per transaction, so transfers and amortization are not new additions;
- coverage only when a schedule cites the transaction's document number, with the same amount, currency and account, instead of amount alone;
- an incomplete or waiting scan when the account list is missing or lacks an account, never a clear one;
- account identity in the Exceptions row key.

Mutation checks also removed a redundant account-list flag. Code review adopted:

- one item per account, so a split bill is covered account by account;
- current schedule versions only;
- hyphens as part of a document number;
- item-based lines making the scan incomplete, counted even without an amount (delta round);
- coverage requiring a captured currency on both sides (delta round).

Code review rejected an account check for fixed assets: depreciation workpapers record the accumulated account, not the cost account, and the document number already ties them to the purchase.

- `TestScheduleCandidatesAreNetAdditions`:
  - Found: a Bill to Prepaid insurance with document number INV-88, a Purchase of office equipment, a journal entry and a Deposit crediting deferred revenue.
  - Not found: a transfer between prepaid accounts, amortization, a credit to accumulated depreciation, a debit to accumulated depreciation, revenue recognition, or a card refund.
  - A split bill gives one item per prepaid account, and a partial transfer names only the account that grew. An item-based line is counted.
  - An account missing from the directory is named.
  - Observed red without the prepaid subtype, the contra exclusion, the Deposit and credit-card signs, the netting and deferred-revenue direction, the unknown-account list, the per-account amount guard and the item-line count.
- `TestLedgerAdditionsAskForASchedule` (PostgreSQL):
  - Without a snapshot nothing is concluded, and the brief says schedule discovery is waiting.
  - These schedules do not cover anything: one citing INV-880, one in EUR, one on another account, and a depreciation schedule with no document number to match.
  - A schedule citing INV-88 on "Prepaid Expenses:Prepaid insurance" covers only that bill; a second 12,000.00 bill (INV-90) still asks.
  - A revised schedule that no longer cites INV-90 stops covering it.
  - A bill with no captured currency is not covered by a schedule without one.
  - An item line alone, then an unknown account, make the scan incomplete, and the brief names both.
  - Observed red without the document number, whole-word, hyphen, currency, account and full-name checks, without the current-version filter, without the incomplete state for item lines, and without the brief's waiting and not-checked lines.

## Agent track record by client (2026-09-24)

Author: Claude Opus 5.5. No migration. Review: GPT-6 Sol (medium).

The plan review rejected the first definition ("agent accuracy" over drafts with no author). Every worker-written draft has no author, including drafts a person directed, and a person's corrections come back through the worker. So the metric was redefined and labelled a track record:

- model proposals compared with what a person confirmed;
- drafts judged by recorded review decisions, not the current state, so replaced evidence does not rewrite history;
- a revision counts only when a successor draft exists.

The delta plan review added counting only model proposals (not rule-based ones), requiring an actual successor draft, and selecting the first 201 accessible clients before aggregating. Code review adopted: amounts compared by value only when they look like amounts (identifiers stay text); sent back and revised counted independently; a window of exactly 12 close months; an accurate empty message when not all clients are shown.

- `TestTrackRecordComparesProposalsWithWhatPeopleConfirmed` (PostgreSQL). The fixture:
  - a proposal confirmed with only case, spacing and 1,200 → 1200.00 differences, whose draft was approved, then its evidence replaced, with a revision queued that produced nothing;
  - a proposal whose prepaid account and booking reference (0012 → 12) were changed, whose draft was sent back and then revised;
  - a proposal with a blank end date the person filled, whose draft was sent back;
  - a proposal whose kind was changed;
  - excluded: an unconfirmed proposal, a rule-based one, one from a close two years old, one from exactly 12 months ago, a carried-forward job, and another firm's client.

  Expected results:
  - 4 confirmed, 2 kept, and changed fields "booking reference", "kind" and "prepaid account";
  - 3 drafts judged: 1 approved, 1 revised, 2 sent back;
  - 1 draft saved directly;
  - a member without the client grant sees nothing.

  Observed red without the model-only filter, without the confirmed filter, without the number, comma, leading-zero, case and blank-value rules, with a 13-month window, with sent back excluding revised drafts, without actual-successor revisions, when using the current review state, without the client grant, without the window, without the kind check, and without the direct-save filter.
- `TestMCPToolsUseTheSignedInPersonsAccess` now lists `agent_track_record`.

## Replaying confirmed readings with the current agent (2026-09-24)

Author: Claude Opus 5.5. No migration. Review: GPT-6 Sol (medium) plan review. Adopted:

- replaying on the evidence the agent originally selected (its checkpoint), not only the sources it cited;
- the worker's client-identity check before any model call;
- counting a value the new reading leaves out as a difference;
- re-checking the evidence after the model call.

The track record's comparison was extracted and reused; `TestTrackRecordComparesProposalsWithWhatPeopleConfirmed` still passes. Code review adopted:

- expected values taken from what the person confirmed, so a value the model misses again is a difference;
- one replay per person at a time;
- the card keyed to its close;
- a mutex in the test's concurrent mock. The race detector is clean.

A firm-wide limit on model-spending actions was routed to a separate ticket, because the Work box and preparation retries have the same gap.

- `TestReplayComparesTheCurrentModelWithConfirmedReadings` (PostgreSQL, OpenAI mocked):
  - Without the model configured, it returns 503.
  - The three most recent model readings are replayed: one is the same as confirmed; one differs (another prepaid account, the amount left out, and an end date the person supplied that the model misses again); one is not replayed because evidence changed since confirmation. That run makes 2 model calls.
  - A rule-based reading is never picked.
  - A second replay by the same person while one runs is refused.
  - A reading naming another client is not replayed, and only the newest three are used.
  - Nothing is written: workpapers, jobs, evidence and stored proposals are unchanged.
  - Evidence added during the model call leaves the reading unscored.
  - Observed red without the model-only filter, the three-reading limit, the evidence check before and after the call, the identity check, the left-out-value rule (including values the person supplied), the one-at-a-time guard, and the configuration check.

## Client memory when confirming (2026-09-24)

Author: Claude Opus 5.5. No migration. Review: GPT-6 Sol (medium) plan review. Adopted:

- a stale snapshot says nothing, and "not in this QuickBooks snapshot" wording;
- **Use** only for a single active account that agrees with the approved procedure;
- excluding revised papers before latest-per-version selection;
- a window of the 12 close months ending with the close;
- the reading connection only, with no fallback.

Code review adopted: showing the other remembered choices unless the proposal matches the most common one, and re-reading notes on Work's refresh, clearing old ones first.

- `TestHintsRememberApprovedAccountsAndCheckTheLedger` (PostgreSQL):
  - Remembered: Prepaid insurance ×2, Prepaid software ×1 and Prepaid legacy ×1.
  - Ignored: a revised older version under a different source name, a changes-requested paper, papers 12 months back and one month ahead, and another client's paper.
  - Without a snapshot there is no ledger check.
  - A job from another close is a 404.
  - With a snapshot, "Prepaid Expenses" is not in it, while "insurance expense" matches regardless of case. Only Prepaid insurance can be used: Prepaid software is ambiguous and Prepaid legacy is inactive.
  - An approved procedure naming Prepaid software is shown and blocks the conflicting remembered account.
  - A connection needing reconnection makes the snapshot stale, and the ledger check goes silent.
  - Observed red without the revision filter, the approved-decision filter, either window bound, the client filter, the stale check, the active check, the procedure conflict, the single-match rule, and procedure loading.

## Standing approval of carried prepaid schedules (2026-09-24)

Author: Claude Opus 5.5. Migration 055 (`standing_approvals`). Review: GPT-6 Sol (medium) plan review. Adopted:

- never approving a revision automatically, including a monthly comparison with no adjustment;
- binding each approval to the grant's own approval and re-checking the grant after locking;
- stating that a grant rests on the granter's authority at grant time and stops when they leave the firm;
- a sweep cursor so refused drafts cannot starve later ones.

The test also caught a revoke that did not bind the workpaper to the close in the URL; every action now does. Code review adopted:

- locking the granting schedule's close, the grant row and (second delta round) the firm's row before the final check, against a racing revoke, re-review or suspension (by inspection; no concurrent test);
- no approvals for a disabled firm;
- showing whether a grant is in effect.

- `TestStandingApprovalApprovesUnchangedCarriedMonths` (PostgreSQL):
  - A grant is refused on a draft that was never approved and on one sent back, and a second active grant is refused.
  - One sweep approves the unchanged October carry under the granter, with a note naming the standing approval. A revision, a draft with a blocker, and a draft proposing an entry are left alone, and a second sweep approves nothing.
  - A re-approval of the granting schedule stops even a fresh carry. November's carry of the approved October draft is covered too.
  - Revoke through another close is a 404; after revoking, nothing more is approved.
  - A signed-off close and another client's carry are skipped.
  - A granter who is not a member is skipped; firm independence refuses a granter who prepared the carry; a disabled firm is skipped; once allowed and enabled, it is approved.
  - The page reports a grant resting on a superseded approval as not in effect.
  - Observed red without the revision check, the entry check, the signed-off check, the grant-approval binding, the membership check, the local-development exception, the client check, the approved-state check on grant, the close binding, the enabled-firm check, and the in-effect status.

## Changes from last month (2026-09-25)

Author: Claude Opus 5.5. No migration. Review: GPT-6 Sol (medium) plan review. Adopted:

- labelling figures as captured, with both capture times;
- refusing to compare different companies or currencies, and waiting when the newest books are from Xero;
- summing only ledger transaction rows, keeping unnamed amounts apart, and never claiming a missing payee while this month has unnamed amounts;
- "possible drivers" when payees do not add up;
- strict P&L rows (duplicates and column mismatches refused);
- neutral wording unless the account type is known;
- one ledger scan per month.

The General Ledger traversal was extracted once (`walkLedger`); ledger page selection is unchanged and its tests pass. Code review adopted:

- waiting when last month's newest books are from Xero;
- requiring a single account-and-total P&L, with a second column titled Total or typed Money (second delta round);
- turning an account's ledger sign to match the P&L when its total is exactly opposite;
- suppressing missing-payee claims when any unnamed row exists;
- counting payees not shown.

- `TestMonthlyActivityAndPayeesReadReportSections`:
  - Reads accounts inside nested P&L sections, but neither summaries nor a parent-account section row.
  - Refuses an account listed twice, a row that does not match the columns, and a P&L with a second amount column.
  - Sums ledger rows by payee: unnamed amounts are kept apart, and a named row with no amount, a subtotal section and other accounts are ignored.
  - A ledger without a Name column cannot attribute payees.
- `TestFluxExplainsMovementsByPayee` (PostgreSQL):
  - Without last month's close it waits.
  - Utilities 800 → 2,000 is explained by the new Con Edison 1,200, and Software 49 → 0 asks whether CloudSuite billed; only moved accounts are listed, largest first.
  - Consulting income signed -900 in the ledger reads as Acme 900.
  - Unnamed rows this month, even netting to zero, suppress the missing-payee claim.
  - Payees that do not add up are possible drivers, with two more payees counted but not shown.
  - A stale snapshot is reported.
  - Different currencies, different companies, and newer Xero books in either month all wait.
  - Observed red without the data-row checks, the duplicate and column checks, the blank-amount and unnamed-amount rules, the Name-column check, the company, currency and Xero checks, the missing-payee suppression, the add-up check, the ordering, the zero-change filter, the single-amount-column rule, the sign match, and the omitted count.

## Good morning brief (2026-09-25)

Author: Claude Opus 5.5. No migration. Review: GPT-6 Sol (medium) plan review. Adopted:

- Daily Books questions reported as new since last review, under "Today", from the complete list (Daily Books' own list is capped at 100);
- counting new drafts, not revisions a person queued;
- the missing-snapshot check honoring QuickBooks period snapshots and Xero;
- automatic approvals matched to the exact note, grant and granter;
- scope applied to every count before ranking;
- "In the past 24 hours" wording, with no claim that nothing needs the person.

Daily Books' computation was extracted once (`dailyFindings`); its tests pass. Code review adopted: marking Daily Books reviewed refreshes the brief, a failed reload shows its error instead of stale figures, and one scan per close counts ready and attention drafts.

- `TestMorningBriefSaysWhatTheAgentDidAndWhoNeedsAPerson` (PostgreSQL):
  - Counted in the past day: one scheduled month-end snapshot, one new agent draft and one automatic approval.
  - Not counted: a two-day-old scheduled snapshot, a person's snapshot, a person-queued revision, hand-saved drafts, a human note starting "Standing approval #", and the exact note written by another reviewer.
  - Harbor (approve 1 draft, no month-end snapshot yet) ranks above Dental (1 new client question since last review).
  - The current-month close and a Xero-snapshotted close are counted as quiet; another firm's close is invisible.
  - A member without client grants sees nothing.
  - Observed red without the 24-hour window, the schedule actor, the revision and hand-saved filters, the exact note, the granter match, the ended-month and Xero rules, the ready-draft evidence rule, the Daily Books merge, and the ranking.

## Agent-prepared month-end comparisons (2026-09-25)

Author: Claude Opus 5.5. No migration. This follows the user's rule: agents build everything, and a person approves before any sync with a third-party system. An audit found every outside write already behind a person's approval: QuickBooks journals, Bills and supplier credits need `posting:approve` then `posting:execute`, and every client email and reminder is approved individually. The rule is now written in production-posting.md.

Review: GPT-6 Sol (medium) plan review. Adopted:

- reading the balance only from exactly one Debit and one Credit column, refusing a wrong-side value;
- skipping any earlier comparison of the schedule against this capture, so a person's failed one is never silently replaced;
- a ledger reference and panel copy saying the balance was read by the agent and awaits a person's confirmation.

Code review adopted:

- queueing each schedule inside its own savepoint, so a refusal or the firm's job limit cannot undo the rest of the close;
- skipping disabled firms with the firm's row locked (second delta round);
- reporting an unexpected database failure after committing earlier schedules, instead of skipping it (second delta round);
- refusing a balance that is not a whole number of cents.

Mutation checks deleted five redundant checks. The test restores the firm's job limit and enabled flag even when it fails, after an interrupted mutation run left the limit changed. The manual comparison path was extracted into `queueComparison` with unchanged behavior; `TestRecurringComparisonRefreshesOnlyCurrentPeriod` passes.

- `TestBookedBalanceReadsTheNormalSideOnly`:
  - Read: a prepaid debit, a credit for accumulated depreciation and for deferred revenue.
  - Refused: a prepaid on the credit side, two Debit columns, cells not matching the columns (short or with an extra cell), no Credit column, an unreadable amount, and a fraction of a cent.
- `TestCarriedSchedulesArePreparedFromTheTrialBalance` (PostgreSQL):
  - Nothing is prepared for a credit-side prepaid, an ambiguous account, a signed-off close (also when signed off after it was picked), an inactive snapshot, or a schedule with a person's failed comparison against the capture.
  - At the firm's job limit nothing is queued and the sweep still completes. A disabled firm is skipped.
  - Then one comparison is prepared by `scheduled-recurring-comparison` at 50.00 with an agent-read reference, and it is not queued twice.
  - Its draft proposes the 10.00 entry and waits for a person's approval, and the revision is not revised again from the same capture.
  - Observed red without the column checks, the orientation, the already-cited and earlier-comparison skips, the sign-off and enabled-firm rechecks, the savepoint rollback, the whole-cent rule and the system actor. The missing-Credit-column guard fails only by panicking without it.


## Finishing the half-built items (2026-09-25)

Author: Claude Opus 5.5. Migration 056. These five items were half-built or waiting on the user's decision. All follow the rule that agents prepare everything and a person approves before any write to an outside system. Nothing here writes to QuickBooks, Xero or email on its own.

- **One approval per entry.** Approving a production or sandbox posting of a pending draft also approves the draft, in the same transaction, for a person holding `workpapers:review`. The usual review checks still apply.
- **Standing approvals for recurring accruals and loans.** An accrual grant covers its recurring lineage. A loan grant covers later months whose terms all match, with no open question.
- **Replay.** Each replay is saved. The worker checks each firm once for each new model, and the morning brief shows the result.
- **Model call limit.** A firm-wide daily cap, reserved atomically before every model call.
- **Xero.** A daily scheduled month-end capture. Changes from last month now work on two Xero months (no payees), client memory checks Xero accounts, and the morning brief counts Xero captures.

Plan review: GPT-6 Sol (medium). Adopted:

- the agent works out each job's real need (0, 1 or 2 calls) and parks a refused job with a retry time, instead of a claim filter that would strand jobs;
- a loan grant covers only drafts whose terms all match, not five fields;
- a scheduled replay counts only once it scores a reading;
- a per-close claim for scheduled Xero captures, which also limits retries after an error;
- the permission check before approving a draft through its posting.

Not adopted: a cross-worker lock for scheduled replays. The worst case is one duplicate run of up to 3 calls per model change, and those calls count against the limit.

Code review: GPT-6 Sol (medium), two delta rounds. Adopted:

- the morning brief's model check shows only a close the person can open;
- the scheduled Xero claim rechecks the firm, the open close and the absence of a recent capture in the same statement, through one shared condition;
- a standing grant is refused on a paper with open questions;
- a replay whose confirmation is not one readable plan (empty, malformed, `[null]` or `[{}]`) is unscored before any model call, so it cannot count as the firm's check.

The claim's race with a person's capture or sign-off has no deterministic test. The disabled-firm, question, access and plan checks each have one, observed red without the fix.

Minimalism review removed an unused return value and an unused scan target. It left the duplicated connection-change check in the Xero handler alone, to keep unrelated handler actions out of the diff.

Tests (PostgreSQL):

- `TestProductionPostingSingleDurableAttempt`: without `workpapers:review` a pending draft is refused. With it, one approval records "Approved together with its posting." and the draft becomes approved.
- `TestStandingApprovalCoversRecurringAccrualsAndSameTermLoans`: approved automatically are a recurring accrual that proposes its entry, and a later loan month on the same terms. Not approved: an accrual outside the lineage, a loan draft from the granting month, a changed rate, and an open question. The existing prepaid test passes unchanged.
- `TestReplayComparesTheCurrentModelWithConfirmedReadings`: the latest run is saved and readable. A model already checked is not replayed again. A run that scores nothing does not count. A new model is checked once. At the limit, the manual route answers 429 and the worker makes no call and records nothing.
- `TestAgentAnswersResumeAndInventoryFence`: at the limit the job waits with the reason, no attempt and no call, and is not reclaimed before its retry time. Afterwards, a small inventory costs exactly one counted call.
- `TestPlainLanguageWorkRequestsBecomePreviewedCommands` now uses the test database, because the route counts its call. It answers 429 at the limit.
- `TestXeroConnectsOneOrganisationAndCapturesTheTrialBalance`: a close captured by hand in the past day is skipped. An older one is captured once as `scheduled-xero-refresh`, and a close already attempted today is not attempted again. The mock now dates its report from the request.
- `TestFluxExplainsMovementsByPayee`: a QuickBooks month is not compared with a Xero month. Two Xero months show the revenue movement only, without payees, and exclude balance-sheet accounts.
- `TestHintsRememberApprovedAccountsAndCheckTheLedger`: a newer Xero capture supplies the chart of accounts.
- `TestMorningBriefSaysWhatTheAgentDidAndWhoNeedsAPerson`: scheduled Xero captures are counted, and the latest automatic model check shows kept out of scored.

Each test was observed red with its rule removed (25 mutations): the review permission and single-approval path, the loan terms, the later-period, questions and prepaid-only proposal rules, the recurring lineage and loan candidates, the reservation, retry time, call count and limit condition, the scored-run and once-per-model rules, the Work route's reservation, the fresh-capture and claim conditions, the ledger-source match, the revenue sign and P&L filter, the Xero payee note, the newest-snapshot order in client memory, and the morning Xero count and kept count.

Not verified: a live Xero organisation (Xero's Debit and Credit columns being the month's movement comes from third-party documentation, not a live report), a live model, and the browser. Sandbox single approval uses the same function as production and is not tested separately. Firm operations' usage line is not tested.

## Sensitive values removed from client replies (2026-09-25)

Author: Claude Opus 5.5. No migration. PRD COM-01 says a thread containing bank credentials must be redacted and restricted, not forwarded to every reviewer. Every free-text reply is now redacted before it is stored: link replies, email replies (while still held) and replies or answers recorded by staff. Removed are card numbers (Luhn), IBANs (mod-97), SSNs, labeled bank account numbers, and everything after a password or sign-in label on its line. Markers keep at most the last four digits. The original is never stored, and receipts and email duplicate checks use the redacted text. The client is told what was removed, also on a retry.

Plan review: GPT-6 Sol (high). Adopted:

- no unkeyed digest of the raw text (link receipt, email message digest);
- broader credential wording, removing the rest of the line;
- the client notice on retries, and replacing the "original reply is saved" copy;
- recording that COM-01 supersedes COM-02's original-storage clause for removed values.

Code review: GPT-6 Sol (high), two delta rounds. Adopted:

- the staff-entered "received from" is redacted too;
- email duplicates are keyed on sender, Message-ID, Date and redacted text, so separately sent identical emails stay separate;
- IBANs in any case, dropping words that follow the number;
- staff copy that names exactly what is removed.

Refuted with code: held replies are already restricted. Close routes admit only the client's grantees or firm-wide access (`organizations.go:60`), nothing is forwarded, and the secret itself is removed before storage. Not changed: after a page reload the client sees only "Your reply is saved"; the removal notice appears at submit and on a retry.

Tests (PostgreSQL):

- `TestRedactSensitiveRemovesCardsBankNumbersAndSecrets`: each rule, the Luhn and mod-97 negatives, and ledger account codes and routing numbers left alone. Redacted text is stable when redacted again.
- `TestScopedClientReplyResumesJob`: a link reply with a card number and a password stores only the redacted evidence. The client is told both kinds, including on the retry. The link receipt is not a digest of the unredacted reply.
- `TestInboundReplyHeldUntilSenderAndReviewerMatch`: a held email reply stores only the redacted account number, once for a repeated delivery. No digest of the raw message is kept, and the released evidence is redacted.
- `TestWorkInboxCoordinator`: a reply recorded by staff loses its password line, and its "received from" loses a PIN.
- The email test also holds separately sent identical emails apart, whether they differ by Message-ID or only by Date.

Each check was observed red with its rule removed (12 mutations): the shared writer and sender redaction, link and email redaction, the email digest and its Message-ID and Date parts, Luhn, mod-97, IBAN trailing-word trimming, the rest-of-line credential rule, and the bank-label requirement.

Not verified: live Cloudflare email routing and the browser page. Detection is deterministic and bounded. A card-like 13–19 digit number that passes Luhn (such as some long invoice numbers) is also masked, keeping its last four digits.

## Client link passcodes (2026-09-25)

Author: Claude Opus 5.5. Migration 057. PRD §8 requires client links to be "subject to authentication appropriate to the data". A firm can now require that every new request link also needs an 8-digit passcode. A person gives it to the client another way; the app sends nothing, keeping to the rule that a person approves every outside send.

- **Issuing:** all four link paths (manual link, both consolidated-request paths, approved email) go through `issueLink`. It locks the firm row while reading the setting, returns the passcode once, and stores only a digest salted by the raw token.
- **Checking:** `linkPasscode` runs before anything is read, on the client page and both scan routes. It answers `PASSCODE_REQUIRED`, counts each wrong try atomically on its own, and the fifth wrong try revokes the link. The browser preflight allows the header.
- **Policy:** the endpoint changes only the setting named, records both resulting values, and allows passcode changes in local development.

Plan review: GPT-6 Sol (high). Adopted:

- showing the passcode from the email approval (lifted to the parent, because the form remounts after queueing), with reminders reusing the original link;
- preserving the unchanged policy setting;
- the preflight header;
- locking the firm row while issuing;
- checking before any read, and failure counts committed on their own.

Code review: GPT-6 Sol (high), two delta rounds. Adopted:

- a retried first email approval whose link has a passcode now refuses with the recovery step (create a new private link) instead of reporting success without a passcode;
- Work clears the replaced manual link when the email is approved;
- reminders are left alone: they keep their idempotent retry and the shown passcode.

Tests (PostgreSQL):

- `TestApprovedEmailDurableRetryAndScope`: with passcodes required, the email approval returns a passcode. The sealed email says one will be given separately and does not contain it, and a retried approval refuses.
- `TestLinkPasscodeGuardsTheRequest`:
  - Without the setting a link opens on possession. The policy change records both settings, leaving independent review unchanged, and an earlier link keeps working.
  - A new link returns an 8-digit passcode, and its stored digest is neither the passcode nor its plain digest.
  - Without the passcode the client gets `PASSCODE_REQUIRED` and no request text. Four wrong tries count down; the fifth revokes the link, even for the right passcode.
  - A new link opens and accepts a reply with its passcode, and the scan route asks for the passcode.
- `TestPrepareCapabilityAndPreflight`: the client page's preflight may send the passcode header.

Each check was observed red with its rule removed (9 mutations): the page and scan checks, the fifth-try revocation, issuing, the salt, preserving the other setting, the preflight header, the retry refusal and the email sentence. The existing request, email, coverage and Work tests pass unchanged.

Not verified: browser flows, hosted CORS through Cloudflare, a race between the policy change and issuing (the row lock covers it, but no test exercises it), and a retried reminder on a passcode link.

## Independent review for large drafts (2026-09-25)

Author: Claude Opus 5.5. Migration 058. PRD REV-02: "Role-based separation can require a different reviewer for specified risk levels." Firms had one on/off rule. They can now instead set a minimum: a draft whose proposed entries total at least that amount needs a reviewer who did not prepare or confirm it.

- **One check:** the rule lives in the existing `independenceIssue`, so individual, bulk, standing, posting-together and production posting approvals all apply it.
- **Locked:** the firm's row is locked during the check.
- **What counts:** the draft's `proposal` amount plus its `proposals` amounts. An accrual's reversal is never posted, so it is not counted.
- **Changing the minimum:** it can be set or cleared, is recorded with the other settings, and is refused for the local workspace by a database constraint.

Plan review: GPT-6 Sol (high). Adopted:

- the parser's real cap;
- locking the firm row during the check;
- a hosted-only database constraint;
- an explicit clear branch, so an empty value clears the minimum instead of keeping it.

Code review: GPT-6 Sol (high), one delta round. Adopted: setting a minimum from Firm settings also turns off the firm-wide rule in the same change; otherwise every draft would still need an independent reviewer.

Tests (PostgreSQL), `TestIndependentReviewPolicyRefusesPreparersAndUnknownProvenance`:

- Negative and zero minimums are refused, and so is a minimum for the local workspace.
- A minimum of 1000 is recorded with the other settings.
- With the firm-wide rule off, the preparer may approve 999.99 but not 1000.00, nor a loan whose two entries total 1000.
- Clearing the minimum lifts the rule, and the database refuses a minimum for the local workspace.

Each check was observed red with its rule removed (5 mutations): the minimum rule, counting a loan's entries, the at-or-above comparison, the clear branch, and the zero check. The existing firm-wide tests pass unchanged.

Not verified: the firm settings page in a browser, and a race between a policy change and an approval (the row lock covers it, but no test exercises it).

## API error codes and idempotency keys (2026-09-25)

Author: Claude Opus 5.5. Migration 059. PRD Appendix C asks the API to "accept idempotency keys, and return explicit errors such as SOURCE_STALE, POLICY_REQUIRED, SCOPE_DENIED, APPROVAL_EXPIRED, and EXECUTION_UNKNOWN". The API already returned `SOURCE_STALE`, `EXECUTION_UNKNOWN` and, from HTTP 403, `SCOPE_DENIED`.

- **`APPROVAL_EXPIRED`** is now returned at the four execution steps (production, sandbox, supplier credit, Bill correction), only when the saved approval's ten minutes have passed.
- **`POLICY_REQUIRED`** is now returned only where a missing or contradicted approved procedure is proven: the typed `preparation.PolicyRequired` from the procedure check on a manual save, and the reconciliation and card procedure gates. Database failures there now answer 503.
- **Idempotency keys:** a POST under `/v1/closes` with an `Idempotency-Key` runs at most once per firm within 24 hours. Only a fingerprint and the final status are stored, never the response. Repeats get `IDEMPOTENT_REPLAY` or `IDEMPOTENCY_IN_PROGRESS`. Docs: mcp.md.

Plan review: GPT-6 Sol (high). It found that a generic response-replay layer would copy secrets (link tokens, passcodes), could skip a route's own permission check, and could leave a committed effect behind a pending key. Adopted:

- no response bodies stored, and never re-execute after a success, a 5xx or an unfinished attempt;
- the query string and content type are part of the fingerprint;
- codes only where the condition is proven;
- MCP keys are out of scope and documented.

Tests (PostgreSQL):

- `TestIdempotencyKeyRunsAPostAtMostOnce`, on approval requests, which otherwise create a row per call:
  - A repeat creates no second request and says `IDEMPOTENT_REPLAY` with HTTP 200.
  - A different body with the same key is refused as reused, and a refusal holds its key too.
  - Pending and 5xx keys are not run again. After 24 hours the key is free, requests without a key are unaffected, and malformed or empty keys are refused.
- `TestProductionPostingSingleDurableAttempt`: a lapsed approval answers `APPROVAL_EXPIRED` with no write.
- `TestLoanDraftsNeedTheClientsApprovedLoanProcedure` and `TestCardPreparationRequiresAppliedProcedureAndClearingEvidence`: `POLICY_REQUIRED` for a missing loan procedure, and for a missing, unapproved or disabled reconciliation procedure.

Code review: GPT-6 Sol (high). Adopted:

- no key is freed after a 4xx, because some refusals follow real work (a procedure draft's model call, a saved connection step);
- an empty key header is refused;
- a failure to save the final status is logged, and the docs give the recovery step (re-read, then use a new key).

Each check was observed red with its rule removed (12 mutations): the middleware, the empty-key refusal, the 24-hour window, the uncertain 5xx, the fingerprint check, recording the status, the expiry test, the policy mapping, and the three reconciliation gates.

Not verified: concurrent requests with the same key (the primary key serializes them, but no test runs them in parallel). The APPROVAL_EXPIRED paths for sandbox, supplier credit and Bill correction share the tested helper but have no test of their own.

## Approved journal posting to Xero (2026-09-25)

Author: Claude Opus 5.5. Migration 060. This closes the largest ledger-parity gap. Approved drafts can now post to Xero as one manual journal, with the QuickBooks production controls.

- **Scope and tier:** the granular `accounting.manualjournals` scope. Manual journals are on every Xero plan, so no Advanced tier is needed.
- **Grant:** a person's client grant lists Xero account IDs, draft kinds, a limit and an expiry.
- **Kinds gate:** `XERO_POSTING_KINDS` defaults to none.
- **Preview:** built from live Xero reads (accounts, lock dates, the Trial Balance at the journal date), with a digest.
- **Approval:** ten minutes, with single approval, basis and independence.
- **Post:** one durable attempt by a person with `posting:execute`, under locks on the close, grant and connection. It carries Xero's `Idempotency-Key` and is read back.
- **Unknown outcomes:** never retried; verified by ID.
- **Cross-ledger guard:** QuickBooks and Xero check each other at the execution reservation.

`xeroAccess` (token refresh) and `postingSource` (source identity, newer-draft check) were extracted and are shared. Close deletion now covers Xero grants and attempts, and the "read-only" copy was updated.

Plan review: GPT-6 Sol (high). Adopted:

- the cross-ledger guard at both reservations, with a source key;
- locking the connection at reservation;
- the accounting-basis check;
- a strict Trial Balance reading (report date, duplicate and unreadable rows refuse; an account with no row counts as zero);
- close deletion and copy.

Code review: GPT-6 Sol (high), one delta round. Adopted:

- verification finds the client's active connection to the saved organisation, so a reconnection can still verify an unknown attempt;
- the QuickBooks competing-intent rule: while another Xero intent on the same accounts is approved or unsettled, this one can't be approved or posted, and must re-preview after the first settles;
- readback also compares narration and tax treatment;
- source freshness is rechecked inside the reservation transaction.

The same pre-existing freshness window in the QuickBooks reservation is recorded for a separate fix.

Tests (PostgreSQL, mocked Xero), `TestApprovedDraftPostsOnceToXero`:

- Refusals without a grant, without the kind enabled, without the journal scope, inside the lock date, for an ungranted account, above the limit, and for a connection now on another organisation.
- The preview builds one POSTED journal on the granted active account, skipping an archived one with the same name.
- Approval refusals: a stale digest, a cash-basis client, and a pending draft approved by someone who cannot review drafts.
- A changed balance stops the write, and a lapsed approval answers `APPROVAL_EXPIRED`.
- A draft attempted in QuickBooks is refused, and the QuickBooks-direction check (`postedElsewhere`) sees the Xero attempt.
- Posting needs `posting:execute`, posts once with the saved request ID as its idempotency key, verifies, and never posts again.
- A different readback (amounts, or narration only) is `EXECUTION_UNKNOWN`. Verification refuses until the journal matches, and verifies after a reconnection to the same organisation without a second post.
- A second intent on the same accounts waits while the first is approved.

`TestNetRefusesAnAccountListedTwice` covers the strict balance reading. Each check was observed red with its rule removed (24 mutations).

Not verified: a live Xero organisation. Xero's acceptance of an AccountID-only, no-tax, immediately POSTED journal, lock-date behavior, readback timing, and granular-scope enforcement need a scoped live check before enabling any kind. Not tested because they cover races only: the connection and grant locks at reservation, and the in-transaction freshness recheck.

## Live Stripe payout reconciliation (2026-09-25)

Author: Claude Opus 5.5. Migration 061. The settlement workflow can now fetch the close month's itemized payout reconciliation from the client's Stripe account instead of a download and upload. The existing recognition, tracing and settlement flow is unchanged.

- **Key and account:** a restricted, report-scoped key (secret keys refused), sealed and covered by key rotation. A person confirms the Stripe account's identity before use.
- **Capture:** the request intent and a Stripe idempotency key are saved before the call. A lost answer is re-asked with the same key, and a repeated click reuses the month's report in progress.
- **Worker:** claims expire after five minutes. Downloads come only from `files.stripe.com`, with no redirects, and are checked against the evidence and settlement limits and the parser's columns. Evidence and the same intake job as an upload are saved in one transaction.
- **Disconnect and deletion:** disconnecting ends pending reports, and close deletion knows the new tables.

Plan review: GPT-6 Sol (high). Adopted:

- confirming the account identity, and calling the key report-scoped;
- a recoverable capture intent with an idempotency key;
- the size and row limits;
- the download host guard;
- disconnect and key rotation;
- dropping the two unused columns and keeping one completion path.

Code review: GPT-6 Sol (high). Adopted:

- the connection is bound to the close's client;
- only the request that saved the intent calls Stripe, and an in-progress month skips the account check;
- the evidence-limit failure is written in the same transaction, which avoided a self-deadlock;
- an unexpected file location, and any definitive download refusal, end the run. Writing the test exposed that the worker kept such a refusal waiting; that is fixed;
- only a confirmed inactive connection fails a run, while a missing server key or database error waits;
- Stripe 429 and 409 are temporary, and a persistent intake failure (the firm's job limit) shows a waiting reason;
- identical evidence with another purpose is refused, as in uploads.

Tests (PostgreSQL, mocked Stripe), `TestStripeReportArrivesAsRecognizedEvidence`:

- A secret key is refused. A checked key waits for confirmation and is stored only sealed.
- Capture is refused before confirmation, before Stripe's data covers the month, and for a key now on another account.
- One run is requested with an idempotency key and the September UTC interval, and a repeat reuses it.
- A pending report is not downloaded and is checked again about a minute later. A finished report is saved once, and its intake job recognizes it as the Stripe export.
- Stripe's refusal is shown. A failed report and a report without the columns fail with the reason, and a lost answer is re-asked with its own key.
- A foreign file location receives no request and ends the run.
- Temporary conditions keep a report waiting: a wrong server key, and the firm's job limit (with its reason). Evidence saved with another purpose and a full close end it.
- A rate-limited request stays requesting, and another client's connection is refused.
- Disconnecting clears the key and ends reports in progress.

Each check was observed red with its rule removed (19 mutations). The two guards against concurrent captures (in-progress check, inserting request only) each cover a race and have no deterministic test.

Not verified: a live Stripe account, including restricted-key permission combinations, account-details access with that key, a real report's size and timing, and test-mode availability of this report type.

## Receivables and payables aging with customer statements (2026-09-25)

Author: Claude Opus 5.5. No migration. PRD §20 Cash Operations: prepare the supporting records before any follow-up. The past-due, unapplied-payment and open-credit questions already existed. This adds the aging and the customer statement from the open items each QuickBooks transaction capture already stores.

- **`quickbooks.Aging`** (pure): receivables and payables grouped by provider ID and currency, invoices and bills bucketed by days past due at month-end, undated items shown as unaged, unreadable balances listed but not counted, and credits kept apart.
- **`GET /v1/closes/{id}/aging`:** reads the latest completed or unchanged capture on the reading connection, flags an incomplete open-item read, and labels the figures as capture-time balances.
- **`/aging/statement`:** a CSV for one customer and currency, stating its basis, with spreadsheet-safe cells. Nothing is sent.

Plan review: GPT-6 Sol (high). Adopted:

- the "balances at capture time, aged against month-end" basis, instead of claiming month-end balances;
- the incomplete flag and the one-year credit window stated;
- grouping by provider ID and currency, with credits apart from the buckets;
- separate treatment for undated and unreadable items;
- `csvText` for cells;
- testing an `unchanged` capture and an incomplete read.

Writing the unit test exposed that one unreadable amount made the whole document decode fail silently; amounts are now read raw so it is listed.

Code review: GPT-6 Sol (high), two delta rounds. Adopted:

- documents without a party stay apart, and a statement needs a customer ID;
- an undecodable record or an unreadable balance marks the report incomplete;
- a missing currency takes the connection's home currency, or shows as unknown rather than USD;
- dates are escaped in the CSV;
- a statement line without a number shows the QuickBooks ID;
- each balance is rounded to cents before summing;
- unique row keys in the panel.

Tests:

- `TestAgingBucketsByDueDateAndKeepsCreditsApart` (pure) covers:
  - each bucket, an undated invoice and a zero balance dropped;
  - credits apart, and another currency grouped apart;
  - an unreadable balance listed but not counted, and an undecodable record counted;
  - unidentified customers kept apart, a missing currency taking home, and totals of rounded lines;
  - vendor bills and credits.
- `TestAgingUsesTheLatestCaptureAndDownloadsAStatement` (PostgreSQL):
  - The aging waits without a capture, uses the newest capture and flags it incomplete, also for an unreadable record or balance.
  - The statement escapes a formula-like name and date, states its basis, lists the invoice and names an unnumbered payment by its ID.
  - An unknown or empty customer gets 404.

Each check was observed red with its rule removed (20 mutations).

Not verified: live QuickBooks open-item payloads, and the panel in a browser.

## Daily books: documents beside ledger transactions (2026-09-25)

Request: matching documents to ledger transactions during the month (PRD §20 Daily Books). `GET /v1/closes/{id}/matching` sets the close's uploaded prepaid invoices beside the bills and purchases of the latest transaction capture (the capture the exceptions list reads, now one shared `latestCapture`), and the close shows it as "Documents and ledger transactions". Pairs need a unique currency and exact amount on both sides; nothing is stored or written to QuickBooks.

Plan review: GPT-6 Sol (high). Adopted:

- only the primary source of an intake read as prepaid work counts, and only a total quoted from that source;
- labels say what the inputs cover: documents in this close, bills and purchases dated in the close month;
- unreadable totals are counted, and the unmatched list is capped at 200 with the total.

Accrual intakes are left out: an accrual is for a service not yet billed, so its document is not expected in the ledger.

Tests:

- `TestMatchPairsOnlyUniqueCurrencyAndAmount` (pure): a unique pair, two documents or two transactions on one amount left to a person, currencies kept apart, unmatched transactions largest first.
- `TestMatchingReadsTheLatestCaptureAndQuotedInvoiceTotals` (PostgreSQL): waits without a capture; pairs a quoted invoice total with the one bill of that amount while an invoice and a reconciliation are ignored; a total quoted from another source is without a total; a superseded document is left out; a purchase without a currency takes the home currency; an unreadable total is counted.

Each check was observed red with its rule removed (18 mutations). One survivor showed a redundant value comparison, which was deleted.

Code review: GPT-6 Sol (high), two delta rounds. Adopted:

- a total with grouped digits such as `1,2` is unreadable rather than read as 12;
- the panel reads each time it opens, clears the previous result, and drops an older answer.

Not verified: live QuickBooks payloads and model-read invoices, and the panel in a browser.

## QuickBooks production posting rechecks the source under the close lock (2026-09-25)

Follow-up from the Xero posting review: production execute and approve checked for a newer draft of the same source only before their transaction. A draft saved between that check and the close lock could not stop the write. The check now runs again inside the transaction, after the close row is locked, as Xero posting does. New drafts wait on that lock (the manual save through its foreign key, the worker through its own lock), so none can commit between the recheck and the commit of the posting decision.

Test: `TestProductionPostingSingleDurableAttempt` holds the close row lock, inserts a newer draft of the same source, starts execution, waits until it is blocked on the lock, then commits. Execution answers 409 with no write. With the recheck removed, the same run posted and verified the journal (observed red).

Blast radius: the production posting, entries, Bill correction, rehearsal and Xero posting tests pass.

Code review: GPT-6 Sol (high), one delta round. Adopted: the test rolls back its lock holder on failure, waits at most 10 seconds, and watches only the session its own holder blocks; the timing claim is limited to the commit of the posting decision.

## Xero transactions feed the close's checks (2026-09-25)

Request: read Xero transactions so Xero clients get schedule discovery, matching, and the aging and statements. Each Xero capture now also reads the month's invoices, bills, credit notes, bank transactions, manual journals and payments, and the open invoices, bills, credit notes, overpayments and prepayments, with the new read scopes. `xero.Ledger` reshapes them into the QuickBooks record shapes, so the existing exception, schedule, matching and aging code reads them unchanged. `latestXeroBooks` picks the newest Xero capture for the close's month from the client's current connection when it is newer than the latest QuickBooks capture. Payee causes and the firm-wide Daily Books list follow in the next change.

Plan review: GPT-6 Sol (high). Adopted:

- open overpayments and prepayments are read from their own endpoints;
- dates use the Xero date parser, payment currency comes from the invoice and journal currency is the base currency;
- only invoice and bill payments are reshaped, refunds are kept but not checked;
- no consistent-snapshot claim: each type pages to its end and must match Xero's count, and the note says what can be missed;
- the capture is bound to the close's month and a connection that is not disconnected, and a newest capture without transactions says why instead of showing an older one's;
- a connection without the read scopes is recognized from its token and told to reconnect;
- a person's capture gives the transaction reads the time left before saving; the daily capture has three minutes.

Simplified after mutation testing: a positive-balance filter and a status re-check on open items were deleted, because the checks that read them already skip zero balances and Xero filters by status.

Tests:

- `TestLedgerReshapesXeroTransactionsForTheChecks` (pure): every kind, tax-inclusive and exclusive lines, account codes, item lines, drafts, deleted records and refunds left out, payment and journal currency, schedule discovery for prepayment and fixed asset accounts, a suspense purchase, aging of open items and credits, open-item questions, and a snapshot without transactions.
- `TestTransactionsNeedTheReadScopes` and `TestTransactionsPageToTheEndAndCheckXerosCount` (pure, mock Xero): both scope forms, two pages, the month range and the open-item range, a count mismatch, a change during the capture, and the 5,000 cap.
- `TestXeroTransactionsDriveTheCloseChecks` (PostgreSQL): the panel's counts; exceptions with Xero wording, schedule discovery, matching and aging from a Xero capture; another month's capture ignored; a newer capture without transactions explained in all three; a newer QuickBooks capture read instead.
- `TestXeroConnectsOneOrganisationAndCapturesTheTrialBalance` now checks that a connection without the scopes still captures and says to reconnect. `TestExceptionsScanTheWholeLatestCapture` disconnects its Xero connection before its QuickBooks-only steps.

Each check was observed red with its rule removed (31 mutations).

Code review: GPT-6 Sol (high), two delta rounds. Adopted:

- a QuickBooks capture whose evidence was superseded no longer hides the Xero books;
- overpayments and prepayments made through the bank are not reshaped as purchases or deposits;
- every page must carry Xero's count, including a full first page (the test sends a pagination object without a count; this fixture was tightened after the second round, when a mutation showed the first version passed for another reason);
- the Xero panel no longer says its snapshot feeds the checks;
- the one-year limit on unused credits is stated as QuickBooks-only.

Not verified: live Xero payloads, rate limits and timing, and the panels in a browser.

## Xero payee causes and Daily books (2026-09-25)

Request: the second half of reading Xero transactions. Changes from last month now explain a Xero client's movements by contact, from `xero.PayeeActivity` over each month's captured transactions. Each Xero capture records its findings when it is saved (migration 062), and Daily books lists a Xero client from its newest capture when that is newer than its latest QuickBooks capture; a review points at exactly one capture from either ledger.

Plan review: GPT-6 Sol (high). Adopted:

- Mark reviewed accepts only the capture Daily books shows, comparing both ledger and ID under the close lock;
- an account with lines in another currency shows no causes, because the capture does not hold base-currency amounts;
- every requested account gets payees, and a line without an account counts as unnamed everywhere, so no missing-payee question is asked from an incomplete month.

Tests:

- `TestPayeeActivitySignsAsTheProfitAndLossShows` (pure): revenue positive from invoices and deposits, a supplier credit negative, a purchase and a journal's credit line, a foreign-currency line flagged, unattributed journal lines, an item line making every account uncertain, and an account with no lines.
- `TestFluxExplainsMovementsByPayee` (PostgreSQL) adds two Xero months with transactions: a new and a missing contact, reconciled, one question; then a foreign-currency line leaves the account unexplained.
- `TestXeroConnectsOneOrganisationAndCapturesTheTrialBalance` checks that captures without transactions record no findings and one with them records an overdue bill in Xero's words.
- `TestDailyBooksShowsExceptionsNewSinceTheLastReview` adds a Xero client: listed from its snapshot, reviewed only with the Xero source, only new findings after review, nothing listed when the newest snapshot has no transactions, and a newer QuickBooks capture shown and reviewable instead, with the review pointing at one capture.

Each check was observed red with its rule removed (24 mutations).

Code review: GPT-6 Sol (high), one delta round. Adopted: Daily books passes over a QuickBooks capture whose evidence was superseded, as the close's own checks do; lines of overpayments and prepayments made through the bank count as unnamed in payee causes; the Daily books text covers both ledgers. Refuted, and confirmed in the delta round: a Xero snapshot saved while Mark reviewed runs. The snapshot's foreign key to the close takes a lock that conflicts with the review's row lock, so the two are serialized either way. The test holds the close lock and shows the snapshot waits. An explicit lock added first was deleted after it survived its mutation for that reason.

Not verified: live Xero payloads, and the panels in a browser.

## Loans: fees paid in cash and debt issuance costs (2026-09-25)

Request: loan fees paid in cash and debt issuance costs (index-rate lookups follow). A payment can carry a fee paid in cash with its statement line; it is shown and totalled, proposes no entry, and is named as a possible cause when the ledger's loan balance is lower than the lender's by exactly those fees. Debt issuance costs are amortized to interest straight-line with the existing prepaid schedule engine, from their own account, under a new separately approved loan method `lender_statement_costs_v1`.

Plan review: GPT-6 Sol (high). Adopted:

- issuance costs need their own approved method, not a relabelled variable-rate approval;
- the amortization entry is proposed only when the preparer confirms the costs were booked to their account and cites its balance, so an unbooked cost cannot produce a credit to interest;
- straight-line needs the accountant's cited not-materially-different conclusion, and the draft shows the costs as deducted from the debt;
- a cash fee needs both a positive amount and its statement line, and the matching difference is a possible cause, not a conclusion.

Tests:

- `TestFeesPaidInCashAreShownWithoutAnEntry` (pure): the fee on its payment, the total, unchanged interest, no entry, the possible-cause wording only for an exact match, a missing statement line, and the fixed-rate method refused.
- `TestIssuanceCostsAmortizeToInterest` (pure): 1,200.00 over 2026 leaves 300.00 unamortized in September and proposes 100.00 to interest against 400.00 booked; over-amortization reverses; rate changes allowed under the new method; the variable method, the loan account, a missing conclusion, an unconfirmed booking, a missing ledger reference and a term ending before the close are refused.
- `TestLoanDraftsNeedTheClientsApprovedLoanProcedure` (PostgreSQL): an issuance cost draft is refused under the variable procedure and saved under the new one, which a firm can publish.
- `TestTheClientsBasisDecidesWhichWorkCanBeSavedAndApproved` checks that a cash-basis client cannot amortize issuance costs.

Each check was observed red with its rule removed (16 mutations).

Code review: GPT-6 Sol (high), one delta round. Adopted, in the form: adding a rate change or fee keeps the issuance cost method; a payment row with only a fee reaches validation; adding, removing or editing issuance costs clears the checked result; each cash fee shows its statement line; the docs and the result no longer say the fee is already booked.

Not verified: real lender statements and closing statements, and the form in a browser.

## Loans: rates from a published index (2026-09-25)

Request: look up variable rates from an index. `GET /v1/closes/index-rates?index=SOFR|EFFR&date=` reads the New York Fed's published value on or in the ten days before the date and stores it (migration 063, shared reference data with an open row-level security policy). A rate change can carry the index, the date it was published for, its value and the note's margin; `loans.Prepare` requires the rate to be their sum, and `preparation.CheckIndexRates` refuses a draft whose index value is not the stored published one at preview, at save and when a queued job runs.

Plan review: GPT-6 Sol (high). Adopted:

- the rate change stores the date the value was published for, not the date asked;
- the table has row-level security with a policy, as hosted readiness requires;
- the check runs again when a queued job runs, in case the value was revised after queueing;
- the lookup is a firm route behind current membership;
- no stored source address.

Simplified after mutation testing: a date filter duplicated by the Fed's own end date was deleted, and the check at plan queueing was dropped because the job checks again when it runs.

Tests:

- `TestRatesFromAnIndexAreTheirSum` (pure): SOFR 3.66% plus a 2.500% margin is 6.160%; a rate that is not the sum, an index published after the change, an unsupported index and a missing margin are refused.
- `TestIndexRatesAreLookedUpFromTheNewYorkFed` (PostgreSQL, mocked Fed): the ten-day window, the latest SOFR before a Sunday is the Friday's and is stored, another index's value is ignored, a revision replaces the stored value, none published is 404 and an unsupported index 422.
- `TestLoanDraftsNeedTheClientsApprovedLoanProcedure` adds a SOFR rate change whose value is not the stored one: refused at save, at preview and by the worker for a queued job; accepted once it matches.

Each check was observed red with its rule removed (13 mutations).

Code review: GPT-6 Sol (high), two delta rounds (the first attempt failed on the reviewer's authentication and was rerun). Adopted: the Fed call refuses redirects; the form clears a looked-up value when the index changes, drops an answer for a changed index or margin, and applies only a row's latest lookup; the docs now say a saved draft keeps the value it was saved with and approval does not look it up again (a check at approval and posting was not added: a revision after saving is rare, and the lender's statement fixes the interest).

Not verified: the live New York Fed service beyond one manual read of its SOFR endpoint, and the form in a browser.

## Stripe manual and instant payouts (2026-09-25)

Built while the GPT-6 Sol reviewer could not authenticate (its key was rejected), at the user's direction: committed on its own branch, reviewed before it is opened or merged.

Request: Stripe manual and instant payouts. Stripe's payout reconciliation report covers automatic payouts only; its docs point manual payouts to the Balance report and say instant payouts cannot be tied to transactions. A second settlement method, `stripe_payouts_v1`, reads Stripe's itemized Payouts report (`payouts.itemized.5` columns balance_transaction_id, currency, net, reporting_category, payout_id, effective_at, payout_expected_arrival_date, payout_status) and traces each payout to its bank row with the existing matching, review and in-transit steps. Failed or canceled payouts expect no deposit and claim none. A draft without a method stays an automatic-payout draft, so saved drafts are unchanged.

Test: `TestManualAndInstantPayoutsAreTracedToTheirDeposits` (pure): a manual and an instant payout matched to their deposits, a failed payout with its reversal and one without it (which must not claim another payout's deposit), an unmatched payout blocking, the method's limit stated, a fee entry refused, and a positive payout row, funds leaving in another month and an automatic-payout export refused.

Each check was observed red with its rule removed (14 mutations).

Plan-and-code review: GPT-6 Sol (high), once available again. Adopted: the bank file may run to four days after the last expected arrival, so a payout that left the balance at month-end can match; a failed payout cannot take a bank row through review or in-transit; a reversal no longer changes the payout's amount and the question says whether the return is in the file; the result view, the review export and the docs describe this method without transactions or categories and say the month's activity must be reconciled separately; a revised draft restores its own method; the method is described as a whole-month trace of every payout in the report, since the report has no manual or instant marker; the parser no longer builds payout categories. Two delta rounds then closed the payouts table and review export (no transactions, gross, fees or categories file for this method); the bank-file hint's wording (run the file through the later of four days after month-end and four days after the last expected arrival) was taken verbatim from the second round's answer without a third round.

Not verified: a real Payouts report export (the sign convention is enforced, not assumed silently), and the form in a browser.

## Stripe refund and revenue entries (2026-09-25)

Request: Stripe revenue and refund entries. With the fee entry, a settlement can propose a refund entry (refunds less failed refunds of payouts deposited this month, from the deposit account to a refunds account) and, when the accountant's rule says the deposit account is a clearing account, a revenue entry (gross charges less uncaptured amounts, out of clearing). Both are drafts to book by hand; production posting still posts the fee entry only.

Plan review: GPT-6 Sol (high). Adopted:

- disputes and other categories left in the deposit account are named for the payouts deposited this month;
- the revenue entry needs an explicit clearing-account confirmation and the refund account must differ from the deposit account; booked amounts are those for the same payouts;
- the drafts appear in the review package, year-end adjustments and tie-out (where they stay proposed even once the fee entry has posted);
- the existing claim that deferred fees are "left for the month their deposit is booked" was corrected: the next month's report does not include those payouts, so they are recorded by hand;
- two named proposals instead of a list.

Tests:

- `TestRefundAndRevenueEntriesCoverPayoutsDepositedThisMonth` (pure): refunds 95.00 (a failed refund reduces them) and revenue 400.00 of the payouts deposited this month; deferred charges and the dispute left in clearing named; refunds already booked and revenue booked beyond the charges; nothing proposed with an unresolved trace; six refusals.
- `TestYearEndHandoffRollsForwardApprovedWork` includes a settlement's refund draft in the adjustments; `TestTieOutComparesBalancesWithLatestSupport` shows a refund draft still proposed after the fee entry posted.

Each check was observed red with its rule removed (12 mutations).

Code review: GPT-6 Sol (high), one delta round. Adopted: revising a saved settlement restores its payout method, fee, refund and revenue entries and their fields; the clearing confirmation is asked again.

Not verified: real client books using these rules, and the form in a browser.
