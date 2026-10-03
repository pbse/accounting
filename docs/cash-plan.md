# Cash plan

B09 in the [agent-first build plan](agent-first-build-plan.md). This page records what is built: the base 13-week plan and named what-if scenarios. Reviewed versions that changed inputs invalidate, and comparing a past plan with what happened, are still to come and are not claimed here.

## The base plan

A client close's **Cash plan** tab (`GET /v1/closes/{id}/cash-plan`, MCP `cash_plan`) lays the client's next 13 weeks of book cash out day by day, with no model call and no estimate of its own:

- **Opening book cash:** the home-currency bank accounts' balances in the close's newest QuickBooks capture, as of that capture. It is the ledger's cash, never called bank-available cash; accounts in another currency are left out and named. The plan starts on the capture's day.
- **Receipts:** the latest capture's open invoices, on the date a customer promised (recorded under [Follow up open invoices and bills](work-inbox.md#cash-operations-overdue-receivables-and-payables)) or otherwise the due date.
- **Payments:** open bills, on the date the client's bill-pay tool will pay (a recorded handoff) or otherwise the due date.
- **Schedules:** payroll, tax, debt service, rent and other commitments from the client's own schedules (`GET`/`POST /v1/closes/{id}/cash-obligations`, migration `109_cash_obligations.sql`): once, weekly, every two weeks or monthly (on the same day, or the month's last day). Each is kept in the client's home currency when it is added (one in another currency is left out and named), counts only after a person with `workpapers:review` approves it, and starts counting from the plan's first day (earlier dates are already in the opening balance); a change retires it with a reason and adds a new one. Nothing runs payroll or files tax. A payment schedule naming the vendor whose bills pay it is not counted in a week where an open bill from that vendor is due, even when the bill's bill-pay date moves its payment to another week, so the bill and the estimate are not both counted.

Each week shows its opening (last week's closing), receipts, payments, closing and its **lowest end-of-day balance** with the date, so a Wednesday payroll shortfall shows even when Thursday's receipts leave Friday positive. The lowest balance across the 13 weeks leads the page. Arithmetic is exact on decimal strings.

Receipts are only what is already invoiced, plus any approved receipt schedule (a schedule can be a receipt, for example a retainer or expected monthly sales): later weeks show what is already known, not a sales forecast, and the page says so. Nothing is given a date nobody agreed: a past-due invoice or bill without a promise or handoff, a disputed or on-hold item, one with no due date and one with an unreadable balance are listed apart with the reason, as are items after the 13 weeks and in another currency. The bank balances and the open items must come from the same QuickBooks company. A capture holds invoices and bills dated up to its close's month-end, so when the plan starts after that date it says later-dated items are missing and points to the latest month's close. The plan says when the latest capture did not read every open item, when the bank balances and the open items were read more than a day apart, and when the balances' capture is no longer current. A Xero client's plan is not built yet.

**Checks:** `TestPlanWeeksShowsAMidweekShortfallAndCarriesBalances` (hand-worked weeks, a midweek low, items outside the horizon) and `TestCashPlanFromTheLedgerOpenItemsAndApprovedObligations` (opening from the capture, a promised date, two-weekly payroll, rent covered by its bill, a disputed bill and a past-due invoice left unscheduled, an unapproved obligation not counted).

## What-if scenarios

Under **What if** on the plan, anyone who can open it (including a client's executive) can try a named scenario (`GET`/`POST /v1/closes/{id}/cash-scenarios`, migration `115_cash_scenarios.sql`): move an open invoice or bill to another date (a customer paying late, a bill paid early), or add a receipt or payment once or weekly, every two weeks or monthly up to an optional last date (a new hire's pay, a one-off purchase). `GET /v1/closes/{id}/cash-plan?scenario={id}` lays the weeks out with those changes and reports the plan's own lowest point beside the scenario's, so a delayed receipt shows exactly which day turns negative.

A scenario is kept with the QuickBooks books and home currency it was made for and is not applied to other books or after the home currency changes, since another company could give the same document key to a different invoice. A client's executive sees, opens and removes only their own scenarios; the firm's what-ifs (a draft salary, say) stay with the firm, while the firm sees every scenario of the client. A scenario that can no longer be shown falls back to the plan as it stands, with the reason.

A scenario changes only that view: never the base plan, a promise or bill-pay date recorded on an open item, an approved schedule or the ledger. It keeps the open items it moves by their document (`invoice:3`); one that has since been paid, disputed or left undated is named as not applied. Removing a scenario keeps its record.

**Checks:** `TestScenariosMoveOpenItemsAndAddPaymentsWithoutTouchingTheBase` (a moved copy, a two-weekly payment up to its end date, a move with nothing to move, refused changes) and the scenario part of `TestCashPlanFromTheLedgerOpenItemsAndApprovedObligations` (a delayed promised invoice and a hire through the API, the base plan unchanged, an executive seeing only their own scenario, a scenario for other books refused, a removed scenario no longer shown).

