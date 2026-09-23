# Supported prepaid and accrued-expense methods

These are explicit product methods, not universal accounting policy. The client's accounting professional decides whether each is appropriate, including materiality, credit treatment, prior-period treatment and reversals. Agents propose sourced facts; they cannot approve methods, invent estimates or post journals. All amounts are bounded nonnegative USD inputs with at most two decimal places. Calculations use integer cents; cash basis and foreign currencies remain unsupported.

## Start locally or on Cloudflare

Apply all migrations in numeric order with API and preparation worker stopped, then restart both on the same code revision. Fresh Compose volumes apply all migrations. No additional service or secret is required for these calculations. Existing hosted AuthKit organization/client permissions and database setup still apply. Cloudflare deployment must migrate the database before starting the new containers; API readiness checks for the required schema. This slice has local validation, not hosted activation evidence.

Open a client close. Upload terms and ledger/credit support, or use the synthetic examples in the preparation forms. Example buttons fill facts but never check approval boxes. Examples use February 2028; use a synthetic close for that month to save them. Avoid mixing synthetic examples into real client evidence.

The accountant can publish the method, conditions, materiality and supporting acceptance reference in **Approved procedures and client overrides**. Apply that reviewed version explicitly to the close. It is cited context, not automatic authorization. For each job, review proposed facts and selected sources, select the method, inspect the examples and confirm the booking assumptions. The durable job calculates a **pending draft**. The authorized accountant reviews the linked sources, amounts, assumptions and proposed adjustment, then records an approval/rejection with a reason in the existing review history. Editing a source invalidates approval; recalculation creates a fresh draft. A preparation checkbox is not final accountant sign-off.

## Partial-month prepaids

Choose one method explicitly:

- `equal_full_months_v1`: existing equal full-calendar-month allocation. Service begins on the first and ends on the last day of a month.
- `actual_days_v1`: invoice amount divided by the actual number of service days, including both endpoints. Each calendar month's amount uses its overlapping service days. Leap days count. Each monthly amount rounds down to cents; the final service month receives the residual.

Both are limited to 120 calendar months, with the close inside the service period. They do not infer taxes, useful lives or policy from descriptions.

Synthetic checks to review:

| Input | Expected expense |
| --- | --- |
| $100; September 1–November 30, 2026; equal months | September $33.33, October $33.33, November $33.34 |
| $100; January 16–March 15, 2028; actual days | January $26.66 (16 days), February $48.33 (29 days), March $25.01 (15 days) |
| $12,000; one service day in September 2026 | September $12,000, ending prepaid zero |

In the daily example, February's closing prepaid is $25.01. Without a supplied booked balance, the preparer must confirm the invoice was fully expensed with no prior entries or credits; the draft reclassifies that remaining balance from expense to prepaid.

## Credits and prior adjustments

The first supported credit treatment is **one prospective credit at the start of a selected service month** (or the service start if later). Supply a positive credit amount, effective month, credit/treatment reference and the current booked prepaid debit balance after the credit entry. The effective month must be inside the service period and no later than the close.

Earlier scheduled expense remains unchanged. Subtract the credit from the unamortized balance at that boundary, then allocate the remaining amount over the remaining months/days using the chosen method. A credit exceeding that balance is blocked. The schedule shows the credit separately; total scheduled expense plus credit equals the original invoice amount.

Example: apply a $10 credit effective February to the $100 daily example. January remains $26.66. The remaining $73.34 becomes $63.34 over 44 days: February $41.74, March $21.60. Total expense is $90.

For any prior adjustments, enter the prepaid balance actually booked through close and its ledger/cutoff reference. It is the current **debit balance**, not a proposed entry amount. The target minus booked balance determines the adjustment:

- February target $21.60, booked $30: debit expense / credit prepaid $8.40.
- Same target, booked $10: debit prepaid / credit expense $11.60.
- Same target, booked $21.60: no entry.

For multiple or retrospective credits, enter up to 20 individual positive credits, each with amount, effective service month, booked month and source reference. The booked month must be no earlier than the effective month and no later than this close. Cite the **client accountant's approved rule** and confirm the prepaid debit balance after all credit entries. Credits effective in an earlier service month revise the analytical schedule from that month; the calculator proposes only the current-close difference from the confirmed booked balance. It never edits an earlier close or posts a prior-period journal. The reviewer must resolve whether that treatment is acceptable before approving. Example: $120 January–March with $5 effective January but booked February and $10 effective/booked February yields $38.33, $33.33 and $33.34 of revised expense; total expense $105. At February close a booked prepaid balance of $40 yields a $6.66 debit expense / credit prepaid proposal. Excess credits, negative balances, combined credits/cancellation, mid-month cancellation and tax/foreign-currency effects remain blocked.

Confirm that net invoice cost is recorded between the selected expense/prepaid accounts and that the balance includes all prior entries. The calculator does not independently read or certify the ledger. The separate month-boundary cancellation treatments below do not change earlier expense.

## Month-boundary cancellation

The accountant chooses one of two explicit treatments for the effective close month. Both require a cancellation source, the current booked prepaid debit balance and ledger/cutoff reference. For a full refund, supply the balance after the refund entry; for no refund, supply the balance before any proposed write-off (or zero if already written off). They cannot be combined with a credit in one workpaper. Mid-month cancellation and corrections to earlier closes require separate review.

- **Full refund of the unamortized balance:** The schedule shows the remaining balance as a refund in the effective month and zero expense afterward. The refund must already be booked against prepaid, so the confirmed booked prepaid balance must be zero. This calculator does not create a cash or receivable entry.
- **No refund:** The schedule expenses the unamortized balance in the effective month and shows zero prepaid afterward. The booked balance must equal that entire unamortized balance (draft debit expense / credit prepaid) or be zero after an already-booked write-off. Partial prior write-offs need separate review; the calculator will not guess their period or treatment.

Synthetic example: $100 allocated equally over January–March 2028 produces January expense $33.33. A February full refund shows $66.67 refunded, February expense $0 and prepaid $0. A February cancellation without refund shows February expense $66.67 and prepaid $0; if the booked prepaid debit balance is $66.67, the proposed write-off is $66.67. Both examples require client-accountant approval before use.

## Fixed incurred accruals

`fixed_incurred_amount_v1` prepares a supported **closing accrued liability**, not an automatic estimate from a missing invoice. Supply service start/end, completed by close month-end; the evidence/approved estimation basis; target liability; current booked liability credit balance (explicit zero is allowed); ledger reference; and distinct expense/liability accounts.

Select whether to propose a reversal on the first of the following month. It reverses **only the proposed adjustment**, not the entire target liability or prior entries. Reversals are draft entries, not scheduled execution.

Example: supported liability $1,200, booked $200 → debit expense / credit liability $1,000 at month-end. Optional next-month reversal: debit liability / credit expense $1,000. Booked $1,400 → correct downward by $200. Booked $1,200 → no adjustment or reversal.

`actual_invoice_true_up_v1` links a prior estimate amount/reference to a distinct actual invoice reference and the client accountant's approved rule. The entered amount is the approved **current closing liability**, supported by the actual invoice; the entered booked balance already reflects invoices, payments, reversals and earlier adjustments through this close. The workpaper reports actual minus estimate as a variance. Only current target minus booked liability becomes a proposed adjustment, and optional next-month reversal applies only to that new adjustment. Example: prior estimate $1,200, actual closing liability $1,250, booked liability $1,200 → variance +$50 and debit expense / credit liability $50. If the booked liability is already $1,250, no entry or reversal is proposed. This does not automatically derecognize paid invoices or revise an earlier period.

The local agent recognizes exact labeled facts. To propose an accrual locally, put `Preparation kind: accrual` on its own line in the primary source, followed by labels such as `Amount:`, `Service start:`, `Service end:`, `Currency:`, `Expense account:`, `Liability account:`, `Booked balance:`, `Booking reference:` and `Estimation basis:`. This classifies a proposal only. OpenAI interpretation supports the same fields with verified citations when configured. Neither path accepts approval flags from document text; conflicting candidates remain visible for review.

## Review, carry-forward and posting boundaries

Saved drafts retain method, example confirmation, credit/balance references, selected evidence revisions and any reversal proposal. The ZIP package includes their inputs and calculation outputs; prepaid CSV adds a credit column. Source changes invalidate reviews through the same existing path.

Reviewed prepaid schedules can carry forward as schedules only. They retain source lineage, clear confirmations and never claim a fresh ledger balance or propose a carried journal. A reviewed fixed recurring accrual can start the next month's draft only after the accountant selects current service evidence, confirms the exact current QuickBooks Trial Balance liability row and its booked balance, and reconfirms the approved rule and reversal choice. The prior workpaper remains linked; a changed prior review reopens dependent review. The product does not execute accrual reversals. New daily/credit/balance-adjusted/cancellation prepaid methods and all accruals are draft/review only; the existing full-month sandbox journal path is not extended to them.

For a reviewed supplier credit in the current close, a separate **VendorCredit** preview maps one cited source and exact 3–21 character credit reference to a QuickBooks sandbox vendor, A/P account and prepaid asset account. The accountant confirms the $25 synthetic example (debit A/P, credit prepaid asset), verifies the vendor name and that the credit has not already been booked or included in the booked prepaid balance, reviews the exact preview, approves it for ten minutes and executes once. The app checks QuickBooks for an existing VendorCredit with that document number and blocks another local attempt for the same reference; credits entered under another number still need accountant reconciliation. After posting, refresh the ledger and workpaper before sign-off. An uncertain provider result blocks retry; use canonical readback and investigate any mismatch. This operation is sandbox-only and requires a hosted role with separate posting approval/execution permissions. It does not cover cash refunds, tax, prior-period corrections or production VendorCredit posting.

## Focused checks

```sh
go -C services/backend test ./internal/prepaids ./internal/accruals ./internal/preparation
TEST_DATABASE_URL="$DATABASE_URL" go -C services/backend test ./internal/transport -run TestAccountingMethodsDurableReviewAndSourceInvalidation -count=1
```

Use only a migrated local test database, with other workers stopped for integration checks. Fixtures are synthetic and cleaned up. Accountant acceptance of the chosen policy and a hosted end-to-end pilot remain separate from passing software tests.
