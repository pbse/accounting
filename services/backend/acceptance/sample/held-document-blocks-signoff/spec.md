# Prepaid insurance, a held second invoice

**Treatment.** Prepaid expense, equal full months (`equal_full_months_v1`): an annual premium paid up front is expensed one twelfth a month over its coverage; what is unexpired at month-end is an asset.

**Expected values, derived by hand.**
- Amount: the annual premium, **1,200.00** USD; coverage 2026-09-01 to 2027-08-31, 12 full months.
- September's expense: 1,200.00 ÷ 12 = **100.00**. Unexpired at 2026-09-30: 1,200.00 − 100.00 = **1,100.00** (11 months).
- The bill (INV-778, Acme Insurance Co, 1,200.00) is booked entirely to Insurance, so the entry at 2026-09-30 moves the unexpired part: debit Prepaid Expenses **1,100.00**, credit Insurance **1,100.00**.

**The case.** Beside the clean invoice (drafted as above and approved), a second invoice is billed to another company and held (expected problem: `wrong_client`).

**What the app must do.** Keep the close from being ready while the held invoice is unresolved, even with the first draft approved and the scope defined.
