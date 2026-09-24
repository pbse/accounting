# Close review package

Open a saved client close and select **Download draft review package**. Once an accountant has signed off the defined scope and readiness has no blockers, select **Download signed accountant binder**. A changed source, job, review, scope or posting state invalidates release until the close is reopened and signed again. Both downloads use saved-work authentication and one consistent database snapshot.

The ZIP contains:

- `review.html`: offline, printable summary with source references, methods, unresolved blockers, stored posting receipts and remaining manual review steps.
- `workpapers.pdf`: portable PDF of saved workpaper inputs, calculations, review IDs and source links. The embedded font may substitute unsupported scripts; JSON retains the exact text. It is a snapshot, not an approval by itself.
- `manifest.json`: close identity, period, database snapshot timestamp, scope, every saved workpaper version and stored posting record, including sandbox VendorCredit attempts.
- `workpapers/{id}.json`: full inputs and calculated results for each draft.
- CSV files for prepaid schedules, reconciliation match candidates and unmatched transactions.
- `evidence/{id}.txt` or `.pdf`, with extracted PDF text alongside: exact uploaded originals, names and hashes in the manifest. Workpaper source links retain role and location.
- `ledger/snapshot-{id}.json`: every captured QuickBooks account/report payload, with snapshot ID, digest, company and capture time in the manifest. These are sequential reads, not a fresh or atomic ledger verification.
- Current workpaper review states, append-only review decisions, and source replacement relationships/reasons in the manifest. Workpaper review is separate from firm close sign-off.
- `checksums.sha256`: hashes of every other file in the archive. Verify the downloaded files before handoff.

After extracting the ZIP, run `shasum -a 256 -c checksums.sha256` from the extracted directory. A mismatch means the package must not be released.

Authenticated callers can use `GET /v1/closes/{id}/export` for the draft archive or `GET /v1/closes/{id}/binder` for a release-gated signed archive. These are read-only and make no provider calls. They are ordinary scoped API routes, not an MCP service. Records come from one repeatable-read database snapshot; the timestamp does not establish source or ledger freshness.

Every saved version is included, including versions beyond the dashboard's 100-row display limit. Alternative drafts are not additive. Export rejects closes exceeding 1,000 workpapers, 16 MiB of workpaper JSON, 1,000 request drafts, 20 ledger captures or 16 MiB of ledger JSON, or 100 source files / 40 MiB of source and extracted content instead of silently truncating them. No partial archive is sent if generation fails.

The draft package is not firm sign-off. The signed binder proves that the app recorded a current sign-off of the stated supported scope, not that the whole ledger is complete or audited. Source references are user-supplied; uploaded originals and stored capture payloads are attached. Stored sandbox and production statuses are historical receipts, not fresh provider verification. The accountant must still inspect source completeness, scope and external steps before releasing it.

## Year-end handoff

Select **Download year-end handoff** on any close. It covers that client's closes from the first month of its fiscal year through the close's month, as read from the connected QuickBooks or Xero company (calendar year when none is connected; a conflict between them blocks the download) (`GET /v1/closes/{id}/year-end`), read from one consistent database snapshot with no provider calls.

The ZIP contains:

- `handoff.html`: printable summary with each month's sign-off state and every open question.
- `months.csv`: each month's close, sign-off state, decision ID and blockers. A month with no close is listed.
- `prepaids.csv` and `loans.csv`: monthly roll-forwards from approved schedules. A prepaid is checked against the prior month's approved closing balance using its own saved schedule, so credits and cancellations are not breaks. A loan's opening principal is checked against the prior month's closing principal.
- `reconciliations.csv`: approved reconciled balances. Bank and card reconciliations with one account name stay separate.
- `adjustments.csv`: entries proposed by approved workpapers.
- `postings.csv`: every stored production posting record for the year, including one tied to an older workpaper version.
- `open-questions.csv`: missing months, months not signed off, blockers, unapproved latest versions, workpaper questions and roll-forward breaks.
- `workpapers/{month}/{kind}-{id}.json` and `checksums.sha256`.

Only the latest version of each workpaper is used (prepaid and accrual by source reference, loan by lender and loan account, owner item by transaction reference, reconciliation by account and account type). If that version is not approved, it becomes an open question; an older approved version is never substituted. The handoff is not an audit opinion or a tax return, and monthly evidence originals stay in each close's own package. Workpapers over 16 MiB for the year return 413 with nothing exported.
