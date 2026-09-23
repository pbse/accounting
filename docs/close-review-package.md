# Close review package

Open a saved client close and select **Download draft review package**. Once an accountant has signed off the defined scope and readiness has no blockers, select **Download signed accountant binder**. A changed source, job, review, scope or posting state invalidates release until the close is reopened and signed again. Both downloads use saved-work authentication and one consistent database snapshot.

The ZIP contains:

- `review.html`: offline, printable summary with source references, methods, unresolved blockers, stored sandbox receipts and remaining manual review steps. Use your browser's Print / Save as PDF command for a PDF summary.
- `manifest.json`: close identity, period, database snapshot timestamp, scope, every saved workpaper version and stored sandbox posting record.
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
