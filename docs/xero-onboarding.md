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

Deleting the close that opened a connection waits until the connection is disconnected and no other close holds a snapshot from it.

## Not included

No writes, transaction or journal sync (complete Journals need Xero's Advanced tier), cash-basis reports, or checks that consume the snapshot yet. Xero prohibits using its API data to train AI or ML models; this connector sends none of it to a model.
