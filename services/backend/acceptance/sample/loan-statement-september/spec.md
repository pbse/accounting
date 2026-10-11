# Loan: lender statement, agreement and ledger not supplied

**Treatment.** Loan schedule from the lender's statement (`lender_statement_v1`): interest is the period's charge on the opening principal; the principal paid reduces the balance.

**Expected values, derived by hand.**
- Interest for September at 6.00% a year on 50,000.00, 30/360: 50,000.00 × 0.06 × 30 ÷ 360 = **250.00**, as printed.
- Closing principal: 50,000.00 − principal paid 750.00 = **49,250.00**, as printed.
- Entries once the agreement and the loan account's ledger page are supplied: interest expense 250.00, and the loan account reconciled to 49,250.00.

**What the app must do.** Read the rate and the opening and closing principal exactly (the labeled facts), then ask for the loan agreement and the loan account's ledger page; it never drafts this alone. The loan reading holds no field for the month's interest charge or principal paid, so those two printed figures are not scored: the accountant checks them against the derivation above, and the closing principal ties them out.
