# Controlled production QuickBooks posting

Production posting is a separate, manually activated capability. Ordinary local development needs no production credentials and never calls a production QuickBooks company. The supported writes are one USD journal per approved draft: the initial full-month **prepaid**, and, once enabled (below), an **accrual**'s month-end adjustment (never its next-month reversal), a **loan** schedule's month-end entries combined in one journal, and an answered **owner** item's reclassification. Blocked, question-only and other drafts are never posted.

## Setup

1. Back up PostgreSQL. Stop API and worker, apply `023_production_posting.sql` after 022, then deploy matching code. Fresh Compose volumes apply all migrations. The API readiness check requires the new tables.
2. Create an Intuit **production** app separately from the sandbox app. Register the exact HTTPS API callback `https://YOUR_API_HOST/v1/quickbooks/callback`. Set server-only `QBO_PRODUCTION_CLIENT_ID`, `QBO_PRODUCTION_CLIENT_SECRET`, `QBO_PRODUCTION_REDIRECT_URI`, `PUBLIC_APP_URL` and the existing `SECRETS_KEY`. Leave these blank until activation. GitHub staging accepts the ID and redirect as variables and the secret as a secret; it rejects partial configuration. A production release workflow and environment are not included in staging.
3. In WorkOS, give only authorized client accountants `workpapers:review` and `posting:approve`, and authorized human executors `posting:execute`. Grant those users the exact client via client access; `clients:all` is firm-wide access. A user connecting Intuit also needs `integrations:manage`. No agent receives these permissions. Verify roles and client assignments before connecting a live company.
4. Open the client close and **Production QuickBooks authorization and client grant**. Connect, complete OAuth, and confirm that the QuickBooks company name exactly equals the client name on the close. Names that differ are blocked; reconcile the identity before posting. The production OAuth connection and credentials are separate from sandbox evidence capture and sandbox posting.
5. With `posting:approve`, enter the production Chart of Accounts IDs the grant allows (2–20), the draft kinds it covers, a USD maximum per journal total, an expiry within 30 days and the reason. Review the exact company and client, then type `GRANT PRODUCTION POSTING`. Each proposal account name must match exactly one allowed account by name; otherwise preview refuses before any write. Grants made before migration 046 cover prepaid drafts only. A current grant can be revoked immediately.

## Per-workpaper flow

Approve the saved workpaper with current source evidence first. In its **Reviewed production posting** panel, prepare a preview. The API checks the supported method, client and close month, active company, exact account names/classifications/currency, positive amount and grant cap. It captures each account's exact `CurrentBalance` and `SyncToken` as ledger preconditions. These are **current** balances, not an atomic period-end Trial Balance. Inspect them against the client's ledger and other postings.

Type `APPROVE PRODUCTION DRAFT` to bind approval to the preview. It expires after ten minutes. Type `POST TO PRODUCTION` to execute. Immediately before the write, the API re-reads the company and accounts; any change in the observed preconditions, workpaper review, grant or source version blocks the write. It commits the unique intent/request ID and `executing` attempt before calling Intuit. It never automatically retries the write, including after a timeout or lost response.

After Intuit returns a journal ID, the API reads that journal by ID and verifies its date, currency, note, two account IDs, posting directions and exact amounts. It then re-reads both current account balances and requires the observed prepaid debit and expense credit effect. The saved receipt contains the journal readback and account observations. If an ID or balance readback is missing or differs, state remains `unknown`; use **Recheck saved journal and balances** after investigating QuickBooks. Report lag or concurrent ledger activity can leave this state unresolved even if the journal exists. No second write button appears. If no external ID was returned, investigate the displayed provider request ID directly in QuickBooks. Enter a found journal ID in the posting panel; the server binds it only after its canonical fields match the saved intent, then checks the account effect. A wrong ID never authorizes a second write. Reconnecting the same exact production company permits a later readback, including after a grant expires or is revoked. Once verified, a later journal identity recheck does not reinterpret balances changed by subsequent activity.

The close readiness digest and review ZIP include production attempt states and stored receipts. `executing` or `unknown` blocks sign-off. Export is a saved snapshot, not a new provider read.

The close page's **Close ledger rehearsal** lists saved, unexecuted production journal previews and combines their effects by account against one versioned balance. It requires the same QuickBooks company, connection, account ID, SyncToken and exact balance across every contributing intent. A different snapshot, more than 100 open proposals, or an uncertain execution blocks the combined result; workpapers needing accountant review remain identified. The preview is one read-only database snapshot, not a fresh QuickBooks query or an official trial balance. It excludes sandbox Bill/VendorCredit corrections and already verified postings. Refresh each individual preview before approval or posting; the server rechecks live preconditions at execution. Overlapping approved intents may still block individual posting even when their combined arithmetic can be rehearsed.

## Local and staged validation

Run `npm ci`, `npm run check --workspace @close/frontend`, and the focused Go tests under `services/backend`. CI applies every migration to PostgreSQL and runs the integration tests with synthetic credentials and mocked Intuit HTTP. Do not configure a real production token for tests. Before real activation, validate the Intuit app, WorkOS roles, account IDs, company identity, grant limits, book-close policy and provider permissions with the client's accounting professional. This code cannot make QuickBooks reads and writes atomic with other users' activity.

Intuit references: [QuickBooks API environment URLs](https://developer.intuit.com/app/developer/qbo/docs/api/accounting/all-entities/invoice), [idempotent request IDs](https://blogs.a.intuit.com/2018/09/10/quickbooks-online-api-best-practices/).

## Closing date and independent review

Every QuickBooks write (production and sandbox JournalEntry, sandbox VendorCredit and Bill correction) reads the company's `AccountingInfoPrefs.BookCloseDate` first and refuses a transaction dated on or before it, before any attempt is reserved. An unreadable or malformed closing date also blocks the write; an empty date means the company has no closing date. The production preview records the closing date, so a change between preview and execution rejects the stale intent without writing. Readback after a write does not recheck the closing date, so books closed after a successful write still verify.

Firms can require independent review (Firm settings, `org:manage`, hosted firms only; migration 040). With it on, nobody may approve a draft they prepared (created the preparation job, confirmed its intake, or saved it directly), drafts without a recorded preparer cannot be approved, and production posting approval and execution require that the current approval met the rule. Each policy change is recorded with its reason, actor and time. The shared local development workspace cannot enable the policy.

## Enabling accrual, loan, owner and Stripe fee posting

These kinds stay off until `PRODUCTION_POSTING_KINDS` lists them (default `prepaid`), for example `prepaid,accrual,loan,owner,settlement`. Turn a kind on only after observing QuickBooks behavior in the sandbox company:

1. Post a test journal in the sandbox that touches each account type the kind uses: Expense and Liability for accruals and loan interest, Liability-to-Liability for current portion, Equity or Asset against Expense for owner items, Expense against Revenue (or the deposit account) for Stripe fee entries.
2. Read each account's `CurrentBalance` before and after. Verification expects debit-normal types (Asset, Expense) to rise on debits, and credit-normal types (Liability, Equity, Revenue) to rise on credits.
3. If QuickBooks doesn't move a balance this way, don't enable the kind: every readback would fail and leave postings unverified.

Posting reads every allowed account, checks the company and closing date, builds one journal, records each touched account's versioned balance, and verifies the journal lines and every balance movement after the write. Approval, independent review, basis, one durable attempt and uncertain-outcome rules are unchanged.

