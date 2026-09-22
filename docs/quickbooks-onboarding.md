# Client-specific QuickBooks sandbox onboarding

This release connects a sandbox company to one organization/client and captures its account directory and bounded period reports. It adds no ledger writes. The existing manually configured sandbox-posting capability remains separate; these OAuth credentials never feed that path.

## Configure once

1. Apply `017_quickbooks_connections.sql` after migrations 001–016. Stop API/worker during upgrades, retain a backup and restart matching revisions. Fresh Compose volumes apply every migration.
2. Create/configure an Intuit developer app for the **sandbox**, using its development client ID/secret. Register the exact HTTPS callback `https://YOUR_API_HOST/v1/quickbooks/callback`. For a same-origin Cloudflare deployment this is the application hostname; with a separate API URL it must use the API hostname.
3. Set server-only `QBO_CLIENT_ID`, `QBO_CLIENT_SECRET`, `QBO_REDIRECT_URI`, `PUBLIC_APP_URL` (the exact HTTPS SPA origin) and `SECRETS_KEY` (the existing 32-byte hexadecimal encryption key). Keep secrets out of frontend variables, logs and source control. The worker needs no QBO credentials for this manual read capability.
4. In GitHub's staging environment set `QBO_CLIENT_ID` and `QBO_REDIRECT_URI` as variables, `QBO_CLIENT_SECRET` and `SECRETS_KEY` as secrets. The deploy workflow forwards these to the API container. It checks partial configuration but does not provision an Intuit app or authorize a company.
5. The operator needs an enabled WorkOS organization, `integrations:manage`, and access to the selected client (`clients:all` or an explicit client assignment). Local shared-token users can inspect the unconfigured state but cannot connect a real third-party account.

Local development without credentials still works for all existing preparation flows. The integration test mocks Intuit and uses synthetic PostgreSQL records. Interactive OAuth testing from local development requires an explicitly configured trusted HTTPS API/SPA endpoint, AuthKit and an Intuit sandbox app; do not weaken callback TLS checks for convenience. A tunnel is not provisioned automatically.

## Connect and confirm

Open a client close → **QuickBooks sandbox ledger evidence**. Confirm the intended client and choose **Connect QuickBooks sandbox**. Intuit's accounting OAuth scope is broader than read-only; this application exposes only fixed GET operations through the new connection. Review Intuit's consent screen accordingly.

After consent, the API validates the unexpired attempt and redirects to the original close with authorization data in the URL fragment. The app removes that fragment and keeps the response in component memory. Choose **Complete authorization** while signed in as the initiating user. The backend rechecks the firm/client/user/state, records a durable exchange claim, consumes the state once, and encrypts the returned tokens.

Inspect the returned sandbox company name and realm ID. Only then choose **Confirm this company belongs to the client**. A confirmed connection is shared across periods of that same case-insensitive client label within its organization. No client rename/merge capability is implied.

If the browser reloads before completion, authentication expires, consent is cancelled, or the ten-minute attempt expires, return to the client, disconnect the old attempt and start again. The app does not store authorization codes in local storage or infer approval from document content.

## Capture and inspect account records

Confirm **Capture account snapshot**. Expiring access tokens are refreshed through a durable claim; the latest rotated token is encrypted and saved before reads continue. The reader checks the confirmed company name, fetches active and inactive accounts, follows pages and saves only a complete bounded result. A failed page saves no snapshot. Read limits: 10,000 accounts, 2 MiB per provider response, 8 MiB combined accounts and 20 stored snapshots per close. Larger companies need a reviewed pagination/storage expansion.

The screen shows account count, company realm and capture time. Download the immutable JSON to inspect the account records. Provider decimal values remain JSON numbers without a floating-point conversion on the backend; snapshot hashes cover the canonical downloaded JSON. Third-party spreadsheet/JSON tools may round large numbers, so use decimal-aware tooling when inspecting financial values.

**An account-only capture is not a period-end ledger snapshot.** Account `CurrentBalance` values reflect the read time, pages are not transactionally consistent with each other, and no general-ledger transactions, trial balance or reports are fetched by the account-only action. The stored JSON explicitly records this scope and read interval. It is not automatically turned into preparation evidence, confirmed facts, booking proof or posting permission. Review packages include the captured JSON with its identity, digest and read times.

## Failure and recovery

- Failed reads show an error; previous snapshots retain their original capture times and are never relabeled fresh.
- A token exchange/refresh records `exchanging`/`refreshing` before Intuit is called. If the result is lost or persistence fails, reconnect. An interrupted process can leave that state visible; it is never blindly retried with an old refresh token.
- Company name changes require reconnection and explicit company confirmation. Disconnecting during a read prevents that read from saving a new snapshot.
- **Disconnect locally** removes stored tokens and stops new local use, while retaining snapshot history. Also revoke the app in Intuit to withdraw provider authorization. This application does not call the remote revocation endpoint yet.
- Losing `SECRETS_KEY` prevents decryption; restore the matching key. Do not substitute a new key and expect old ciphertext to work. Follow the [recovery runbook](operations.md).
- OAuth callback URLs carry short-lived codes: redact query strings in infrastructure access logs and disable analytics on callback paths. No application log intentionally records these values.

Live provider activation remains a separate, authorized sandbox exercise. Production OAuth, automatic refresh scheduling and OAuth-backed posting remain pending. Period report evidence is described below.

Primary protocol references: [Intuit OAuth client source](https://github.com/intuit/oauth-jsclient), [Intuit query builder](https://github.com/intuit/QuickBooks-V3-PHP-SDK/blob/master/src/QueryFilter/QueryMessage.php), [CompanyInfo entity identity](https://static.developer.intuit.com/sdkdocs/qbv3doc/ippdotnetdevkitv3/html/c3f4bf10-4ba7-c032-b2f0-db7deb1592f4.htm). CompanyInfo's object ID is distinct from the OAuth realm ID; access is addressed through the confirmed realm's API path.

## Period ledger evidence and coverage planning

Choose **Capture this month’s accrual reports and plan coverage** in the QuickBooks panel. Confirm accrual is appropriate for this client. The server takes the dates from the saved close month and reads `TransactionList`, `GeneralLedger`, `TrialBalance` and `ProfitAndLoss`, plus the account directory. Transaction List supplies reported transaction rows; this is not a raw transaction-entity synchronization. Cash-basis preparation, report-to-reconciliation CSV conversion and verified opening-balance rollover remain unsupported.

Report name, exact month bounds, accrual basis, currency, timestamp and report structure must be present and consistent. Empty reports need the provider's explicit empty-data flag. A failed report, invalid scope, indicated truncation or evidence bundle larger than 96 KiB saves nothing. This deliberately bounded first version preserves full report bodies without silently truncating them. Larger combined inventories use a separate, reviewable source-selection step when OpenAI is configured; without it, preparation reports a blocker. Reports are sequential reads, not an atomic ledger state or proof of completeness. No arithmetic, sign inference or balance normalization is performed on provider cells.

A successful capture atomically stores the snapshot and a named `.txt` JSON evidence bundle with its own content hash. Find the source in Evidence and the snapshot in QuickBooks. The bundle is included in ZIP exports through the existing evidence path. Each coverage proposal cites an account location; matching trial-balance rows also retain their JSON locations. Missing account identities in the trial balance are flagged for review. Bank/credit-card types suggest independent statement and ledger evidence; prepaid subtypes suggest invoice/schedule review; all other accounts require explicit accountant scoping. Inactive and zero-balance accounts are never silently excluded. Account-name text does not select accounting policy.

**Prepare this close** shows these deterministic coverage proposals without requiring AI credentials. They are proposed evidence needs, not an assertion that documents are absent or work is complete. Review the linked reports and current documents, upload the necessary supporting evidence, and use existing durable intake jobs. With OpenAI configured, those jobs inspect the permitted linked ledger evidence, select cited sources, ask specific questions and resume preparation after confirmed facts. Without OpenAI, existing local labeled-invoice proposals/manual preparation remain available; semantic report interpretation is not simulated. Reconciliation still requires two separate supported CSVs. Select unresolved requirements to draft one consolidated request, then approve its exact wording and recipient. Coverage resolves from the explicitly bound complete job set and current approved outputs; see [coverage requests and scanned attachments](coverage-requests.md). No request is approved or sent automatically.

A new capture supersedes earlier period evidence atomically, invalidates source-dependent review/posting approvals, and requeues pending OpenAI proposals. Uncertain sandbox posting attempts must be resolved first. The close-run scope digest includes ledger coverage, so a changed capture requires a fresh scope confirmation. Completed work is never silently overwritten; review source-changed drafts and recalculate using existing review controls. Historical snapshots and originals remain downloadable. A disconnected connection is labeled stale in coverage. Restore the connection and recapture before relying on it.

No migration, dependency or new cloud secret is required beyond migration 017 and the configuration above. Deploy the API and frontend together. Live sandbox validation remains pending; development integration tests intercept all Intuit HTTP calls.

Protocol references: [Intuit report service parameters](https://github.com/intuit/QuickBooks-V3-PHP-SDK/blob/master/src/ReportService/ReportService.php), [report header schema](https://github.com/intuit/QuickBooks-V3-PHP-SDK/blob/master/src/Data/IPPReportHeader.php).
