# Payroll: provider summary, ledger page not supplied

**Treatment.** Payroll recording from the provider's summary (`payroll_summary_v1`): the provider's bank debits are booked to a clearing account; the payroll is recorded at month-end against it.

**Expected values, derived by hand.**
- The summary ties: gross 10,000.00 − employee taxes 1,500.00 − deductions 500.00 = net pay **8,000.00**.
- Total cost: gross 10,000.00 + employer taxes 765.00 + contributions 0.00 = **10,765.00**.
- Provider debits: net 8,000.00 + employee taxes 1,500.00 + employer taxes 765.00 = **10,265.00**, as printed.
- Not paid through the provider: 10,765.00 − 10,265.00 = **500.00** (the deductions, owed onward).
- Entries at 2026-09-30, once the clearing account's ledger page shows 10,265.00 debited: debit wages 10,000.00 / credit clearing; debit payroll tax expense 765.00 / credit clearing; debit clearing 500.00 / credit payroll liabilities.

**What the app must do.** Read every figure exactly, then ask for the clearing account's ledger page; the reviewer later confirms the summary is final and not booked.
