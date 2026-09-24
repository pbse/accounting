# Stripe settlement reconciliation

Stripe deposits a batch of charges, refunds, disputes and fees as one net payout, so the bank shows one number that matches no invoice. This workpaper traces each automatic payout to its bank deposit using Stripe's own batch relationship (PRD REC-04), from exports only. No Stripe API access or credentials are needed.

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
- No journal is proposed. Recording gross revenue, fees and refunds follows the client's approved method; a fee or revenue entry is a separate future slice with its own accountant rule.
- Out of scope: Stripe's Balance or ending-balance reports, non-USD, Connect platforms, and live Stripe API or MCP access.

## Setup and recovery

Apply `037_settlements.sql` with the API and worker stopped, then restart both. `/readyz` reports not ready until it's applied. There's no new dependency, secret or service.

If a file is rejected, fix it at the source and re-upload. Replace the evidence if a draft already used it; dependent drafts then show evidence changed. Re-tracing is deterministic. Reviewer choices live in the draft's input and are reset when files change.

Checks: `go test ./internal/reconciliation -run 'Settlement|StripeReport'` (pure). With `TEST_DATABASE_URL`: `go test ./internal/transport -run TestStripeExport` (recognition under both engines, the evidence-bound job and the export).
