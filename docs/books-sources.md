# Books sources: files, more ledgers and connections

Written 2026-10-10. **Proposed, not yet adopted.** The [agent-first build plan](agent-first-build-plan.md) governs launch scope, and its proposed acceptance group is US clients on QuickBooks Online. This document proposes widening where a client's books can come from, and moving connections to the client. It takes effect only when the plan is amended to adopt it (see [Effect on the build plan](#effect-on-the-build-plan)).

## The problem

Every check that makes a close useful reads the month's books: the opening baseline, the tie-out, readiness and sign-off, the balance exceptions, month-over-month changes, account coverage, the management pack, budget against actual and the cash plan's starting cash. Today those books come only from a QuickBooks Online or Xero capture.

A client on anything else (QuickBooks Desktop, Sage, NetSuite, Business Central, MYOB, Tally, Zoho Books, FreshBooks, Wave) can upload exports and label them ([historical exports](validation.md#historical-exports-labeled-as-reported-2026-09-28)), but a label is not a Trial Balance. None of those checks run, and the brief keeps asking for the exports to be verified. The same is true of a QuickBooks Online client whose owner will not grant the firm a connection.

A firm moves its practice only when it can bring all of its clients. A product that serves half of them, however well, stays a side tool. The aim is that **every client gets the whole close, whatever ledger they keep and however the firm reaches it.**

## Principles

1. **Take the books the way the client has them.** Any ledger, any export layout, CSV, Excel or PDF. No fixed template the firm must reshape files into; the product learns the layout.
2. **Do the most with what is given.** A month-end Trial Balance alone runs the close. Each further file (more months, ledger detail, a cash-flow statement) unlocks more, and the page says what each would add. Nothing is refused because something else is missing.
3. **Files are first-class.** A month on files can be signed off, published and carried forward like a month on a live ledger. Its source is named plainly everywhere ("Books from files, exported 3 October by …"), never shown as lesser.
4. **Stop only for correctness, never for format.** A layout the product does not recognise is a mapping question, answered once. What stops sign-off is accounting: a Trial Balance that does not balance, accounts with no classification, a month that does not roll forward. Even then the import is kept, the work goes on and the exact fix is named.
5. **Whoever has the file can provide it.** A member uploads it, the client sends it through a request link, it arrives by forwarded email, or it is picked from Drive.
6. **Live where available, never required.** A live connection is preferred when there is one, because it refreshes itself and can be written back to. It is not a precondition for any check.
7. **Exact lineage.** The original file is kept byte for byte with its digest, every reading is reproducible from it, and a later upload replaces an earlier one with a reason, as evidence does today.

## What a client can bring

| File | Needed | What it unlocks |
| --- | --- | --- |
| Trial Balance at the month-end | Yes, the only one | Opening baseline, tie-out, readiness and sign-off, wrong-side, holding-account and opening-equity exceptions, month-over-month changes, account coverage, the management pack's Profit and Loss, Balance Sheet and working capital, budget against actual, the cash plan's starting cash |
| Trial Balances for earlier months (several files, or one file with a column per month) | No | Prior-period comparisons and trends from the first month, and the takeover's history without reopening old months |
| General Ledger detail for the month | No | Filling a reconciliation's ledger side, schedule discovery, drilling from a changed balance to its transactions, the month's transaction review in Daily Books, payee causes in Changes from last month |
| Chart of accounts export | No | A better first guess at each account's classification |
| Statement of cash flows | No | The management pack's cash flow, through the firm-attested import Xero months already use (`parseCashFlow` in `transport/management.go`); otherwise an accountant-approved derivation from two months' balances, when one is approved |
| Receivables and payables aging | Later | Open items (B10) for clients without a live ledger |

**Formats.** CSV; Excel, any sheet, with the workbook itself kept (today only the first sheet is kept, as CSV); PDF, read page by page with the existing extraction and confirmed row by row, as bank-statement PDFs already are (`transport/statement.go`). Common layouts are recognised without a question: QuickBooks Desktop, QuickBooks Online and Xero exports, Sage 50, Sage Intacct, NetSuite, Business Central, MYOB, Tally, Zoho Books, FreshBooks and Wave.

**Size.** At least 3,000 accounts and 100,000 ledger lines a month, which covers the clients the plan targets with room to spare. Beyond that, the message says how to split the file.

## How an upload becomes the month's books

1. **Upload.** From the client's Books card, the month, a client request, forwarded email or Drive. A file whose client or month is not obvious is asked about once.
2. **Read the layout.** A recognised layout is parsed directly. Otherwise the agent proposes which rows are the header and which columns hold the account number, name, debit, credit or signed balance, and the as-of date and currency where the file states them. A person confirms. The confirmed layout is saved for the client and reused the next month without a question, and changes only when the file's shape does.
3. **Map the accounts.** Each account needs a class (asset, liability, equity, revenue, expense) and, where the checks use one, a type (bank, credit card, receivables, payables, prepaid, fixed asset, accumulated depreciation, loan, payroll liability, sales tax and so on). This is what QuickBooks' account directory gives a capture today. The agent proposes from names, numbers, a chart-of-accounts export and the client's earlier months; a person confirms the map once. It carries forward. A new account in a later month is the only thing asked again.
4. **Check.** The checks below run on every import.
5. **Confirm.** A person confirms what the files are: the client's books for the month, exported from the named system on the named date, complete or partial, and with or without the final adjustments. This replaces the historical-exports label for these files; the attestation is part of what a sign-off covers.
6. **The month reads it.** The import becomes the month's books with source *files*, and every check above runs on it.

A replacement upload is a new version with a reason. Like a new capture, it marks drafts and sign-offs bound to the earlier books as stale. Approvals whose figures are unchanged survive, as the plan requires.

### Checks

Each check either holds sign-off and publication, or is a note. None refuses the upload.

| Check | Holds sign-off | Why |
| --- | --- | --- |
| Debits equal credits to the cent | Yes | An export that does not balance is missing rows or rounded |
| The as-of date is the month-end | Yes, for the month's books | A Trial Balance at another date can still be kept as history |
| Every account has a class | Yes | Unclassified accounts already make the management pack incomplete |
| Each account once; amounts in the client's currency with two decimals | Yes | The existing currency rule |
| With ledger detail: last month's closing balance plus the month's lines equals this month's balance, for every account | Yes, per account that differs | A difference usually means the prior month changed after it was closed. That finding is reported as such, with the accounts and amounts: it is useful in its own right |
| Profit and loss accounts reset at the fiscal year start | Note | Month activity is this month's year-to-date balance less last month's, except in the first month of the fiscal year (from the client profile) |
| Basis (accrual or cash) | Note | Recorded as stated. Methods that assume accrual say so when the books are cash basis |
| The prior month's approved entries appear | Note | See [posting](#writing-back) |

The first month on files goes through the existing opening baseline review, exactly as a first captured month does.

## What differs from a live ledger

| | Live ledger (QuickBooks, Xero) | Files |
| --- | --- | --- |
| Freshness | Captured on demand and daily | As current as the last upload; the export date shows wherever the books do. Re-uploading is allowed at any time, including mid-month |
| Changes after capture | Change hints and the daily capture | A re-upload. When approvals need fresher books, the agent drafts a client request for the new export |
| Writing back | Approved entries post through the governed path | Approved entries download as a journal import file (below) |
| Native reconciliation flags | Xero's are counted | None |

### Writing back

Approved entries for a month on files come out as a journal import file in the client ledger's own import format where one exists (QuickBooks Desktop IIF, Xero, Sage, MYOB and NetSuite journal CSV), and a plain journal CSV otherwise. The client's bookkeeper imports it. The next month's Trial Balance is the readback: the entries either appear or are named as missing. This is the plan's "clearly recorded external completion and evidence check" for a provider without the native operation.

## Architecture

### One month-books record

Today about 28 backend files read `quickbooks_snapshots` and 23 read `xero_snapshots`, each with its own branch. A third branch for files in every one would make each new source cost more than the last.

Instead, every source writes **one month-books record**, and the checks read only that:

- **`books_snapshots`**: close, source (`quickbooks`, `xero`, `files`), the source's own record (a capture or an import), as-of date, currency, basis, the account list (id, number, name, class, type, subtype), the month-end balances (net, debit minus credit), the month's activity for profit-and-loss accounts, a digest, who confirmed it and when.
- **`books_lines`**: the month's ledger lines when the source has them, keyed to the snapshot.
- **`books_imports`**, **`books_layouts`** and **`books_account_maps`**: the uploaded files and their readings, the client's confirmed column layout, and the client's confirmed account map with its history.

The shape already exists in miniature. `periodBooks` (`transport/flux.go`) returns a close's newest month-end capture from either provider, and `readLedgerMonth` turns it into the management pack's `books` type (`transport/management.go`): currency, accounts with class and type, balances and activity. Month-over-month changes, budget against actual and the management pack already read through that seam. `ledger.AccountBalance` is the per-account form the shared exception checks take. The month-books record is that shape, stored.

`periodBooks` picks whichever capture is newer, QuickBooks or Xero. With the month-books record the month's source is the one chosen for the client (see [Connections](#connections)), not the most recent capture.

QuickBooks and Xero captures keep their raw payloads and also write a month-books record when they capture. Provider-specific work stays provider-specific: posting, bill recodes, change hints, entity syncs and native reconciliation flags.

The demonstration seed writes QuickBooks-shaped captures from generated reports. A client's files must not be written that way: it would record a file as a QuickBooks read.

### Moving the readers

Readers move one at a time, each PR leaving the product working:

1. Those that need only balances, starting with the `periodBooks` seam (the management pack, month-over-month changes, budget against actual), then the tie-out and opening baseline, readiness, exceptions, coverage and the cash plan's starting cash.
2. Those that need ledger lines: reconciliation ledger fill, schedule discovery (`preparation/straight.go`, the recurring paths), Daily Books and search.
3. Status lines: Home, Portfolio, the morning brief, the coordinator, the plan and the close package.

## Connections

### Today

- **Per client, within one firm.** A connection row is keyed by firm and client name (QuickBooks also by sandbox or production), so all of a client's months share it; each month keeps its own captures (`017_quickbooks_connections.sql`, `048_xero.sql`).
- **The source is implicit.** A client could have both a QuickBooks and a Xero connection; the month-end checks read whichever capture is newer.
- **One Krith app per provider.** Firms never bring their own keys. Each client company still authorises separately.
- **Who:** a member whose session carries `integrations:manage`, signing in with their own QuickBooks or Xero login.
- **Where:** only inside a month. The month's Evidence tab has the QuickBooks, Xero, historical exports and Stripe panels; the production QuickBooks authorization, which is what actually becomes the client's books, is on the month's Settings tab. There is no client page: Clients leads straight to a month.

### Proposed

- **One books source per client, chosen explicitly:** QuickBooks, Xero or Files (more ledgers later). Each month records which source it read. Switching is a decision with a reason that applies from a month onward; earlier months keep theirs. Months can mix: a takeover can bring a year of history as files and run the current month live.
- **The client's Books card** holds the source, its state, the last capture or upload and its date, reconnect, upload, the saved layout and the account map. It lives with the client, not in a month. Until a client page exists it opens from the client's row in Clients; every month's header shows the source and its freshness and links to it. The QuickBooks, Xero and historical-exports panels leave the month's Evidence tab, and the production authorization leaves the month's Settings tab.
- **Firm Settings does not hold connections**, as already decided for Settings. Home's attention lists clients whose connection needs reconnecting and months still waiting for their books.
- **Who connects or uploads:**
  - a member with `integrations:manage` connects with their own login, as today;
  - any member with access to the client uploads files;
  - **the client can do either**, through a client request: "Connect your books" or "Upload this month's Trial Balance". A client-initiated connection is bound to that request's client, and a member still confirms that the company belongs to the client (the existing confirmation step). Few competing products offer this; it matters for owners who will not hand the firm an accountant login.
- **Many clients at once:**
  - a Xero sign-in can authorise several organisations; each is assigned to a client in one step;
  - a folder of Trial Balances for many clients and months can be dropped at once; the agent sorts them by client and month and a person confirms the sorting.
  - QuickBooks always authorises one company at a time, by Intuit's design.

### Provider terms that shape this

These come from vendor pages and third-party reports gathered on 2026-10-10 (sources below). Each needs checking against the provider's own terms before it is relied on.

- **QuickBooks reads are metered** under Intuit's App Partner Program (from July 2025): a free tier of 500,000 read credits a month, then paid tiers from $300 a month. The daily capture of every connected client is a recurring cost; measure calls per capture before adding many clients.
- **QuickBooks may allow one active connection per company per app.** If so, two firms serving one company through Krith's single app, or a client moving firms, would replace one another's connection. Verify with Intuit.
- **Xero prices by connected organisations** (from March 2026): free for 5, about A$35 a month for 50, A$245 for 1,000, A$1,445 for 10,000. Ledger lines through `/Journals` and the Bulk Connections picker need the top tier. The current capture does not use `/Journals`; it reads the Trial Balance report and transaction endpoints (`xero/xero.go`).

## More ledgers

Files cover every ledger from the first release. Native connectors add refresh and writing back where enough clients need them.

| Ledger | Where it matters | Programmatic access | Recommendation |
| --- | --- | --- | --- |
| QuickBooks Desktop | Still large in the US: Intuit's Desktop ecosystem earned about $2.8B in FY2025. New sales of Pro, Premier and Mac Plus to US subscribers stopped in September 2024; Enterprise is still sold | No cloud API; the Web Connector runs on the client's Windows PC. Conductor sells a REST layer over it | Files first; then evaluate Conductor rather than building the Web Connector |
| Sage Intacct | US mid-market; supported by FloQast, Numeric and Truewind | REST API (generally available February 2025) with a Trial Balance object. Each company authorises our sender ID; partnership about $2,500 a year plus per-call fees | First native connector after files |
| NetSuite | Mid-market | No Trial Balance endpoint (summed from transaction lines with SuiteQL); each customer sets up access | Files; native later if demand |
| Business Central | Mid-market; vendor counts put it above NetSuite | The cleanest API: Trial Balance, ledger entries, journal posting; consent per customer | Files; native when a firm brings several |
| MYOB, Tally, Zoho Books, FreshBooks, Wave, Sage 50 | MYOB in Australia, Tally in India; the rest a long tail | Varies; Tally is local only; Wave's API reportedly needs a paid plan | Files |

**Unified accounting APIs** (Merge, Rutter, Apideck, Unified.to, FIS's Railz; Codat has moved to banks): most show no true Trial Balance, cost about $25–65 per linked client a month, and still need our own Intuit and Xero apps and fees. They are not worth it for the core ledgers. One may suit read-only long-tail ledgers later, once files show which ones clients use.

**What others do.** FloQast and Fathom take a Trial Balance file from any ledger: FloQast maps each export layout, Fathom requires its own template. Truewind asks for twelve months of ledger detail for ledgers it does not connect. Double (formerly Keeper) has an "unconnected client". Numeric connects only NetSuite, QuickBooks Online, Xero and Intacct. Taking any layout without a template, and running the whole close on it, would put Krith ahead of all of these.

Reliable US market-share figures for these ledgers were not found; the counts above are vendor-reported.

## Delivery

Each PR leaves the product working and is reviewed and validated as usual.

1. **Month-books record.** `books_snapshots` and `books_lines`; QuickBooks and Xero captures write them; the management pack and tie-out read them. No visible change.
2. **Trial Balance from files.** Upload, layout reading and confirmation, the account map, the checks and the attestation; the month runs on files through the opening baseline, tie-out, exceptions, comparisons, coverage, sign-off and the management pack.
3. **The client's Books card.** Connections and uploads move out of the month; source switching; Home's waiting and reconnect lines.
4. **Client-provided books.** "Connect your books" and "Upload this month's Trial Balance" client requests.
5. **History and bulk.** Multi-month files, a folder for many clients, PDF Trial Balances.
6. **Ledger detail.** Ledger-line import; reconciliation fill, schedule discovery, drill-down and Daily Books on files.
7. **Writing back by file.** Journal import files and readback on the next upload.
8. **Native connectors.** QuickBooks Desktop through Conductor, after an evaluation; then Sage Intacct.

## Acceptance

- **Files agree with the live ledger.** For a sandbox QuickBooks month and a Xero demo month, the same month's exports, uploaded as files, give the same month-books record to the cent and the same tie-out, exceptions and management pack.
- **Real exports read without a template.** At least QuickBooks Desktop, Sage 50, NetSuite and a spreadsheet the firm built by hand, each read, mapped and confirmed, then read again the next month with no question.
- **Two consecutive months on files** for one client, through sign-off and publication, with the second month's opening rolling from the first and the first month's approved entries confirmed in the second's Trial Balance.
- **A prior month changed after close** is reported with its accounts and amounts.
- **A client's own upload and connection** through a request, confirmed by a member.
- The plan's partner-signup gates apply to file-based months as to live ones.

## Effect on the build plan

Adopting this amends the plan:

- **Proposed acceptance group**: US single-entity service businesses on QuickBooks Online, **or any ledger whose month-end Trial Balance the firm can export**.
- **B01 Takeover**: history may come as files; the opening baseline ties to the uploaded Trial Balance.
- **B03 and B06**: writing back to a ledger without the native operation is a journal import file with readback on the next upload.
- **B07**: a month on files publishes like any other, with its source named.
- **Shared experience**: connections and books uploads live on the client, not in a month.

## Open decisions

1. Adopt files as a first-class books source for launch, or after it.
2. Whether a client may connect or upload their own books through a request link from the first release, or later.
3. A client page, or a Books panel opened from the Clients list until one exists.
4. Whether to evaluate Conductor for QuickBooks Desktop now, and when to start the Sage Intacct partnership.

## Sources

Gathered 2026-10-10; vendor pages may change.

- Intuit FY2025 10-K: https://www.sec.gov/Archives/edgar/data/896878/000089687825000035/intu-20250731.htm
- QuickBooks Desktop stop-sell: https://quickbooks.intuit.com/r/whats-new/quickbooks-desktop-stop-sell/
- Intuit API limits: https://developer.intuit.com/app/developer/qbo/docs/learn/limits-and-throttles
- Intuit App Partner Program pricing, as reported: https://www.apideck.com/blog/quickbooks-api-pricing-and-the-intuit-app-partner-program
- One connection per company per app, as reported: https://help.quickbase.com/docs/connect-to-quickbooks-online
- Xero API pricing and tiers: https://developer.xero.com/pricing and https://developer.xero.com/faq/pricing-and-policy-updates
- Xero investor deck H1 FY26: https://brandfolder.xero.com/NE531UQB/as/ks8qbt4tj62mb8j6hpq6mz/Investor_Deck_H1_FY26_Xero
- Sage Intacct Trial Balance API: https://developer.intacct.com/api/general-ledger/trial-balances/
- Business Central Trial Balance API: https://learn.microsoft.com/en-us/dynamics365/business-central/dev-itpro/api-reference/v2.0/api/dynamics_trialbalance_get
- QuickBooks Desktop integration options: https://conductor.is/blog/quickbooks-desktop-sdk-vs-web-connector-vs-qbxml
- FloQast Trial Balance upload: https://help.floqast.com/hc/en-us/articles/360002052972-TB-Upload
- Fathom Excel import: https://support.fathomhq.com/importing-data/financials/importing-from-excel
- Truewind: https://www.truewind.ai/blog/truewind-vs-basis-which-fits-your-team
- Double (formerly Keeper) onboarding: https://help.doublehq.com/en/articles/6415571-new-client-onboarding
- Numeric: https://www.numeric.io/solutions/close-management-software
- Apideck pricing: https://www.apideck.com/pricing.md
