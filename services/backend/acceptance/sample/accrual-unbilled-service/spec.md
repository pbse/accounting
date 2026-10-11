# Accrual: service received, not yet invoiced

**Treatment.** Fixed incurred accrual (`fixed_incurred_v1`). Harbor Cleaning confirms September's cleaning was completed and will invoice in October. The cost belongs to September, so it is accrued at 2026-09-30 for the agreed monthly fee, reversed when October's invoice is booked.

**Expected values, derived by hand.**
- Amount: the agreed monthly fee, **850.00** USD, for one full month of service (2026-09-01 to 2026-09-30).
- Entry at 2026-09-30, once a person names the accounts and confirms nothing is booked yet: debit cleaning expense **850.00**, credit accrued liabilities **850.00**.

**What the app must do.** Read the amount, currency and service dates exactly, then ask a person for the expense and liability accounts and the booked balance; it never drafts this alone.
