# Stripe settlement reconciliation

Stripe deposits a batch of charges, refunds, disputes and fees as one net payout, so the bank shows one number that matches no invoice. This workpaper traces each automatic payout to its bank deposit using Stripe's own batch relationship (PRD REC-04). The Stripe report can be uploaded as an export, or fetched from the client's Stripe account ([Live Stripe](#live-stripe)).

## Flow

1. In Stripe, open **Reports → Payout reconciliation**, select the close month, then under **Payout reconciliation** choose **Download → Itemized**. Use the full download, not a single category. Export the bank account's activity as `id,date,amount` (deposits positive), including the first four days of the next month.
2. Upload both CSVs to the client close. The Stripe file is recognized from its header without a model call, under either intake engine. The Work inbox shows **Recognized Stripe payout export** with the payout count and expected dates. A file with a problem, such as a non-USD row, a payout from another month, or a row where net ≠ gross − fee, is shown as unsupported with the exact reason.
3. Select **Trace payouts to bank deposits →**. The Stripe file is preselected, and the bank file is picked automatically when it's the only other CSV. Enter the bank account name, confirm the method and file coverage, and select **Trace payouts →**.
4. Review the result. Each payout shows its transactions, gross, fees, net and bank row:
   - **Candidate deposit**: exactly one bank row carries the payout's net amount from one day before to four days after Stripe's expected date, and no other payout competes for it.
   - **Choose a deposit**: equal amounts compete for the same rows. Pick the verified row and give a reason.
   - **No deposit found**: find the row, or mark the payout **in transit at month-end**, citing the accountant-approved rule and your evidence. In transit is refused while a possible bank row exists.
5. **Confirm linked facts and continue this job** queues the durable preparation job using the uploaded evidence. Saved drafts appear on the close as **Stripe settlement**. Approval, sign-off, the review package and the binder use the existing flows. The package adds `workpapers/{id}-payouts.csv` and `-categories.csv`.

Without a close, **Trace Stripe payouts** in the navigation previews local files or a synthetic example. It saves nothing.

## Accounting boundary

- Payouts come only from Stripe's `automatic_payout_id`. Rows without one, such as manual or instant payouts, are listed and block approval. They're never assigned to a payout by amount.
- Matching uses exact cents. A candidate is a lead until an accountant approves the workpaper. A deposit dated after month-end is labeled as such.
- Categories outside charges, refunds, disputes and fees are shown for review. The totals still tie.
- By default no journal is proposed. When the client books Stripe deposits net of fees and their accountant's rule says to record fees as an expense, choose **Stripe fee entry**: name the fee expense account, the account the net deposits were booked to, the fees already booked this month (zero explicitly) and the rule reference. Only payouts matched or reviewed to a bank deposit dated within the close month count; their fees are the per-transaction fees plus Stripe fees charged as their own `fee` rows. Fees of payouts in transit or deposited outside the month are named and left for the month their deposit is booked; fees of payouts not yet traced are named separately. The month-end draft entry debits fees and credits the deposit account for the difference from what is booked (reversed if more was booked) and appears only when every payout is traced. The review package states the entry and rule, and an approved entry is part of the year-end adjustment history. Revenue, refund and dispute entries are not proposed.
- Once the settlement is approved, its fee entry can post to the client's production QuickBooks company under a production grant that covers **Stripe fee entries** (`settlement` in `PRODUCTION_POSTING_KINDS`), with the same preview, approval and readback as other entries. A newer draft of the same Stripe report supersedes it.
- Out of scope: Stripe's Balance or ending-balance reports, non-USD, Connect platforms, and Stripe MCP access.

## Live Stripe

Instead of downloading and uploading the report, **Stripe payout reconciliation** on the close's Evidence tab fetches it (migration 061).

1. **Create a key.** In Stripe, create a *restricted* key with read access to reports and account details and write access to report runs. Stripe needs that permission to generate a report; it changes no financial data. Secret keys (`sk_…`) are refused, so the app never holds full account access.
2. **Connect.** Someone with `integrations:manage` pastes the key. The API checks it can read the payout reconciliation report and reads the Stripe account's ID and name. The key is sealed with `SECRETS_KEY` and covered by the key-rotation command.
3. **Confirm.** A person confirms the account belongs to the client before any report is requested.
4. **Capture.** **Capture this month's payout reconciliation** asks Stripe for the close month's `payout_reconciliation.itemized.5` report, with UTC month boundaries and exactly the parser's columns. It is refused until Stripe's data covers the whole month, or if the key now belongs to another account.
   - The request and its idempotency key are saved first. A lost answer is asked again with the same key, so Stripe returns the same report, and a repeated click reuses the month's report in progress.
5. **Save.** Every minute the worker checks reports in progress. It downloads a finished report only from `files.stripe.com` (no redirects, bounded size), checks it fits the evidence limits (512 KiB, 5,000 transactions) and has the parser's columns, then saves it as evidence named "Stripe payout reconciliation YYYY-MM.csv". It starts the same intake job as an upload, so the report is recognized and traced exactly like an uploaded export.
   - A report Stripe cannot produce, one that is too large, or one missing columns fails with the reason. Download that one from Stripe in parts and upload it.
6. **Disconnect.** Disconnecting clears the key and ends reports still in progress. Deleting the close that opened the connection waits until it is disconnected.

Set `SECRETS_KEY` on the API and the worker. Manual and instant payouts and revenue or refund entries remain out of scope.

## Setup and recovery

Apply `037_settlements.sql` with the API and worker stopped, then restart both. `/readyz` reports not ready until it's applied. There's no new dependency, secret or service.

If a file is rejected, fix it at the source and re-upload. Replace the evidence if a draft already used it; dependent drafts then show evidence changed. Re-tracing is deterministic. Reviewer choices live in the draft's input and are reset when files change.

Checks: `go test ./internal/reconciliation -run 'Settlement|StripeReport'` (pure). With `TEST_DATABASE_URL`: `go test ./internal/transport -run TestStripeExport` (recognition under both engines, the evidence-bound job and the export).
