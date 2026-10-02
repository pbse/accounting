# Management reports

B07 in the [agent-first build plan](agent-first-build-plan.md). This page records what is built; the historical cash-flow statement, publication, commentary, exports and executive access are still to come and are not claimed here.

## Management pack

A client close's **Reports** tab (`GET /v1/closes/{id}/management-pack`, MCP `management_pack`) prepares the month's management accounts from the close's newest capture, QuickBooks or Xero, whichever is newer: the same capture **Changes from last month** uses. It makes no model or provider call and estimates nothing. Everything is read from one database snapshot.

- **Profit and loss:** the month's Profit and Loss report by account. QuickBooks groups by account type (Income, Cost of Goods Sold, Expense, Other Income, Other Expense) with gross profit, operating profit and net income; Xero, whose captures carry only the class, shows income, expenses and net income.
- **Balance sheet:** the month-end Trial Balance by account type (QuickBooks) or class (Xero), on each class's normal side, with income and expense balances shown as one line, *earnings not yet closed to equity*. Total assets must equal liabilities plus equity; any difference is shown.
- **Working capital (QuickBooks):** book cash (the ledger's bank accounts, never called bank-available cash), receivables, payables, current assets and liabilities.
- **Cash flow:** not prepared yet. Balance changes alone cannot separate depreciation, assets bought on credit or borrowing from cash, so the pack says so instead of deriving one; book cash and its change are on the balance sheet. A historical cash-flow statement needs the ledger's own Statement of Cash Flows, checked against the real report in a sandbox, or an accountant-approved preparation method.

Each pack states its entity, period, basis (accrual), currency, capture time and source. Each month shown has a status: **sign-off stands** only while that close's sign-off stands over a QuickBooks capture, whose identity the signed readiness digest binds (a newer capture reopens it); a Xero capture is not bound by that digest, so it stays **provisional**, as does any unsigned month; **status unknown** when readiness cannot be read. A capture whose connection or evidence changed is marked not current, for either month. Last month's close supplies the comparison only when its newest capture is from the same ledger, company and currency; otherwise the pack says why there is no comparison. An account appears under this month's classification, or last month's when it has none this month; an account classified differently in the two months is listed. An unclassified account or a balance sheet that does not balance makes the pack **not complete**, with the amount shown. Approved drafts not yet posted are excluded.

QuickBooks account figures open their captured General Ledger rows for both months; totals are the sums of the accounts listed above them. Xero figures have no ledger-row reader yet. The agent's investigations can read the pack, as they read other close-scoped tools.

**Checks:** `TestManagementPackTiesStatementsToTheTrialBalances` works two balanced months by hand (statements, working capital, a prior-only expense, a reclassified account, an unclassified account and an untyped ledger). `TestManagementPackReadsTheCapturedReports` reads QuickBooks-shaped reports through the API and shows a sign-off covering its QuickBooks capture while a newer Xero capture stays provisional. Real-provider captures are not yet exercised.
