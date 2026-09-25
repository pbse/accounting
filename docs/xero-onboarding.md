# Xero month-end Trial Balance

Connect a client's Xero organisation from its close, choose the exact organisation, and capture the Trial Balance at month-end as read-only evidence. The connector never writes to Xero.

## Xero app

1. Create a web app in the Xero developer portal. Apps created after 2 March 2026 use granular scopes; this connector asks only for `offline_access accounting.settings.read accounting.reports.trialbalance.read`.
2. Register the exact HTTPS callback `https://<api host>/v1/xero/callback`.
3. Set `XERO_CLIENT_ID`, `XERO_CLIENT_SECRET` and `XERO_REDIRECT_URI` on the API only (the worker never calls Xero), plus the existing `PUBLIC_APP_URL` and `SECRETS_KEY`. For hosted deploys set `XERO_CLIENT_ID` and `XERO_REDIRECT_URI` as repository variables and `XERO_CLIENT_SECRET` as a secret. Apply migration 048 after 047.

Xero limits uncertified apps to a small number of connected organisations. Check your app's tier before onboarding many clients.

## Connecting

A signed-in firm user with `integrations:manage` opens the close, checks the authorisation box and selects **Connect Xero**. After Xero sign-in the browser returns to the close; **Finish connecting Xero** exchanges the code under that same user. The organisation list shows only organisations authorised in this sign-in (Xero also returns earlier consents, which are excluded) and only organisations, not practices. The accountant always picks the organisation explicitly, even when there is one.

The connection belongs to the firm and client, so later months reuse it. Access tokens last 30 minutes; each refresh returns a new refresh token and retires the old one, so the new one is saved before the Trial Balance is read. Credentials are encrypted with `SECRETS_KEY` and covered by the key-rotation command. Disconnecting clears the stored credential; also remove the app under Connected Apps in Xero to revoke it there.

## Capture

**Capture month-end Trial Balance** reads the organisation, the chart of accounts and `Reports/TrialBalance` dated the last day of the close's month, checks that Xero returned that report for that date, and stores the exact JSON with a SHA-256 digest. The close shows each account joined to the chart by Xero account ID, with Xero's own columns: Debit and Credit, and YTD Debit and YTD Credit. For asset, liability and equity accounts YTD is the balance at that date; for revenue and expense accounts it is the movement since the start of the financial year. The reads are sequential, not an atomic copy of the ledger.

The worker also captures it: every ten minutes it looks for open current- and prior-month closes (not signed off, enabled firm) with an active Xero connection and no Xero snapshot from the past day. It captures up to 10 of them as `scheduled-xero-refresh`, through the same token rotation as the button. Each close is claimed before the capture, so it gets at most one attempt a day, whichever worker makes it and however it ends. A failed attempt records the connection error and waits for the next day or a manual capture. The morning brief counts these captures.

Deleting the close that opened a connection waits until the connection is disconnected and no other close holds a snapshot from it.

## Exceptions

Each snapshot also records the organisation's base currency and financial year, which set the client's draft currency and year-end handoff the same way QuickBooks does. Until the first snapshot, drafts are held to USD.

The close's exceptions use the most recently captured month-end snapshot, QuickBooks or Xero, to list accounts on the wrong side of their normal balance and suspense, clearing, undeposited-funds or uncategorized accounts that still hold a balance (clearing only on balance-sheet accounts). Each holding balance asks for reconciliation rather than a correction; for QuickBooks the item opens that account's ledger rows for the month, which can be added to agent evidence. Xero has no contra-account subtype, so accounts named "accumulated" are skipped, along with retained earnings and owner drawings; revenue and expense accounts are judged on their year-to-date movement.

## Changes and client memory

**Changes from last month** uses each close's newest snapshot, QuickBooks or Xero. With two Xero months from the same organisation, it compares each revenue and expense account's movement in the month, read from the Trial Balance's Debit and Credit columns (Xero reports these for the current month up to the report date). Xero snapshots carry no payees, so causes are marked unavailable. A QuickBooks month is never compared with a Xero month. When confirming a document, client memory checks proposed accounts against the newest snapshot's chart of accounts, including Xero's (an account is usable only while Xero marks it active).

## Not included

No writes, transaction or journal sync (complete Journals need Xero's Advanced tier), cash-basis reports, or other checks on Xero data yet. Without transactions, schedule discovery, payee causes and agent-prepared recurring comparisons stay QuickBooks-only. Xero prohibits using its API data to train AI or ML models; this connector sends none of it to a model.
