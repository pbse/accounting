# Provider capability inventory

Phase A of the [agent-first build plan](agent-first-build-plan.md#delivery-sequence-and-launch-gates) requires a provider capability inventory. This is it: for each row of the plan's [integration contract](agent-first-build-plan.md#integration-contract), what the code on `main` does on 2026-10-06, and how far each capability has been proven. It is read from the code and the dated records in [validation](validation.md), not from provider marketing or function names.

**Headline: no provider capability has been verified against a live provider.** That covers QuickBooks, Xero, Stripe, Resend, the model provider, Google Gemini's PDF reading, Google Drive and the Cloudflare upload scan. Every provider test intercepts the provider's HTTP. The records say so in their own words:

- 2026-09-22: "No live Intuit consent, account reads, postings or cloud deployment occurred."
- [QuickBooks onboarding](quickbooks-onboarding.md): "Live sandbox validation remains pending; development integration tests intercept all Intuit HTTP calls."
- 2026-09-25: "Not verified: a live Xero organisation."
- [Email delivery](email-delivery.md): "no live delivery has been validated."
- 2026-10-04: "No production provider credentials were installed and no paid inference occurred."

The only live checks so far are infrastructure: the production cutover on 2026-10-04 (`/readyz` returned 200) and a browser check that sign-in hands WorkOS the production redirect.

## How to read it

Each capability is marked:

- **Live**: verified against a real provider, with a dated record.
- **Synthetic**: implemented and tested only with stubbed provider responses or synthetic data. The test is named.
- **Untested**: implemented, with no test.
- **Missing**: not implemented.

Paths are relative to `services/backend/internal/`.

**Per-firm outcomes are already recorded in the app.** The firm's Security view (`GET /v1/closes/security`, `transport/capabilities.go`) lists each connector operation with:

- the date and close of its latest verified sandbox and live outcome in that firm;
- whether the server enables it, and for which clients;
- its owner, fallback and stated limitation.

An administrator records the sandbox-check and accountant-acceptance stages there (`POST /v1/closes/security/operations`, migration 111). It covers QuickBooks, Xero and Stripe operations only. This document is the cross-provider view the app does not give: email, the model provider, uploads and identity as well as the ledgers, and what remains unproven everywhere.

## QuickBooks Online

| Capability | State | Evidence |
| --- | --- | --- |
| Company identity | Synthetic | CompanyInfo is read on connect (`quickbooks/connection.go`) and the name is checked again before every snapshot (`quickbooks/period.go`). A production company's name must match the client. `TestQuickBooksScopedOAuthRefreshAndSnapshots` |
| OAuth refresh | Synthetic | A durable `refreshing` claim; a lost result becomes `reconnect_required` (`quickbooks/sync.go`). Same test. |
| Revocation at Intuit | Missing | Disconnecting clears the stored tokens only (`transport/quickbooks.go`). The onboarding doc says it "does not call the remote revocation endpoint yet." |
| Pagination | Synthetic | Accounts: 1,000 a page, at most 10,000. Entities: 500 a page, at most 2,500 a type (`quickbooks/entities.go`). `TestPeriodEntitiesRequiresCompletePagedRevisions` |
| Full populations | Synthetic, bounded | Twelve transaction types, each counted before and after. The capture is compared with TransactionList (`TestCertifyComparesTheReportWithTheCaptureTransactionByTransaction`). A company over a limit fails; there is no path for larger companies. |
| Revisions and change hints | Synthetic | Each record needs a SyncToken and LastUpdatedTime, and the Preferences revision is checked again at the end. Webhook hints are taken from sandbox companies only (`transport/quickbooks_webhook.go`). `TestChangeHintsRefreshAfterTheCaptureThatMissedThem`, `TestTheEndedMonthSnapshotIsCapturedAndRefreshedWhenItChanges` |
| Scheduled refresh | Synthetic | Through the connection the books are read from, production first, for current- and prior-month closes not signed off and any close an agent works on (`quickbooks/sync.go`, `TestTheProductionConnectionIsReadFirst`). |
| Dimensions (class, location) | Missing | Kept in the raw JSON, never read. |
| Attachments | Missing | No Attachable reads. |
| Report basis | Synthetic, accrual only | Accrual is requested and checked (`quickbooks/period.go`). Cash basis is missing. |
| Sandbox write and readback | Synthetic | `TestSandboxAttemptIsDurableAndNotRepeated`, `TestGovernedFinishPostsToTheSandboxAndSignsOffOnlyTheDeclaredScope` |
| Production write authority | Synthetic | A grant per client and kind (`transport/production_posting.go`). `TestProductionPostingSingleDurableAttempt`, `TestProductionLedgerEffect`. **When `PRODUCTION_POSTING_KINDS` is unset, `prepaid` is enabled**, though [production posting](production-posting.md) says to enable a kind only after a sandbox check, and none is recorded. |
| Unposted bank feeds | Missing | |
| Native reconciliation status | Missing | The app reads no per-record reconciliation status from QuickBooks. |
| When unavailable | Synthetic | Snapshots are immutable and keep their capture times. A failed refresh is saved on its sync row. A stopped refresh adds a readiness blocker, which holds sign-off and, through it, publication (`transport/readiness.go`, `TestScheduledPeriodSnapshotRefreshesUntilAPersonCaptures`). An approval is invalidated by a changed capture, not paused by a failed read. |

## Xero

| Capability | State | Evidence |
| --- | --- | --- |
| Organisation identity | Synthetic | The person picks one organisation from those authorised in the sign-in (`xero/xero.go`). Unlike QuickBooks production, its name is not checked against the client. `TestXeroConnectsOneOrganisationAndCapturesTheTrialBalance` |
| OAuth refresh | Synthetic | The rotated token is saved (`transport/xero.go`); the test covers two expiries in a row. |
| Revocation at Xero | Missing | Local only. |
| Pagination | Synthetic | 1,000 a page; Xero's item count must match; at most 5,000. `TestTransactionsPageToTheEndAndCheckXerosCount` |
| Full populations | Synthetic, bounded | Nine read sets. If transactions fail, the Trial Balance is still saved with the reason. |
| Revisions and change hints | Synthetic, partial | A record updated after the capture began is refused; no per-record revision is stored. Webhook hints cover invoices and credit notes. `TestXeroWebhookHintsTheTenantAndTheSweepCapturesHintedCloses` |
| Account coverage | Synthetic | The same coverage questions and blockers as QuickBooks, from the Xero capture of the month (#278). |
| Dimensions (tracking) | Missing | Only compared for equality during a bill recode. |
| Attachments | Missing | |
| Report basis | Missing | The Trial Balance is requested with a date only; no basis is chosen or checked. |
| Write and readback (demo company) | Synthetic | `TestApprovedDraftPostsOnceToXero`, `TestXeroBillLineIsRecodedOnceAfterIndependentApproval`. Xero does not mark demo companies, so the app counts every Xero outcome as live. |
| Production write authority | Synthetic | `XERO_POSTING_KINDS` defaults to none. |
| Unposted bank feeds | Missing | Bank statement lines are not read. |
| Native reconciliation status | Synthetic, read-only | Xero's `IsReconciled` flags are counted per bank account. `TestNativeReconciliationCountsXerosFlagsPerBankAccount`. 2026-09-26: "Not verified: live Xero payloads". |
| When unavailable | Partial | The error is saved on the connection and shown in its panel, and snapshots keep their capture time. **A refresh that fails after the month was captured adds no sign-off or publication blocker**, unlike QuickBooks. A missing capture of the month is still a coverage blocker. |

## Stripe settlements

| Capability | State | Evidence |
| --- | --- | --- |
| Source completeness | Synthetic | A fetched report's data window must cover the month, and net must equal gross less fees, in USD (`reconciliation/settlement.go`). Totals are not tied to Stripe's own opening and closing balances; the app says so as the operation's limitation. `TestStripeReportArrivesAsRecognizedEvidence`, `TestStripePayoutsAndActivityReportsAreCapturedByKind` |
| Refunds | Synthetic | `TestRefundAndRevenueEntriesCoverPayoutsDepositedThisMonth` |
| Disputes | Detected only | Categorised and raised as a question; no entry is proposed. |
| Fees | Synthetic | `TestStripeFeeEntryCoversFeesOfPayoutsDepositedThisMonth`. Posting needs `settlement` among the posting kinds, which is off by default. |
| Clearing | Synthetic | The revenue entry moves gross charges out of clearing. |
| Payout reconciliation | Synthetic | `TestSettlementTracesSourcePayoutsToBankRows`, `TestManualAndInstantPayoutsAreTracedToTheirDeposits`, `TestManualPayoutsRollTheStripeBalanceForwardWithTheMonthsActivity` |
| Gated until verified | Missing | Stripe reads are always marked approved (`transport/capabilities.go`), and the routes have no feature gate. |

The 2026-09-25 validation heading "Live Stripe payout reconciliation" names the API-fetch feature; it is not a live test. Its entry says: "Not verified: a live Stripe account".

## Upload, Drive and forwarding

| Capability | State | Evidence |
| --- | --- | --- |
| Exact bytes | Synthetic, except workbooks | PDFs keep their original; text is stored verbatim; both with a SHA-256 digest (`transport/evidence.go`). **An .xlsx upload is kept as CSV text of its first sheet, and the workbook itself is not kept.** Forwarded mail keeps its body and digest. `TestForwardedFilesKeepsTwoExactAttachmentsAndRejectsUnsupported` |
| Source identity | Forwarding synthetic; Drive missing | The forwarded message's digest is kept, and its sender is recorded as unverified. A Drive file reaches the app as an ordinary upload; no Drive file ID or revision is stored. |
| Malware scan | Forwarded and client-request files synthetic; others missing | A scan is tied to the exact digest, and release needs a clean scan from the last 24 hours (`forwarded/worker.go`). `TestCoverageRequestsQuarantineAndResolution`, `TestIsolatedScanner`. A local ClamAV check on 2026-09-22 accepted a clean file and rejected EICAR; the Cloudflare verdict is stubbed. **Direct uploads, Drive files and fetched Stripe reports are not scanned.** |
| OCR | Synthetic | Per page, with OCR text labelled for checking against the original (`documents/extract.go`). Gemini reads every page when its key is set (see the model provider), and Tesseract is the fallback. `TestExtractMixedPDF`, which needs Poppler and Tesseract. `TestCheckPageAndFactConflicts` |
| Duplicate versus replacement | Synthetic | The same digest reuses the existing source; a replacement needs a newer source and a reason. `TestReviewAndEvidenceReplacement`, `TestReplacedInvoiceIsRecalculatedStraightThrough` |
| Lineage | Synthetic, except Drive | `superseded_by` with the replacement's reason; a released forwarded file links to its evidence. |
| Drive selection | Synthetic | `TestDriveFilesAreListedAndHandedBackOnlyWithAGrant`. "Not verified: a live WorkOS Pipes integration and Google Drive responses." |

## Email and client links

| Capability | State | Evidence |
| --- | --- | --- |
| Sender and routing | Synthetic | Sending needs a verified domain; replies route to `+reply-<id>`. `TestApprovedEmailDurableRetryAndScope` |
| Recipient authorization | Synthetic, by confirmation only | A person confirms the recipient. There is no client-contact directory, and a client link is a bearer token. |
| Approval before sending | Synthetic | The worker checks the approval again before sending. |
| Signed delivery events | Synthetic | Svix signatures within 300 seconds. `TestWebhookOfficialVector` |
| Replies | Synthetic | Held and redacted until the sender matches and a reviewer releases them. `TestInboundReplyHeldUntilSenderAndReviewerMatch`, `TestReplyTextAcceptsPlainTextAndRejectsAttachments` |
| Failed or pending delivery shown | Synthetic | An unknown outcome is settled from the provider's records. `TestAnUnknownEmailIsSettledFromTheProvidersRecords`, `TestStaffSettleAFirmsEmailDuringRecovery` |

2026-10-04: "Resend had no webhooks, and production had no email connections."

## Model and extraction provider

| Capability | State | Evidence |
| --- | --- | --- |
| Bounded context | Synthetic | A 2-minute timeout, 2 MiB requests and 1 MiB responses by default (`llm/llm.go`). `TestOpenAIFailuresAndStreaming`, `TestLargeInventorySelectionIsBoundedAndReviewable` |
| Redaction | Synthetic | Applied before the model sees sources. `TestRedactRemovesCardsBankNumbersAndSecrets`. "Not verified: live model behaviour on redacted text." |
| Source verification | Synthetic | Quotes are checked against the redacted copy. `TestQuoteLocationAndExplicitClientBoundary` |
| Model and version recorded | Partial | Preparation stores the configured model and the response ID. The model name the provider returns is read (`llm/openai.go`) but not stored with the result. |
| Timeout and cost limits | Synthetic, calls not money | A daily call budget per firm and three attempts. No money cap; in subscription mode the byte caps do not bound hidden reasoning tokens. |
| Prompt injection | Synthetic | `TestHostileDocumentCannotSteerTheAgent` proves the execution boundary, not the model's behaviour. |
| Gemini PDF reading (2026-10-07) | Synthetic; not verified live | With `GEMINI_API_KEY` on the worker, every PDF page goes as a PNG to Gemini (`documents/gemini.go`, `generateContent`, default model `gemini-3.8-flash`). On a page with a text layer, the reading is used only when it says exactly what the layer says, apart from whitespace, so a misread figure, swapped row or changed word is never taken. A scanned page falls back to Tesseract when Gemini fails, is cut short, is blocked or is over the firm's daily call limit. Each page's label names how it was read and the model version Gemini reports. The worker reports its setting in its heartbeat for Firm settings and the security view. `TestGeminiReadsEveryPageAndNeverTakesAFigureTheTextLayerLacks`, `TestGeminiIsConfiguredOnlyWithAKey`, `TestSameReadingSetsOnlyLayoutAside`, `TestFirmSettingsShowHowTheWorkerReadsPDFs`. Unredacted page images reach Google; use a paid key, since the free tier allows Google to use content. |
| When unavailable | Synthetic | No fallback provider. Deterministic proposals still need a person's confirmation. `TestEveryPurposeCanSwitchBetweenAPIAndContextMachine`, `TestResearchResultsFailClosed` |

## Identity, business access and operations

| Capability | State | Evidence |
| --- | --- | --- |
| Server-side roles | Synthetic, partly live | Signed WorkOS tokens (`auth/auth.go`) and per-client access (`transport/access.go`). `TestVerifiedOrganizationSession`, `TestOrganizationIsolation`. Live, 2026-10-04: sign-in hands WorkOS the production redirect and WorkOS shows its form. "Not verified: a real WorkOS sign-in, refresh and sign-out on the deployed site." |
| Client boundaries | Synthetic | `TestRowLevelSecurityIsolatesFirms`, `TestAgentsActOnlyUnderAnApprovedMemberGrant` |
| Session revocation | Synthetic | Server-mode sign-out and staff revocation. `TestServerSessionKeepsWorkOSTokensOnTheServer`. In client mode, revocation waits for the token to expire; production's mode is the `AUTH_SESSION` repository variable. |
| MFA | Missing in the app | Left to WorkOS configuration ([operations](operations.md)); no MFA claim is checked. |
| Alert delivery | Synthetic, off by default | A webhook. `TestOperationsAlertSendsOnlyScopedMetadata`, `TestPlatformProblemsAlertOnceAndResolve`. A live receiver is pending. |
| Restore | Synthetic, local only | `infra/postgres/check-restore.sh`, rehearsed 2026-09-22: "hosted RTO/RPO remain unverified." |
| Deletion and export evidence | Synthetic | `TestCloseDeletionRequiresPolicyAndSettledWork`, `TestAFirmDownloadsItsRecordsAtExit` |

## Gaps

Ordered by what they risk. Each live check needs credentials the user installs through the user-run scripts; none goes through an agent's context.

1. **Nothing is proven live.** Run, and record with dates:
   - QuickBooks sandbox consent, reads and write/readback, then one production read;
   - a Xero demo-company read and post;
   - one Stripe test-mode report run;
   - one Resend send, its delivery webhook and one reply;
   - one model call per purpose;
   - one Gemini reading of a scanned PDF page and of a page with a text layer;
   - one Drive selection;
   - one Cloudflare scan verdict.

   Record each with its date in [validation](validation.md). For QuickBooks, Xero and Stripe operations, also record the sandbox-check and acceptance stages in the Security view, which takes only those three providers.
2. **Production `prepaid` posting is on by default without a sandbox record.** Decide whether the default becomes none, matching Xero, or record the sandbox check.
3. **Not every file is scanned.** Direct uploads, Drive files and fetched Stripe reports skip the malware scan that forwarded and client-request files get.
4. **A failed Xero refresh does not hold sign-off** the way a stopped QuickBooks refresh does.
5. **Neither ledger revokes OAuth at the provider** on disconnect.
6. **The Stripe path is not gated.** It is approved without verification, and nothing ties its totals to Stripe's own balances.
7. **Workbooks and Drive files lose their identity.** The .xlsx original is not kept, and a Drive file keeps no Drive identity.
8. **Not implemented:** ledger dimensions, attachments, unposted bank feeds, QuickBooks reconciliation status, cash basis, and Xero basis control. These matter only for the clients whose admitted scope needs them.
9. **The provider's reported model name is not stored**, and there is no money cap on model use.
10. **No MFA claim is checked, no alert receiver is on, and there has been no timed hosted restore** (B12).

## Corrections made with this inventory

- [Source sync and forwarding](source-sync-and-forwarding.md) said automatic QuickBooks checks were sandbox-only. They read the connection the client's books are read from, production first.
- The same doc implied the Drive path's files miss only a scan beyond the direct upload's. Neither is scanned.

Dated validation entries are records of their day, so older statements there are left as written. These include the 2026-09-21 entry's "not implemented" for sandbox refresh and production posting, which both exist now.
