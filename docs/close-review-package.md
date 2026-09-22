# Close review package

Open a saved client close and select **Download close review package**. The download uses the same authentication as saved workpapers, including the configured backend URL.

The ZIP contains:

- `review.html`: offline, printable summary with source references, methods, unresolved blockers, stored sandbox receipts and remaining manual review steps. Use your browser's Print / Save as PDF command for a PDF summary.
- `manifest.json`: close identity, period, database snapshot timestamp, scope, every saved workpaper version and stored sandbox posting record.
- `workpapers/{id}.json`: full inputs and calculated results for each draft.
- CSV files for prepaid schedules, reconciliation match candidates and unmatched transactions.
- `evidence/{id}.txt`: exact uploaded text originals, with names and hashes in the manifest. Workpaper source links retain role and location.
- Current workpaper review states, append-only review decisions, and source replacement relationships/reasons in the manifest. Workpaper review is separate from firm close sign-off.

Agents can use authenticated `GET /v1/closes/{id}/export` to obtain the same archive. Export is read-only and makes no provider calls. Records come from one repeatable-read database snapshot; the timestamp does not establish source or ledger freshness.

Every saved version is included, including versions beyond the dashboard's 100-row display limit. Alternative drafts are not additive. Export rejects closes exceeding 1,000 workpapers or 16 MiB of workpaper JSON instead of silently truncating them. No partial archive is sent if generation fails.

This is a preparation package, not firm sign-off. Source references are user-supplied; only uploaded text originals are attached. Stored sandbox statuses are historical records, not fresh provider verification or production posting evidence. Direct PDF generation, PDF/image ingestion, approval collection and full PRD package certification remain outside this feature.
