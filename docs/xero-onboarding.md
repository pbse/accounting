# Xero month-end Trial Balance

Connect a client's Xero organisation from its close, choose the exact organisation, and capture the Trial Balance at month-end as evidence. The only writes are an approved draft's manual journal and a reviewed bill line recode, each under a client grant, after a person approves it and a person executes it (below).

## Xero app

1. Create a web app in the Xero developer portal. Apps created after 2 March 2026 use granular scopes; this connector asks for `offline_access accounting.settings.read accounting.reports.trialbalance.read accounting.manualjournals accounting.invoices accounting.payments.read accounting.banktransactions.read`. Manual journals are available on every Xero plan; Xero's Advanced tier is only needed for the separate Journals read endpoint, which is not used. Connections made before migration 060 lack the journal scope: reconnect before posting. Connections made before transaction reading lack the invoice, payment and bank transaction scopes: their captures still save the Trial Balance and say to reconnect. Connections made before bill corrections hold `accounting.invoices.read`: they keep reading invoices, and reconnect before correcting a bill.
2. Register the exact HTTPS callback `https://<api host>/v1/xero/callback`.
3. Set `XERO_CLIENT_ID`, `XERO_CLIENT_SECRET` and `XERO_REDIRECT_URI` on the API and the worker (the worker makes the scheduled month-end captures), plus the existing `PUBLIC_APP_URL` and `SECRETS_KEY`. For hosted deploys set `XERO_CLIENT_ID` and `XERO_REDIRECT_URI` as repository variables and `XERO_CLIENT_SECRET` directly in Cloudflare using `node scripts/cloudflare-credentials.mjs XERO_CLIENT_SECRET staging < private-file`. Apply migration 048 after 047.
4. Optional change hints: in the app's Webhooks settings, set the delivery URL `https://<api host>/v1/webhooks/xero`, subscribe to Invoices and Credit notes, and set the webhook key as the server-only secret `XERO_WEBHOOK_KEY` (API only; migration 067). Xero's intent-to-receive check then passes: a correctly signed notification gets 200, a wrongly signed one 401.

Xero limits uncertified apps to a small number of connected organisations. Check your app's tier before onboarding many clients.

## Connecting

A signed-in firm user with `integrations:manage` opens the close, checks the authorisation box and selects **Connect Xero**. After Xero sign-in the browser returns to the close; **Finish connecting Xero** exchanges the code under that same user. The organisation list shows only organisations authorised in this sign-in (Xero also returns earlier consents, which are excluded) and only organisations, not practices. The accountant always picks the organisation explicitly, even when there is one.

The connection belongs to the firm and client, so later months reuse it. Access tokens last 30 minutes; each refresh returns a new refresh token and retires the old one, so the new one is saved before the Trial Balance is read. Credentials are encrypted with `SECRETS_KEY` and covered by the key-rotation command. Disconnecting clears the stored credential; also remove the app under Connected Apps in Xero to revoke it there.

## Capture

**Capture month-end Trial Balance** reads the organisation, the chart of accounts and `Reports/TrialBalance` dated the last day of the close's month, checks that Xero returned that report for that date, and stores the exact JSON with a SHA-256 digest. The close shows each account joined to the chart by Xero account ID, with Xero's own columns: Debit and Credit, and YTD Debit and YTD Credit. For asset, liability and equity accounts YTD is the balance at that date; for revenue and expense accounts it is the movement since the start of the financial year. The reads are sequential, not an atomic copy of the ledger.

The worker also captures it: every ten minutes it looks for open current- and prior-month closes (not signed off, enabled firm) with an active Xero connection and no Xero snapshot from the past day. It captures up to 10 of them as `scheduled-xero-refresh`, through the same token rotation as the button. Each close is claimed before the capture, so it gets at most one attempt a day, whichever worker makes it and however it ends, unless a change hint (below) makes it due again after 30 minutes. A failed attempt records the connection error and waits for the next day, a manual capture or a later change hint. Confirming an organisation, including after reconnecting, counts as a change hint, so a lapsed token no longer holds the client's open closes back for the rest of the day. The morning brief counts these captures.

With `XERO_WEBHOOK_KEY` set, `POST /v1/webhooks/xero` checks each notification's `x-xero-signature` (base64 HMAC-SHA256 of the raw body) and records the newest invoice or credit note change time on that organisation's connections; contact and other events are acknowledged and ignored, and a change time in the future is ignored. It makes no Xero call. A close whose active connection changed after its last capture and last scheduled attempt is then captured by the next sweep, even inside the daily window, but at most once every 30 minutes, however many notifications arrive. A replayed or late notification never moves the change time back. Webhooks cover only invoices and credit notes; the daily capture still picks up bank transactions, manual journals and other changes. A notification is a hint, not proof that the capture is complete. Without the key the endpoint returns 503 and the daily capture continues as before.

Deleting the close that opened a connection waits until the connection is disconnected and no other close holds a snapshot from it.

## Transactions

When the connection has the read scopes, each capture (the button and the daily one) also reads the close month's authorised and paid invoices and bills, credit notes, authorised bank transactions, posted manual journals and authorised payments, plus every authorised invoice, bill and credit note dated on or before month-end, and the authorised overpayments and prepayments, which are still open at capture time. Each type is read 1,000 at a time to its end, up to 5,000 records, and the count Xero reports must match what was read; a record changed during the capture asks for a new one. Reads are sequential, not an atomic copy: a record deleted and another added while paging can be missed. The raw records are kept in the snapshot, up to 12 MiB. A person's capture leaves the transactions out, with the reason, when they cannot be read in the time left, and the daily capture has three minutes. The Trial Balance saves either way, and the Xero panel says how many transactions were read or why none were.

Each capture also saves those reshaped rows (bills and invoices, credit notes, spend and receive money, transfers, manual journals and payments) for [search](chat.md#search), keeping only the newest capture's for the close, so search never scans the stored payloads. The close's exceptions, schedule discovery, Daily Books matching, and the aging and statements then read the newest Xero capture for the close's month, when it is newer than the close's latest QuickBooks transaction capture. The records are reshaped into the same form as QuickBooks records: bills and invoices, supplier and customer credit notes, spend money as purchases, receive money as deposits, transfers, manual journals, and invoice and bill payments. Refunds, other payment types, and overpayments and prepayments made through the bank are kept but not checked; their unused credits are read as open items. Line amounts are net of tax, and a line without an account counts as an item line, so schedule discovery says it is incomplete. Prepayment accounts ask for a prepaid schedule and fixed asset accounts for a depreciation schedule; Xero has no deferred revenue account type, so that check stays QuickBooks-only. Open overpayments and prepayments from customers count as unapplied payments, and those to suppliers as supplier credits. When the newest capture has no transactions, each of these says why instead of showing an older capture's.

## Exceptions

Each snapshot also records the organisation's base currency and financial year, which set the client's draft currency and year-end handoff the same way QuickBooks does. Until the first snapshot, drafts are held to USD.

The close's exceptions use the most recently captured month-end snapshot, QuickBooks or Xero, to list accounts on the wrong side of their normal balance and suspense, clearing, undeposited-funds or uncategorized accounts that still hold a balance (clearing only on balance-sheet accounts). A nonzero Opening Balance Equity (QuickBooks' opening-balance subtype, or an account of that name in either ledger) is its own cleanup item (B03), checked before the skips that depend on class or wording: it is usually zero once opening balances are settled, so the item says its cause is unverified, asks for the opening entries and the prior accountant's closing trial balance, and leaves the treatment (correcting a wrong opening balance or placing supported opening equity, with an approved date for a closed year) to the accountant. Nothing is changed. Each holding balance asks for reconciliation rather than a correction; for QuickBooks the item opens that account's ledger rows for the month, which can be added to agent evidence. Xero has no contra-account subtype, so accounts named "accumulated" are skipped, along with retained earnings and owner drawings; revenue and expense accounts are judged on their year-to-date movement.

## Account coverage

A close whose books are read from Xero (its Xero capture is newer than any QuickBooks one) asks the same coverage questions a QuickBooks close does. Every account in the month's capture needs a decision before sign-off: bound to the work that supports it, or excluded with a reason. The kinds are:
- `BANK` accounts (bank and credit card): reconciliation;
- `PREPAYMENT`: a prepaid schedule;
- a `CURRLIAB` account named as an accrual: an accrual;
- anything else: the accountant's review.

How it works:
- **Proposals are made from the capture when read**, so earlier captures have them too.
- **Decisions name the capture.** Coverage decisions, client requests about accounts and account hand-offs name the Xero capture they were made on (`xero:<id>`). A newer capture replaces them, as a new QuickBooks capture does.
- **Takeover.** Takeover setup proposes the account scope from the Xero capture of the month.
- **Sign-offs.** Since coverage counts toward sign-off, a Xero close signed off before account coverage existed shows **reopen required**, until its accounts are decided and it is signed off again.

## Changes and client memory

**Changes from last month** uses each close's newest snapshot, QuickBooks or Xero. With two Xero months from the same organisation, it compares each revenue and expense account's movement in the month, read from the Trial Balance's Debit and Credit columns (Xero reports these for the current month up to the report date). Payees come from the transactions each capture read: each contact's lines on the account, net of tax and signed as the Profit and Loss shows them, with journal lines unattributed. When either month's capture has no transactions, causes say why; an account with lines in another currency is not explained from them, because the capture does not hold their base-currency amounts; and a line without an account keeps the missing-payee question from being asked. A QuickBooks month is never compared with a Xero month. When confirming a document, client memory checks proposed accounts against the newest snapshot's chart of accounts, including Xero's (an account is usable only while Xero marks it active).

The **Reports** tab's management pack has no Xero cash-flow statement to read, since Xero's API has none: the firm imports its own as CSV, checked against the capture's bank accounts (see [management reports](management-reports.md)).

## Approved journal posting

This keeps the same human steps and refusals as production QuickBooks posting (migration 060).

0. **Firm setting.** Every Xero organisation counts as a client's live books, so nothing is posted until a firm administrator turns on **Writing to clients' books** in Settings → Policies ([production posting](production-posting.md)). It starts off.
1. **Grant.** Someone with `posting:approve` grants posting for the client under **Xero posting grant**. The grant lists 2–20 Xero account IDs (taken from the Trial Balance rows), the draft kinds it covers, a limit per journal and an expiry within 30 days. Type `GRANT XERO POSTING` to grant it or `REVOKE XERO POSTING` to revoke it.
2. **Enable kinds.** **Connector operations** (Settings → Security → Advanced) shows each kind's status and the clients it is enabled for; the demo-company check is not recorded in the app. Each kind stays off until the operator lists it in `XERO_POSTING_KINDS` (for example `accrual,loan`), after checking Xero's behavior for it.
3. **Preview.** **Reviewed Xero posting** on a draft reads Xero live and refuses unless all of these hold:
   - each entry account matches exactly one active account the grant lists;
   - the entries share one date, at the close month-end, after the organisation's period and end-of-year lock dates;
   - the total is within the grant limit;
   - the draft's currency is the organisation's base currency;
   - no newer draft of the same source exists;
   - the token carries the journal scope.

   The digest covers the journal and the journal accounts' Trial Balance at that date. A changed balance, account, lock date or grant refuses approval and posting.
4. **Approve.** `APPROVE XERO DRAFT` binds a person's approval for ten minutes. Approving the posting of a pending draft also approves the draft, for someone who can review drafts. The accounting-basis and independent-review rules apply.
5. **Post.** A person with `posting:execute` types `POST TO XERO`. The API locks the close, the grant and the connection, and rechecks that no newer draft of the source exists. While another Xero intent on the same accounts is approved or unsettled, this one can't be approved or posted; refresh its preview after the other settles. It refuses if the draft or its source was already attempted in QuickBooks, and QuickBooks refuses the reverse. It saves one attempt, then creates one POSTED manual journal (lines by account ID, no tax) with the saved request ID as Xero's `Idempotency-Key`, and reads the journal back.
6. **Check the result.** A matching readback is verified. Any doubt, including a lost answer, a different readback or a rejection, leaves the attempt unknown (`EXECUTION_UNKNOWN`). It is never posted again: find the journal in Xero and verify it by its ManualJournalID, also after reconnecting the same organisation. The readback also compares the narration and tax treatment. Xero keeps an idempotency key for only a few minutes, so the saved attempt, not the key, prevents a second journal.

Balances after posting are not re-checked; the readback compares the journal's lines, date and status.

An executing or unknown Xero journal blocks sign-off, a signed-off close takes no Xero journal decision until it is reopened, and the close package carries each Xero attempt and its readback as a receipt. A close with Xero journals has their receipts in its signed snapshot, so one that changes after sign-off asks for the close to be reopened (a close signed off before this change with Xero journals asks once).

## Bill corrections

**Recode a Xero bill line** (Xero panel, migration 068) moves one line of one bill to another expense account, the Xero counterpart of the QuickBooks Bill recode. It needs the client's Xero grant to cover **bill_correction** and list both the line's current and new accounts, the line's amount within the grant's maximum, `bill_correction` in `XERO_POSTING_KINDS`, and a connection with the `accounting.invoices` scope. It stays off until the operator has checked, on a Xero demo company, that a multi-line approved bill keeps its line IDs, amounts, status, dates and other accounts through the update.

1. **Preview.** Choose a bill from the close's latest Xero capture: an approved (AUTHORISED) bill dated in the close month with nothing paid or credited, no tax and up to ten lines. The live bill must equal the captured one on every compared field (contact, dates, number, reference, currency and rate, totals and amounts due, paid and credited, and each line's ID, description, quantity, unit amount, item, account ID and code, tax, amount, tracking and discount). The chosen line must have no item and a positive amount, and post to a granted, active expense account; the new account must be a different granted, active expense account with a code. The bill must be dated after Xero's lock dates. The accountant's reason and cited rule are saved and bound into what is approved.
2. **Approve.** A person with `posting:approve` approves for ten minutes. The firm's independence rule applies: whoever previewed the correction cannot approve it when the rule covers the line's amount.
3. **Update once.** A person with `posting:execute` confirms. Every check is repeated from live reads; under the close lock the close must be open, the independence rule met, the grant current (its grantor still a member) and the connection active, and one attempt is reserved. The bill is updated with every line as read, only the chosen line's account ID and code changed, with the saved request ID as Xero's `Idempotency-Key`.
4. **Read back.** The bill must read back identical except for that line's account. Anything else, or a lost answer, leaves the attempt unknown; it is never sent again. **Read back from Xero** settles it through any active connection to the same organisation, also after the grant ends or the close is signed off.

An executing or unknown correction blocks sign-off and close deletion and is listed in operations; a verified one asks, before sign-off, for a Xero capture that began reading after the first verification (a capture saved later but read earlier does not count; with webhooks set up, the invoice change hint brings one). The close package carries every attempt and readback.

## Not included

No complete-journal sync (complete Journals need Xero's Advanced tier), no corrections to sales invoices, credit notes, bank transactions (Xero refuses edits to reconciled ones) or tax-bearing bills, and no cash-basis reports. Agent-prepared recurring comparisons stay QuickBooks-only. Xero prohibits using its API data to train AI or ML models; this connector sends none of it to a model.
