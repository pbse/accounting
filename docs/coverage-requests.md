# Coverage, consolidated requests and scanned client attachments

## Required setup and local development

Apply migration `018_coverage_requests_attachments.sql` after 001–017 and use matching API/worker revisions. Fresh Compose volumes apply all migrations. Client attachment release now requires [Cloudflare WAF upload scanning setup](cloudflare-upload-scanning.md): entitlement, proxied custom hostname, blocking rules, managed result header, and a private edge-to-origin key. ClamAV and its extra memory allocation have been removed from preparation; TXT/CSV use a separate on-demand scanner.

Local TXT/CSV scanning uses the private Compose scanner service and a configured UPLOAD_SCAN_KEY. Client PDFs cannot be released without a trusted Cloudflare scan. Synthetic receipt tests verify application behavior without claiming live malware detection. Use the linked guide for actual staging acceptance and scan retries. `/readyz` verifies database schema, not Cloudflare configuration.

## Define coverage once; follow resolution automatically

Capture the month's QuickBooks period evidence, then open **Prepare this close → Coverage and client evidence**. Each account starts unresolved. Expand an account, select its complete required job set, record a rationale and confirm the scope. Selecting a job is an accountant's explicit coverage mapping; matching names or the presence of one invoice cannot establish completeness. Up to 20 jobs can be bound per requirement. Unsupported work must be explicitly excluded from this supported preparation scope and tracked as an external close task with a reference.

The server derives status from current jobs and their drafts. Every bound job must be completed, have prepared output, and have current approved drafts of the expected supported kind, without calculation blockers and with linked sources. Only then is the requirement resolved. A changed source or withdrawn approval reopens it automatically. Ledger recapture starts a new coverage scope; prior mappings and exclusions remain in history and need fresh confirmation. A disconnected/stale ledger prevents new mappings and requests.

Coverage does not authorize accounting methods or posting. Existing fact, method, review and sign-off gates still apply. All ledger requirements must be resolved or explicitly excluded before close sign-off. Quarantined attachments also block sign-off until released or explicitly rejected. Changes to decisions and attachment dispositions invalidate the signed snapshot. Archives contain coverage bindings, target membership and scan/decision metadata; quarantined bytes are never exported as evidence.

## Consolidate missing information

Select up to 20 unresolved requirements, choose **Draft selected questions**, then edit the exact request. Review existing evidence first: suggested questions describe evidence needs, not proof that documents are absent. Confirm the exact recipient and that the answer may be shared with every job bound to those requirements. Approval creates one immutable request and an expiring private link; it does not send email. Share this consolidated link manually, or expand its email approval under Approved consolidated requests. The [Resend guide](email-delivery.md) covers consolidated delivery, signed tracking and individually approved reminders.

The request records its ledger capture, coverage binding versions and exact job revisions. New evidence, a changed mapping, a cancelled job or a new ledger capture prevents stale replies from being applied. An unresolved requirement can be requested before any job exists. Once the documents are released, new intake jobs appear for the accountant to include in its required work set.

A requirement cannot have overlapping open consolidated links. Withdraw the prior request before replacing it. Withdrawal revokes its links and returns still-matching pending jobs to review; it cannot reset a newer request's work. Link replacement never stores readable tokens, and replies cannot choose a different client, account or job. A link holder's identity is unverified. Do not put tokens in logs, tickets or analytics. Request text and filenames are untrusted data.

## Client submission and quarantine

The client sees approved wording and the client/month only. They can submit one original text reply plus up to five distinct attachments: UTF-8 TXT/CSV up to 512 KiB or PDF up to 5 MiB each, at most 20 MiB per submission. TXT/CSV use the separate antivirus service; PDFs use Cloudflare. One request-link submission is consumed atomically; the exact reply plus filename/content hashes identifies a retry. A changed retry is rejected. Keep the original file selection after an uncertain network response and retry unchanged. Per-close quarantine is bounded at 100 files / 20 MiB, in addition to the existing evidence storage limit.

The text reply becomes scoped evidence and wakes only explicitly targeted jobs. Attachments are stored separately; no OCR, model call, raw download or evidence export can access them before a clean automated scan. The browser sends each original file separately through its scanner, and the server matches its hash to the quarantined attachment in the same request. Accepted receipts mark it `clean`; missing, blocked or failed scans leave it quarantined. There is no polling scan worker or firm-side scan override. A clean result is malware screening, not proof of accounting correctness or absence of every threat.

## Review, release and resume

A recent clean scan permits an authenticated firm user to download the file. An authorized reviewer must confirm client relevance and record a release/rejection reason. Release requires a clean scan within 24 hours over the exact bytes; there is no manual override for failed or detected files. For a missing or stale scan, ask the client to reopen their unexpired link and use **Retry attachment scanning** with the same original file. Fix Cloudflare configuration before retrying if scanning is unavailable. Detected files can be rejected, not overridden. Ask the client for a fresh safe document through a new request.

Release creates or reuses immutable evidence, starts its existing durable intake job, links it to the original request, and resumes unfinished affected jobs. PDF extraction uses the existing bounded OCR path; its completion wakes waiting affected proposals again. Model interpretation remains untrusted and must pass the existing citation/fact/method gates. Local labeled-invoice proposals still work without OpenAI. Select the new jobs in the account's confirmed required work set; receiving documents alone never approves the scope.

Finished, cancelled or actively preparing target jobs prevent automatic release into that request; review a new request/scope instead. A newer ledger capture also blocks release against the old request. Rejected/quarantined bytes are retained with audit metadata: automatic deletion remains disabled pending an approved retention policy.

## Operational verification boundaries

Local PostgreSQL checks and synthetic Cloudflare trust-boundary checks are recorded in [validation](validation.md). Hosted WAF activation, blocking rules, live malware detection and container deployment remain unverified until the [activation checks](cloudflare-upload-scanning.md#usage-and-activation-checks) pass. No local bypass exists.
