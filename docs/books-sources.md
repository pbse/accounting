# Books sources: files, more ledgers and connections

Written 2026-10-10. **Adopted 2026-10-10 as a track that does not gate launch** (see [decisions](#decisions-2026-10-10)). The [agent-first build plan](agent-first-build-plan.md) governs launch scope; its proposed acceptance group stays US, single-entity, USD service businesses on QuickBooks Online. This document widens where a client's books can come from, gives each client a page that holds its connection, and proposes recording the client's ledger at onboarding. Each part ships when it is ready, before or after launch; launch never waits for it (see [effect on the build plan](#effect-on-the-build-plan)).

## The problem

Every check that makes a close useful reads the month's books: the opening baseline, the tie-out, readiness and sign-off, the balance exceptions, month-over-month changes, account coverage, the management pack and budget against actual. Today those books come only from a QuickBooks Online or Xero capture, and the cash plan opens only for QuickBooks clients, from the latest capture's bank balances.

A client on anything else (QuickBooks Desktop, Sage, NetSuite, Business Central, MYOB, Tally, Zoho Books, FreshBooks, Wave) can upload exports and label them ([historical exports](validation.md#historical-exports-labeled-as-reported-2026-09-28)), but a label is not a Trial Balance. None of those checks run, and the brief keeps asking for the exports to be verified. The same is true of a QuickBooks Online client whose owner will not grant the firm a connection.

A firm moves its practice only when it can bring all of its clients. A product that serves half of them, however well, stays a side tool. The aim is that **every client gets the whole close, whatever ledger they keep and however the firm reaches it.**

## Principles

1. **Take the books the way the client has them.** Any ledger, any export layout, CSV, Excel or PDF. No fixed template the firm must reshape files into; the product learns the layout.
2. **Keep everything; hold only what depends on what is missing.** Every file is kept and read, and work that does not depend on a gap goes on. A gap holds only the results that need it (a statement, a tie-out claim, a sign-off of full coverage), and the page names exactly what would release them. Nothing is refused because something else is missing.
3. **Files are first-class.** A month on files can be signed off, published and carried forward like a month on a live ledger. Its source is named plainly everywhere ("Books from files, exported 3 October by …"), never shown as lesser.
4. **Stop for correctness, never for format.** A layout the product does not recognise is a question, answered once. What holds sign-off is accounting: an incomplete population, a Trial Balance that does not balance, accounts without a classification, a month that does not roll forward, an incompatible basis.
5. **Whoever has the file can provide it.** A member uploads it, the client sends it through a request, it arrives by forwarded email, or it is picked from Drive. Providing a file and vouching for it are separate authorities (see [who does what](#who-does-what)).
6. **Live where available, never required.** A live connection is preferred when there is one, because it refreshes and can be written back to. It is not a precondition for any check, and a connected client can still add files.
7. **Exact lineage.** The original file is kept byte for byte with its digest. Every reading is reproducible from it, down to the cell or page location of each figure. A later upload replaces an earlier one with a reason, as evidence does today, and both stay on record.

## What a client can bring

| File | What it gives | What it cannot give alone |
| --- | --- | --- |
| Trial Balance at the month-end | Month-end balances: opening baseline, tie-out, readiness, wrong-side, holding-account and opening-equity exceptions, coverage, the Balance Sheet and working capital | The month's profit and loss activity, unless it carries a movement column or a compatible prior month exists (see [monthly activity](#monthly-activity)) |
| Trial Balance with a column per month, or several months' files | Balances and, with compatible months, activity for each; prior-period comparatives | Closes for those months (see [history and takeover](#history-and-takeover)) |
| General Ledger detail for the month | Activity by account; with trusted opening balances, a derived Trial Balance; reconciliation ledger fill, schedule discovery, drill-down from a change to its transactions, the month's transaction review in Daily Books, payee causes; journal-level readback of written-back entries | Opening balances, unless the prior month is accepted or an opening column is present |
| Chart of accounts export | A better first guess at each account's classification and hierarchy | |
| Statement of cash flows | The management pack's cash flow, through the firm-attested import Xero months already use | |
| Book cash at a recent date (a Trial Balance or cash-account balances run to that date) | The cash plan's opening cash, as of that date | A forecast, without open items at the same cutoff (see [cash and open items](#open-items-and-cash-dates)) |
| Receivables and payables open items (invoice and bill level) | Open items for B10 and dated receipts and payments for the B09 cash plan | |

**A Trial Balance is not required.** A complete General Ledger for the month plus trusted opening balances (the prior accepted month's closing balances, or an opening column in the export) gives a Trial Balance by a deterministic roll-forward, recorded as derived. A month whose books come from a live ledger can take files as supplements: a fuller General Ledger, an imported cash-flow statement, open items, or a temporary replacement during a connection outage (see [one authoritative books version](#one-authoritative-books-version-per-month)).

**A Trial Balance alone is not a publishable pack.** It gives the Balance Sheet and, with activity, the Profit and Loss. Publication also needs a reconciled cash-flow statement, as it does today. Balances alone cannot give gross flows: a loan borrowed and repaid within the month leaves the same month-end balances as no borrowing at all, yet the statement must show both flows.

## Reading files

### Formats and lineage

- **CSV**, any delimiter and encoding the file declares or that can be detected without ambiguity.
- **Excel**, every sheet; the workbook itself is kept (today only the first sheet is kept, as CSV). Hidden rows, columns and sheets are read and shown as hidden. A stored value displayed rounded is read at its stored precision.
- **Formulas.** Ledger exports rarely have them; spreadsheets a firm builds by hand often do. A stored formula result is the last calculation, not necessarily the current one, so a balanced workbook can still be stale. Formula text is kept beside each stored value. When an amount depends on formulas, the reader recalculates the workbook's formulas over its own cells and its confirmed linked inputs, and compares; a match releases the amounts. Recalculation alone does not refresh linked values: a spreadsheet application can recalculate against its stored copy of another workbook's cells. So **linked workbooks are inputs**: each is supplied, read with its own digest, and its version and cutoff confirmed. Amounts that depend on a link whose input is not supplied and confirmed stay held.

  Formula amounts the reader cannot release itself (unsupported functions, a recalculation flag it cannot honour) are released by any of:
  - a values-only export freshly produced from the ledger;
  - for a spreadsheet that is itself the firm's working record, a **reviewed recalculated copy**: made in a spreadsheet application after every link was refreshed from the confirmed inputs and the workbook fully recalculated, with input cells matching the original's, and a reviewer attesting to the refresh and recalculation.

  The original, the linked inputs, the copy, their digests and the attestation are all kept, so the result can be reproduced.
- **PDF**, read page by page with the existing extraction. As bank-statement PDFs are confirmed today (`transport/statement.go`), every page is checked and every proposed row accounted for before the reading is used. Repeated page headers and page subtotals are recognised as such.
- Each figure keeps the location it came from (sheet and cell, or page and line) and its original text.

### Values

The reader states, per import, how it read amounts. It never applies one rule silently:

- negatives in parentheses, with a trailing minus, or as `Dr`/`Cr` suffixes; separate debit and credit columns; a single signed balance with its sign convention confirmed;
- thousands separators and decimal commas, from the file's locale or a confirmed choice;
- a stated scale ("in thousands"): amounts are converted with exact decimal arithmetic, so `1.23456` thousand is `$1,234.56`. Precision is then judged on the converted amounts: amounts exact to the cent are usable like any others; amounts that were rounded at the source hold their use as the month's books, and can serve as comparatives with their precision shown;
- column meaning: opening balance, period movement, year-to-date, closing balance, budget. A column's meaning is confirmed, not inferred from its position.

A value the reader cannot place with certainty is flagged for a person; it is never dropped or defaulted.

### Hierarchy, dimensions and entities

- **Rows have kinds:** posting account, parent total, section subtotal, grand total, header. Totals are checked against their children and then excluded from balances, so a file that lists both cannot double-count and still balance.
- **Parent accounts with their own postings** keep those postings as the parent's own balance, beside its children.
- **Dimensions** (class, location, department, tracking category) are kept as dimensions, separate from the account's classification. An account repeated once per dimension is one account whose balance is the sum, with the split preserved.
- **Several entities in one workbook** are split into one import per client. Each is authorised, checked and accepted on its own. Accepting such a file gives each client its own books; it does not consolidate them.
- **Access to a combined original.** The original holding several clients is kept under access covering all of them (a member who can open every client in it). Each client gets an extracted view of its own rows, with provenance back to the original's digest and locations. Downloads, exports, the close package and model context use the extracted view for anyone without access to the whole.

### Size and parts

At least 3,000 accounts and 100,000 ledger lines a month. A larger export, or a ledger that exports in pieces, can come as **parts of one import**. The parts declare the same client, month, basis and scope. Overlap is judged on what each part contains, not on account names:

- ledger-detail parts may repeat accounts (the 1st–15th and the 16th–31st of a month will), so they are combined by their transaction and line identities, or by a partition the person confirms, such as non-overlapping date ranges;
- balance parts must be mutually exclusive slices (account ranges, entities, or dimension values confirmed not to overlap); class and location summaries of the same accounts are not.

The combined import is checked as one, for completeness and for duplicates.

## How an upload becomes the month's books

1. **Submit.** From the client's Books card, a client request, forwarded email or Drive. A file whose client or month is not obvious is asked about once. Files from outside the firm are scanned before release, workbooks included.
2. **Read.** A recognised layout is parsed directly. Otherwise the agent proposes the header rows, row kinds, column meanings, sign convention, as-of date and currency; a person confirms. The confirmed layout is saved for the client as a version and proposed again next month. It is reused without a question only when the new file matches it on headers, column meanings and scale, and its totals check; any difference asks again.
3. **Scope.** The import records what population it covers: the client and entity, the ledger or reporting book, any filters the export applied, the accounts and dimensions present, and the periods. The person attests that it is complete, or names what is missing.
4. **Map the accounts.** Each account needs a class (asset, liability, equity, revenue, expense) and, where the checks use one, a type (bank, credit card, receivables, payables, prepaid, fixed asset, accumulated depreciation, loan, payroll liability, sales tax and so on): what QuickBooks' account directory gives a capture today. The agent proposes from names, numbers, the hierarchy, a chart-of-accounts export and earlier months; a person confirms. The map is versioned. A reclassified, renamed or renumbered account is a change to the map, not a new account, and revalidates whatever depended on it.
5. **Basis.** What the export represents (accrual, cash or modified cash), confirmed for the import and compared with the client's approved basis (see [basis](#basis)).
6. **Check.** The checks below run on every import and every replacement.
7. **Accept.** A member with review authority accepts the reading: the client's books for the month, from the named system, exported on the named date, with the stated scope, basis and whether the final adjustments are in. The acceptance replaces the historical-exports label for these files, and is part of what a sign-off covers.
8. **Activate.** The accepted import becomes the month's books version if Files is the month's source, or a supplement if it is not.

### Checks

Each check either holds results that depend on it, or is a note. None refuses the upload.

| Check | What it holds |
| --- | --- |
| **Scope is complete:** the attested population, every account with a balance in the prior accepted month present or explained, no filter that excludes postings | Every statement, total and check that includes what is missing, until the rest arrives. An engagement exclusion, as the plan allows, narrows which accounts the firm prepares and reconciles; it never stands in for missing balances, so company-wide statements stay held. A report on a narrower population (one department, say) names that population on its face |
| Debits equal credits to the cent, after totals are excluded | Everything that reads balances |
| The as-of date is the month-end | Use as the month's books (it can still be kept as history) |
| Totals equal the sum of their children | The import, until the hierarchy is confirmed |
| Every account has a class | The statements and coverage; unclassified accounts already make the pack incomplete |
| Amounts in the client's currency and exact to the cent once converted | Use as the month's books |
| Basis: what the export represents matches the client's approved basis, and any month it is compared or combined with | Comparisons, derived activity and roll-forwards across the mismatch, the treatments the approved basis does not support, and the statements, sign-off and publication built on the import (see [basis](#basis)) |
| Roll-forward, when ledger detail or an opening column is present | The accounts that do not roll (see [roll-forward](#roll-forward-and-changed-prior-months)) |

The first month on files goes through the existing opening baseline review, as a first captured month does.

### Monthly activity

The management pack and budget against actual need each profit and loss account's activity for the month. It comes from, in order:

1. a period-movement column the person confirmed;
2. a complete General Ledger for the month;
3. this month's and the prior month's year-to-date balances, only when both are the same basis, book and scope, the fiscal-year boundary is accounted for, **and the prior figure is current**: it comes from the same export as this month's (a multi-month export with both columns), or there is evidence the prior month is unchanged since it was accepted (a re-export of it that matches, or ledger detail with no prior-dated entries made since). Otherwise a backdated adjustment to the prior month would be reported as this month's profit. In the first month of the fiscal year the activity is the year-to-date balance itself, and a post-closing Trial Balance (profit and loss balances closed to equity) is recognised and not differenced.

Otherwise activity is unavailable and the page asks for the one thing that would supply it, usually a period-movement column. It is never estimated. Derived activity pins both versions it was taken from and is recalculated when either changes. Signs follow the existing convention: balances are debit minus credit; activity uses the profit and loss presentation sign, as `books.Activity` does now.

### Roll-forward and changed prior months

With ledger detail, an opening column, or a multi-month export that includes the prior month, each account's opening balance is compared with the prior accepted month's closing balance, and the month's movements are reconciled separately from that opening. A difference is reported first as **unexplained**, with its accounts and amounts. It becomes "the prior month changed after it was closed" only with evidence of that change: ledger lines dated in the prior month and entered after its acceptance, or a re-export of the prior month that differs from what was accepted. Fiscal-year closing entries are modelled explicitly, not reported as differences. Both versions of a changed prior month are kept, and its downstream effects (signed drafts, published packs, carried schedules) are listed.

### Versions, replacements and approvals

Each books version pins what it was read with: the file digests, the layout version, the column meanings, the account map version, the scope, the basis and each figure's source location. A replacement upload is a new version with a reason. An approval or sign-off survives a new version only when every input it depends on is unchanged: its figures, the accounts' classification and meaning, the scope and the basis. A change to any of these revalidates the work that used it, as a new capture does today.

## What differs from a live ledger

| | Live ledger (QuickBooks, Xero) | Files |
| --- | --- | --- |
| Freshness | Captured on demand. A scheduled refresh covers open, unsigned months within its limits; a QuickBooks month stops refreshing automatically once a person captures it ([source sync](source-sync-and-forwarding.md), [Xero](xero-onboarding.md)). Transaction captures and month-end reports have their own times | As current as the last upload; the export date shows wherever the books do. Re-uploading is allowed at any time, including mid-month |
| Changes after capture | Change hints and the scheduled refresh | A re-upload. When approvals need fresher books, the agent drafts a client request for the new export |
| Writing back | Approved entries post through the governed path, with provider readback | A journal import file with journal-level readback (below) |
| Native reconciliation flags | Xero's are counted | None |

### Writing back

For a ledger without a native connection, approved entries download as a **journal import batch** in the ledger's own import format where one exists (QuickBooks Desktop IIF, Xero, Sage, MYOB and NetSuite journal CSV), and a plain journal CSV otherwise. Each batch carries a batch identifier and each journal a reference in its memo, so a duplicate or partial import can be seen.

Each journal moves through **exported → entered → verified**, or **uncertain**:

- *entered*: a person records that it was imported, with the ledger's own journal reference;
- *verified*: the journal is found in a ledger-detail or journal-report export, matching the client, date, accounts, amounts and dimensions, and the affected month's books are refreshed to include it;
- *uncertain*: it was found twice, partly, or not at all when expected.

A month whose entries are dated within it is published only after they are verified in refreshed books for that month. Next month's Trial Balance alone does not verify a journal: it cannot identify a journal, its date or its dimensions, and a reversing entry may have netted out by then.

Not every correction is a journal. Where the right fix is to a source record (recoding a bill, voiding a duplicate payment), the product gives the bookkeeper the exact instruction and records completion with evidence, as the plan requires for providers without the native operation.

### Cash-flow statement

A month on files gets its cash flow as Xero months do: the firm imports its statement and attests to it, and the existing check ties its sections to the net change and its ending cash to the books' cash. The import takes the existing `Section,Line,Amount` form (`parseCashFlow` in `transport/management.go`) after the file is read and normalised like any other. A statement prepared by the product from ledger detail needs an accountant-approved deterministic method with the transactions, schedules and non-cash facts it requires; it is never derived from balances alone, which the current `cashFlow` check already refuses.

### Open items and cash dates

B09 and B10 need invoice- and bill-level facts, dated receipts and payments, and current cash; month-end balances cannot supply them. Three times are kept apart: the **accounting period end** a books version describes, the **observation time** a cash balance or open-item list was true at, and the **upload time**.

- **Current cash.** The cash plan opens from an observation of **book cash**, as it does today (the latest QuickBooks capture's balances for its bank accounts, at its capture time; a Xero client's cash plan is not built yet). A client on files brings book cash at a recent date: a Trial Balance or cash-account balances run to that date. September's month-end books uploaded on 10 October are not cash on 10 October. A bank balance is not book cash. Say the bank shows $100,000 and a $20,000 check recorded on the 8th has not cleared: book cash is $80,000, and the bill the check paid is no longer open. Opening the forecast at the bank's $100,000 would leave the $20,000 out of both the opening cash and the payments to come, overstating cash by $20,000. A bank export can open the forecast only with a reviewed **dated bank-to-book bridge** at the same date (bank balance, outstanding items with their evidence, book cash), which gives the $80,000. That bridge is new: today's reconciliation workpaper covers whole months only. Each observation records its balance type (book or bank) and the accounts it covers.
- **Open items** come as exports (customer or vendor, document, dates, amount, open amount) with their own as-of time. Cash and open items must share a cutoff, or the gap is bridged with evidence (receipts and payments between the two times); otherwise collections in the gap would vanish from both opening cash and future receipts.
- **Accrual books:** open items at a month-end are reconciled to the receivables and payables control accounts of that month's books; a difference holds the open items, not the close.
- **Cash-basis books** carry no receivables or payables, yet the client still has unpaid invoices and bills. Their open items are accepted as an operational list, checked for completeness on their own terms (the source report's totals and document sequence) and labelled as outside the books. They feed collections and the cash plan and never enter cash-basis statements.

Refreshing is a re-upload; promised dates and follow-up notes carry over by document. Payment and collection actions keep their existing approvals and hand-offs. Month-end versions are kept independently of later observations.

### History and takeover

The opening baseline reviews the takeover month's month-end tie-out; it does not create a missing opening balance or earlier months' activity. History is therefore its own status:

- **Reviewed comparatives.** Earlier months imported as files (or captured) and accepted as prior-period figures, with their scope and basis. They feed comparisons and trends; they are not closes and carry no sign-off. Publication rules that today require comparison months to be signed off are amended to accept a reviewed comparative, labelled as such.
- **Cross-source identity.** Comparisons and budgets match months today by the provider's company identity (budget actuals key on source and realm). A client moving from files to a live ledger, or the other way, needs a person to confirm that both are the same company and to map the accounts across, once; until then the months are not compared.

## One authoritative books version per month

- Each client has a **default source**: QuickBooks, Xero or Files (more ledgers later). Each month records the books version it reads and where it came from.
- A **month-level override** (for example, files during a connection outage, or a corrected export) is an explicit decision with a reason, by a member who may activate sources. Earlier months keep their versions.
- **Supplements** never replace the month's books, and come in two kinds, with the same rules for connected and file clients:
  - *month-end accounting supplements* (fuller ledger detail, a cash-flow statement, month-end open items for accrual books) are reconciled to the month's books version before use; one that does not reconcile is shown with its difference and not used;
  - *operational observations* (current book cash, current open items, cash-basis open-item lists) are checked under their own basis and observation time, as described under [cash and open items](#open-items-and-cash-dates), with a control-account reconciliation where the basis has one or an evidenced bridge across the gap.
- Months may differ in source: a takeover can bring a year of reviewed comparatives as files and run the current month live.

## Basis

The client's accounting basis already has one authority: the approved `basis` procedure (`procedure_versions`), changed only under `procedures:manage`, and read by `preparation.BasisIssue` to refuse treatments the basis does not support. It stays the authority. An import records what the export represents; a reviewer confirms that fact, not the client's policy. When the two differ, everything built on the import for reporting is held: comparisons and derivations, the treatments the approved basis does not support, **the statements themselves, and the sign-off and publication that depend on them**. They are released by compatible source data or an approved, evidenced conversion between bases, or by a change to the procedure by whoever may make it, with its existing invalidation. A label never converts figures: accrual figures are not published as cash basis because the procedure says cash. Each books version pins both its source basis and the procedure version it was checked against.

The management pack's basis label is hard-coded as `Accrual` today (`transport/management.go`). Reading it from the approved procedure, subject to the hold above, is part of this work.

## Client profile

`preparation.ClientProfile` takes the client's currency and fiscal-year start from its active QuickBooks or Xero connection, and without one falls back to USD and January with state `none`. A file-only client needs a **confirmed client profile independent of connections**: entity, currency and fiscal-year start, versioned with effective dates, and referring to the approved basis procedure rather than holding a basis of its own. Where a connection reports different values, the difference is a question, not an override. Currency checks, fiscal-year calculations, statement confirmation, takeover setup and the year-end handoff read the profile. No file-only client gets a placeholder connection to satisfy them.

## Architecture

### One month-books record

About 28 non-test backend files reference `quickbooks_snapshots` and 23 reference `xero_snapshots` (counted 2026-10-10; this includes writes, deletions and readiness checks, not only accounting readers). Adding a third provider table would add a third case to many of them.

Instead, every source writes **one month-books record**:

- **`books_versions`**: client, month, source (`quickbooks`, `xero`, `files`), the source's own record (a capture or an import), as-of date, currency, basis, scope, the account list (id, number, name, class, type, subtype, parent), month-end balances (debit minus credit), the month's profit and loss activity when available with how it was obtained, the pinned inputs listed under [versions](#versions-replacements-and-approvals), a digest, and who accepted it and when.
- **`books_lines`**: ledger lines when the source has them, with dimensions.
- **`books_imports`**, **`books_layouts`**, **`books_account_maps`** and **`client_profiles`**: uploaded files with each figure's location, the client's confirmed layouts and account maps with their versions, and the confirmed profile.
- **`books_sources`**: each client's default source and each month's overrides and supplements.
- **`books_observations`**: current bank balances and open-item lists with their observation and upload times, apart from month-end versions.

The shape already exists in miniature. `periodBooks` (`transport/flux.go`) returns a close's newest month-end capture from either provider, and `readLedgerMonth` turns it into the management pack's `books` type (`transport/management.go`): currency, accounts with class and type, balances and activity. Month-over-month changes, budget against actual and the management pack read through that seam. `ledger.AccountBalance` is the per-account form the shared exception checks take. The month-books record is that shape, stored, with lineage. Today `periodBooks` picks whichever capture is newer; with the record, the month reads the version its source selects.

QuickBooks and Xero captures keep their raw payloads and also write a month-books version. Provider-specific work stays provider-specific: posting, bill recodes, change hints, entity syncs and native reconciliation flags.

The demonstration seed writes QuickBooks-shaped captures from generated reports. A client's files must not be written that way: it would record a file as a QuickBooks read.

### Migration

1. **Backfill and dual-write.** Write a month-books version for every existing QuickBooks and Xero month-end capture, then for every new one, idempotently. A parity check compares what each reader computes from the provider payload and from the record; readers do not switch until it agrees for every existing close.
2. **Source selection.** The client's default source and month overrides exist, set from today's behaviour (the newest capture), before any file can become a month's books.
3. **Feature by feature.** Each feature moves its readers, its readiness and digest inputs, its status lines, its exports and its invalidation together, so no two parts of the product disagree about one month. Order: the `periodBooks` seam (management pack, month-over-month, budget against actual); the tie-out and opening baseline; readiness and exceptions; coverage; the cash plan, once current-cash and open-item observations are stored apart from month-end versions and covered by the parity check; then the ledger-line readers (reconciliation fill, schedule discovery in `preparation/straight.go` and the recurring paths, Daily Books, search); then Home, Portfolio, the morning brief, the coordinator, the plan and the close package.
4. **Existing records stand.** Signed-off and published versions keep their digests and their content; nothing already signed is recomputed under the new record.

File imports stay behind a firm setting until every feature a month on files needs reads the record.

The record landed first as step 3 of the delivery, with no visible change (migration 145): the worker's books-versions loop writes a version for every QuickBooks period capture and Xero capture that has none, the existing ones first, a batch at a time, then each new one within a minute. A version is the books `readLedgerMonth` makes of the capture (accounts with class, type and subtype; balances and activity as exact decimals; the ledger's cash-flow statement when it has one), with the capture it came from, the company, the month-end date and a digest of its figures; a QuickBooks capture's General Ledger rows are kept as its lines, each with its report location. A capture the readers cannot read is kept with the reason, once, so a reader that would refuse it agrees with the record. The parity check recomputes each stored version from its capture's payload and names any that disagree; the staff console's Connections page shows how many captures have their version and whether the readers agree. No reader reads the record yet.

## Connections

### Today

- **Per client, within one firm.** A connection row is keyed by firm and client name (`017_quickbooks_connections.sql`, `048_xero.sql`); QuickBooks is also keyed by environment since `023_production_posting.sql`. All of a client's months share the connection; each month keeps its own captures.
- **The reading connection.** A client's QuickBooks books are read from its production connection unless that is disconnected, otherwise its sandbox one (`quickbooks.ReadingConnection`). A client could also hold a Xero connection; the month-end readers through `periodBooks` take whichever capture is newer.
- **One Krith app per provider.** Firms never bring their own keys. Each client company still authorises separately.
- **Who:** a member whose session carries `integrations:manage`, signing in with their own QuickBooks or Xero login.
- **Where:** only inside a month. The month's Evidence tab has the QuickBooks, Xero, historical exports and Stripe panels; the production QuickBooks authorization is on the month's Settings tab. There is no client page: Clients leads straight to a month.

### What changes

- **The client's Books card** holds the default source, its state, the last capture or upload and its date, reconnect, upload, the saved layouts, the account map and the client profile. It lives on the [client page](#client-page), not in a month; each month's header shows the month's books version and its freshness and links to it. The QuickBooks, Xero and historical-exports panels leave the month's Evidence tab, and the production authorization leaves the month's Settings tab.
- **Firm Settings does not hold connections**, as already decided for Settings. Home's attention lists clients whose connection needs reconnecting and months still waiting for their books. The firm's setup checklist step "Connect the books or label historical exports" (`transport/setup.go`), which opens a month's Evidence tab today, opens the client's Books card instead.
- **Many clients at once:** a Xero sign-in can authorise several organisations, each assigned to a client in one step; a folder of files for many clients and months can be dropped at once, sorted by the agent and confirmed by a person. QuickBooks always authorises one company at a time, by Intuit's design.

### Client page

Each client gets a page at `/dashboard/clients/<client>`, on the Krith design system, reached from its row in Clients (the records page from #343). It holds:

- the client's name, and the roster settings `Roster.tsx` manages today: active state, first month to close, owner, and days after month-end, including what it takes from the firm's template. Renaming a client is not possible today; the page is the natural place for it as a follow-up, which must keep the client's identity, access and history intact (existing names already resolve through confirmed aliases);
- the **Books card**: the client's ledger and how its books arrive, the connection or the latest upload with its freshness, reconnect, upload, the saved layouts and the account map;
- the client profile (currency, fiscal-year start) and a link to the approved basis procedure;
- the client's months, newest first, each with its state and books version;
- open client requests, and the members with access.

It follows [design system](design-system.md#rollout) as any screen added during the rollout does: Krith's tokens, the Krith component from the artifact where one exists, and the rollout's stated fallback otherwise. It does not wait for the files work: on its own it moves today's QuickBooks and Xero connection panels out of the month. Until it ships, connections stay where they are today.

### Ledger catalog and onboarding

The owner's direction (2026-10-10) on which connector comes first: "I think it should be a config maybe as part of onboarding?" What follows is this document's proposal for carrying that out; its details are for the owner to confirm as the work starts.

- **The client's ledger is recorded at onboarding.** The Add client dialog (today: name and first month to close) also asks which ledger the client keeps: QuickBooks Online, Xero, QuickBooks Desktop, Sage Intacct, NetSuite, Business Central, Sage 50, MYOB, Zoho Books, FreshBooks, Wave, Tally, another ledger (named), or not known yet. Not known yet is completed later on the Books card.
- **The ledger is not the books source.** The ledger says which product the client keeps; the source says how a month's books arrive (the QuickBooks connector, the Xero connector, or files). Correcting the ledger changes the export steps and the catalog entry shown, and leaves accepted books alone. Moving a client between sources, for example from files to a new connection on the same ledger, is the source switch under [one authoritative books version](#one-authoritative-books-version-per-month), with its confirmation; it is available once source selection exists ([delivery](#delivery) step 4).
- **The ledger catalog is configuration, not code per ledger.** For each ledger it records: its name; its native connector, if one exists; the export steps and the recognised layouts for its Trial Balance, General Ledger and open items; its journal import format; and the operations it cannot do natively.
- **Connectors are switched on by configuration**, per environment and, for a pilot, per firm. A connector reaches firms when it passes its own acceptance, with no other change.
- **No dead ends.** Choosing a ledger whose connector is not switched on gives the files route with that ledger's export steps. Another ledger gives the files route with any layout, under the name given.
- **Demand informs the next connector.** The admin console counts clients by ledger, including the names given for other ledgers, and by whether they have a connector. Those counts inform which native connector to build next, weighed against each ledger's access terms under [more ledgers](#more-ledgers).

### Who does what

| Step | Who |
| --- | --- |
| Submit a file | Any member with access to the client; the client through a request |
| Release a client's file | As today: after a clean scan, by a member who reviews the request |
| Confirm a layout, account map, scope and basis; accept an import | A member with `workpapers:review` |
| Connect a ledger, set the default source, override a month | A member with `integrations:manage` |
| Mark a written-back journal entered | A member with `posting:approve` |
| Change the client's accounting basis | As today (`procedures:manage`) |
| Sign off | As today (`close:signoff`) |

### Client-provided books

A client can be sent "Connect your books" or "Upload this month's Trial Balance" as a client request. This matters for owners who will not hand the firm an accountant login, and few competing products offer it.

- **Connecting.** The sign-in is bound to that request and its client: single use, expiring with the request, revoked with it, and refused if replayed. A connection made this way is inactive until a member with `integrations:manage` confirms that the company belongs to the client (the existing confirmation step).
- **Uploading.** Request links take TXT/CSV up to 512 KiB and PDF up to 5 MiB today ([coverage requests](coverage-requests.md)). Books files need Excel workbooks, scanned like other client files, and larger limits.
- **What the client sees:** received, scanned, accepted, and any question still open, in the request.

### Provider terms that shape this

From vendor pages and third-party reports gathered 2026-10-10 (sources below); each needs checking against the provider's own terms before it is relied on.

- **QuickBooks reads are metered** under Intuit's App Partner Program (from July 2025): a free tier of 500,000 read credits a month, then paid tiers from $300 a month. Measure calls per capture before adding many clients.
- **QuickBooks may allow one active connection per company per app.** If so, two firms serving one company through Krith's single app, or a client moving firms, would replace one another's connection. Verify with Intuit.
- **Xero prices by connected organisations** (from March 2026): free for 5, about A$35 a month for 50, A$245 for 1,000, A$1,445 for 10,000. Ledger lines through `/Journals` and the Bulk Connections picker need the Advanced tier, a security assessment and use-case approval. The current capture does not use `/Journals`; it reads the Trial Balance report and transaction endpoints (`xero/xero.go`).

## More ledgers

Files cover every ledger from the first release. Native connectors add refresh and writing back where enough clients need them; which comes first is informed by the ledgers firms record at onboarding (see [ledger catalog](#ledger-catalog-and-onboarding)), not fixed in advance.

| Ledger | Where it matters | Programmatic access | Recommendation |
| --- | --- | --- | --- |
| QuickBooks Desktop | Still large in the US: Intuit's Desktop ecosystem earned about $2.8B in FY2025. New sales of Pro, Premier and Mac Plus to US subscribers stopped in September 2024; Enterprise is still sold | No cloud API; the Web Connector runs on the client's Windows PC. Conductor sells a REST layer over it | A candidate to evaluate when onboarding counts show demand: Conductor rather than building the Web Connector |
| Sage Intacct | US mid-market; supported by FloQast, Numeric and Truewind | Each company authorises our sender ID; partnership about $2,500 a year plus per-call fees, as reported. The cited Trial Balance page documents the XML `get_trialbalance` function; the REST equivalent must be confirmed before relying on it | A candidate to evaluate when onboarding counts show demand |
| NetSuite | Mid-market | No Trial Balance endpoint (summed from transaction lines with SuiteQL); each customer sets up access | Files; native later if demand |
| Business Central | Mid-market; vendor counts put it above NetSuite | Trial Balance, ledger entries and journal posting; consent per customer | Files; native when a firm brings several |
| MYOB, Tally, Zoho Books, FreshBooks, Wave, Sage 50 | MYOB in Australia, Tally in India; the rest a long tail | Varies; Tally is local only; Wave's API reportedly needs a paid plan | Files |

**Unified accounting APIs** (Merge, Rutter, Apideck, Unified.to, FIS's Railz; Codat has moved to banks): most show no true Trial Balance, cost about $25–65 per linked client a month as reported, and still need our own Intuit and Xero apps and fees. They are not worth it for the core ledgers. One may suit read-only long-tail ledgers later, once files show which ones clients use.

**What others do.** FloQast and Fathom take a Trial Balance file from any ledger: FloQast maps each export layout, Fathom requires its own template. Truewind asks for twelve months of ledger detail for ledgers it does not connect. Double (formerly Keeper) has an "unconnected client". Numeric connects only NetSuite, QuickBooks Online, Xero and Intacct. Taking any layout without a template, and running the whole close on it, would put Krith ahead of these.

Reliable US market-share figures for these ledgers were not found; the counts above are vendor-reported.

## Delivery

Each PR leaves the product working, and is reviewed and validated as usual. None of it gates launch, and launch work does not wait for it. File imports stay behind a firm setting: step 9 turns it on for internal validation and the demonstration firm only; partner firms get it after step 13, once the acceptance below passes.

1. **Client page.** On Krith, with the Books card holding today's QuickBooks and Xero connections, moved out of the month, and the setup checklist pointing to it. Independent of everything below.
2. **Ledger catalog and onboarding** (details to be confirmed with the owner as the work starts; see [ledger catalog](#ledger-catalog-and-onboarding)). The catalog as configuration, the client's ledger recorded in Add client and on the Books card (including not known yet and a named other ledger), connector switches, and the admin console's counts. Recording or correcting the ledger changes no books; source switching arrives with step 4, file upload with step 9.
3. **Month-books record.** `books_versions` and `books_lines`; backfill and dual-write for QuickBooks and Xero; parity checks. No visible change.
4. **Client profile and source selection.** The confirmed profile and its readers; each client's default source and month overrides, set from today's behaviour.
5. **Readers move.** Feature by feature as under [migration](#migration), each with its readiness, status, exports and invalidation. A reader switches only when parity holds and the switch puts no launch work at risk; otherwise it waits, and the provider path keeps working unchanged.
6. **Reading files.** Formats, values, hierarchy, dimensions, entities, parts; layouts and account maps with versions; scope and basis; the checks; acceptance.
7. **Activity, roll-forward and history.** Monthly activity, roll-forward and changed prior months, reviewed comparatives and cross-source identity.
8. **Ledger detail.** Ledger-line imports; derived Trial Balances; reconciliation fill, schedule discovery, drill-down and Daily Books on files.
9. **Months on files.** Activation, supplements and overrides; the opening baseline, tie-out, exceptions, coverage, sign-off and the pack (with an imported cash-flow statement) on files. Internal validation only; work that later steps supply is shown as unavailable, not attempted.
10. **Writing back by file.** Journal import batches, the journal states and source-record correction instructions.
11. **Cash and open items.** Current-cash and open-item observations with their cutoffs; a dated bank-to-book bridge (extending the reconciliation to a mid-month cutoff, or a reviewed external reconciliation with its outstanding items), pinned to its cutoff, accounts and evidence and never counted as a completed month-end reconciliation; reconciliation to control accounts for accrual books and operational lists for cash-basis books; the cash plan on files.
12. **Client-provided books.** Connect and upload requests, workbook scanning, larger limits. Part of the first release of files to partner firms.
13. **Partner release of files,** once the acceptance below passes.
14. **Native connectors,** in an order informed by demand (the onboarding counts, as proposed) and each ledger's access terms, each switched on by configuration after its own acceptance.

## Acceptance

- **Files agree with the live ledger.** For a sandbox QuickBooks month and a Xero demo month, the same month's exports, uploaded as files, give the same month-books version to the cent and the same tie-out, exceptions and management pack.
- **Real exports read without a template.** At least QuickBooks Desktop, Sage 50, NetSuite and a spreadsheet the firm built by hand, each read, mapped and accepted, then read again the next month with no question.
- **The hard cases are caught, not absorbed:** a partial export (missing department, account range or journal) holds dependent results; a file with parent totals and children does not double-count; an account per class or location sums once; ledger parts for two halves of a month combine without loss or duplicates; parentheses, `Dr`/`Cr`, decimal commas and an exact scaled file read correctly, and a rounded one is held as the month's books; a balanced workbook with stale formula results is caught; a cash-basis file is not compared with an accrual month, and a basis that differs from the approved procedure is a question; a post-closing Trial Balance and a first fiscal month give the right activity; a backdated prior-month adjustment in a year-to-date export is not reported as this month's profit; a reclassified account revalidates its approvals; an unexplained roll-forward difference is not called a changed prior month; a journal imported twice, or partly, is uncertain; month-end books uploaded later are not used as current cash; a $100,000 bank balance with a $20,000 recorded, uncleared check opens the forecast at $80,000 only through a dated bridge, and the check is not subtracted again; a mid-month bridge is not shown as a completed month-end reconciliation; accrual figures under a cash-basis procedure hold the statements; a firm-built workbook it cannot recalculate is released by a reviewed, recalculated values-only copy; a balanced workbook whose formulas recalculate cleanly against stale linked values is held until the linked workbooks are supplied; a firm-built adjusted Trial Balance linked to its supporting schedules, with a function the reader cannot evaluate, is released by a reviewed copy recalculated after its confirmed schedules were refreshed; a member with access to one client in a combined workbook sees only that client's rows.
- **Two consecutive months on files** for one client, through sign-off and publication, with the second month's opening rolling from the first and the first month's written-back journals verified at journal level.
- **A client's own upload and connection** through a request, confirmed by a member.
- The plan's partner-signup gates apply to months on files as to live ones.

## Effect on the build plan

- **Launch is unchanged and never waits for this.** The launch acceptance group stays US, single-entity, USD service businesses on QuickBooks Online. Nothing here is a launch gate, and no launch work is held for it. The plan records this track in its scope section.
- **Each part ships when ready**, before or after launch. Files reach partner firms only after their own acceptance above; native connectors only after theirs.
- **Launch is protected.** The month-books record is written beside today's captures, and readers switch only under the parity rule in [delivery](#delivery) step 5.
- **When files reach partner firms,** the group of clients a firm can bring widens to any ledger whose month-end books it can export; single entity and USD are unchanged.
- **Where each part belongs** once it lands: takeover history and the client page with B01 and B02; months on files, writing back by file and carry-forward across sources with B03–B06 and B11; publication on files with B07 and B08; cash and open items on files with B09 and B10; the files row in the provider capability inventory with B12.

## Decisions (2026-10-10)

1. **Files can land at any time; launch must not be blocked.** Files are a track beside the launch, not part of it (see [effect on the build plan](#effect-on-the-build-plan)).
2. **Clients may connect or upload their own books through a request** in the first release of files to partner firms ([delivery](#delivery) step 12).
3. **A client page on the Krith design system** ([client page](#client-page)).
4. **On which connector comes first:** "I think it should be a config maybe as part of onboarding?" No connector is chosen in advance. The [ledger catalog](#ledger-catalog-and-onboarding) section proposes how to carry this out (the ledger recorded at onboarding, a configured catalog, connectors switched on by configuration, counts that inform the next connector); its details are to be confirmed as the work starts.

## Sources

Gathered 2026-10-10; vendor pages may change.

- Intuit FY2025 10-K: https://www.sec.gov/Archives/edgar/data/896878/000089687825000035/intu-20250731.htm
- QuickBooks Desktop stop-sell: https://quickbooks.intuit.com/r/whats-new/quickbooks-desktop-stop-sell/
- Intuit API limits: https://developer.intuit.com/app/developer/qbo/docs/learn/limits-and-throttles
- Intuit App Partner Program pricing, as reported: https://www.apideck.com/blog/quickbooks-api-pricing-and-the-intuit-app-partner-program
- One connection per company per app, as reported: https://help.quickbase.com/docs/connect-to-quickbooks-online
- Xero API pricing and tiers: https://developer.xero.com/pricing and https://developer.xero.com/faq/pricing-and-policy-updates
- Xero investor deck H1 FY26: https://brandfolder.xero.com/NE531UQB/as/ks8qbt4tj62mb8j6hpq6mz/Investor_Deck_H1_FY26_Xero
- Sage Intacct Trial Balance (XML `get_trialbalance`): https://developer.intacct.com/api/general-ledger/trial-balances/
- Sage Intacct partnership pricing, as reported: https://docs.rutter.com/platforms/accounting/sageintacct2
- Business Central Trial Balance API: https://learn.microsoft.com/en-us/dynamics365/business-central/dev-itpro/api-reference/v2.0/api/dynamics_trialbalance_get
- QuickBooks Desktop integration options: https://conductor.is/blog/quickbooks-desktop-sdk-vs-web-connector-vs-qbxml
- FloQast Trial Balance upload: https://help.floqast.com/hc/en-us/articles/360002052972-TB-Upload
- Fathom Excel import: https://support.fathomhq.com/importing-data/financials/importing-from-excel
- Truewind: https://www.truewind.ai/blog/truewind-vs-basis-which-fits-your-team
- Double (formerly Keeper) onboarding: https://help.doublehq.com/en/articles/6415571-new-client-onboarding
- Numeric: https://www.numeric.io/solutions/close-management-software
- Apideck pricing: https://www.apideck.com/pricing.md
