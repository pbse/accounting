# Sample acceptance corpus

Synthetic cases (a fictional client, invented documents) that keep the acceptance runner and its scoring working. They
are not the release corpus: that holds authorized, de-identified client months with an independent accountant's labels,
kept outside this repository. See [docs/acceptance-corpus.md](../../../../docs/acceptance-corpus.md).

| Case | What it checks |
| --- | --- |
| `straight-through-prepaid` | A clean invoice whose bill was booked to Insurance goes through without a person and drafts moving the 11 unexpired months, 1,100.00, to Prepaid Expenses; once the accountant approves it and defines the scope, the close is ready. |
| `reading-ten-times-too-large` | A reading of 12000.00 against the printed 1,200.00 is not accepted: a person is asked. |
| `billed-to-another-company` | An invoice billed to another company is held for a person. |
| `no-ledger-capture` | Without the month's ledger capture the agent cannot check the bill, so it asks a person. |
| `held-document-blocks-signoff` | With the clean invoice's draft approved and the scope defined, the held invoice still keeps the close from being ready. |
| `trial-balance-does-not-tie` | The same clean invoice, approved, but the month-end Trial Balance already holds 500.00 in Prepaid Expenses: with the draft it would not tie, so the close is not ready. |
| `same-invoice-uploaded-twice` | The same bytes uploaded again are one document: no second piece of work. |
| `accrual-unbilled-service` | A service received in September and invoiced in October: the agent reads the 850.00 fee and the service dates exactly, then asks a person for the accounts and the booked balance. |
| `payroll-summary-september` | A Gusto summary that ties (10,000.00 gross, 8,000.00 net, 10,265.00 debited): every figure read exactly, then the clearing account's ledger page asked for. |
| `loan-statement-september` | A lender statement (6.00%, 50,000.00 to 49,250.00): the rate and balances read exactly, then the agreement and the loan account's ledger page asked for. |

Each case has a `spec.md`: the treatment and how each expected value is derived by hand, for the independent accountant to confirm or correct in the review package (`cmd/acceptance -review`). Until confirmed, the labels are the builder's.
