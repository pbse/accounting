# Workspace design system (Krith)

The product (everything under `/dashboard`) uses **Krith**: a white canvas, a faint grey sidebar with categories, hairlines instead of shadows, graphite for the one thing to do, blue only where something is alive, Geist for everything the person operates and Newsreader for the few lines the product says in its own voice. Each screen asks one thing of the person. Home is a stage for the agent. A close is one sentence and the few things left. Records pages keep the same shell, so the agent is always a keystroke away. It is light only. The client request page at `/request` takes the tokens and type only: it is public, its link authorises one scoped reply and nothing else ([client request links](client-request-links.md)), so it keeps its standalone single-column shell with no sidebar and no composer.

The reference for every value is the Krith design system artifact: https://claude.ai/artifact/5UEvdXnLb9BgC4Ecte4ydL. It holds the tokens (colour, type, spacing, radius, shadow, size), the brand book (content, visual foundations, motion, the agent, icons, accessibility) and a live preview with guidelines for every component. The design was chosen on 2026-10-10 from the mockups in https://claude.ai/artifact/92yC3uzKqVHD7dcqhts5sk (rows "Calm" and "Growth", boards G0–G4). The earlier system, Tray, is archived at [design-system-tray.md](archive/design-system-tray.md).

## Where things live

| What | File | Change it when |
| --- | --- | --- |
| Tokens: colours, type, spacing, radii, shadows, sizes | `packages/ui/theme/tokens.css` | A value changes in the design system. Names match the artifact's tokens. |
| The shell both apps share: the sidebar, the page column, element styles | `packages/ui/theme/shell.css` | The shell or a shared element style (buttons, inputs, chips, cards, tables) changes. |
| Font files (Geist and Newsreader; both OFL) | `packages/ui/theme/fonts/` | A face is upgraded. Keep the Latin and Latin Extended subsets with their `unicode-range`s. |
| Shared components, each with its stylesheet beside it | `packages/ui/components/` | A pattern used on more than one page or app changes. |
| Icons | `packages/ui/components/Icon.tsx` | An icon is added or replaced. |
| The firm app's own patterns (preparation forms, evidence pane, the thread) | `apps/frontend/src/theme/workspace.css` | One of those changes. |
| The staff console's own patterns (workflow flow, inspector, schema forms) | `apps/admin/src/admin.css` | One of those changes. |
| The browser toolbar colour on shell pages | `packages/ui/theme/useShellThemeColor.ts` | A new shell page opens outside the firm app's workspace layout. |
| The public site's styles (its own system: `k-site` classes; see [public site](marketing-site.md#design)) | `apps/frontend/src/styles.css` | A public page changes. |

In code the shell's classes carry the prefix `ks-` (the public site's stylesheet already uses `k-`, and the layout check proves the shell takes nothing from it); the design system's previews call the same parts `k-`.

Both apps import the design system as `@close/ui/...`, an alias to `packages/ui` set in each app's `vite.config.ts` and `tsconfig.json`. Each `main.tsx` imports `@close/ui/theme/tokens.css`, `@close/ui/theme/shell.css` and then its own stylesheet.

## The shell

One sidebar, no top bar. The sidebar is 256px on `surface-side` with a hairline on its right (56px of icons when collapsed), and its order is fixed:

1. The wordmark (Newsreader, lowercase, over its double blue rule) and the collapse control.
2. The firm line (the firm's name, a switch when the person belongs to more than one).
3. New chat, with its ⇧⌘O hint (⌘K stays the command palette, as `CommandPalette.tsx` binds it today).
4. The pages. For a firm member: Home, Clients, Closes (with a count of what needs the person), then each module as a group that opens to its pages (Tax: Returns, Estimates, Deadlines), then Reports. For a client executive, the same sidebar carries their four client-scoped destinations instead (Business overview, Reports, Cash plan, My decisions, as `executiveNav` in `Layout.tsx` gives them today, under the selected client's name in the firm line's place); they never see firm navigation. A module is one line until opened; the one the person is in stays open.
5. Chats: the four most recent by title, a pulsing dot while the agent works, a steady dot when it answered while the person was away, "All chats".
6. Needs you: one-line notices of what the agent noticed (a document arrived, a client replied, something is waiting, something is done), at most four showing, then "All notices". Taking one opens it and the card slides out. "All notices" opens the full list (grouped Today, Yesterday, This week, Earlier, with a filter, as "All chats" does for chats), where taken notices stay, muted, so nothing is lost.
7. At the foot: Settings, Help, then the person (avatar, name, role), which opens the account menu.

The main column is white. Home is a stage: the greeting in Newsreader ("Good evening, Prashant."), a concise current briefing with freshness, the composer at the centre with up to three contextual suggestions, a Your-next-decisions list of up to three rows and a folded summary of completed agent work. Send turns the stage into the thread: the greeting and the list fade up, the composer glides to the foot, the answer streams. New chat brings the stage back.

**Founder screenshot clarification (October 10):** adopt that main Home composition while retaining the current sidebar destinations. Do not copy the concept's nested company/month tree. Clients remains the scalable browsing entry to each company and its monthly closes; the selected close keeps its current tabs. Home/chat/notices open the exact scoped decision directly for routine supervision. Recent chats and notices stay bounded as above, and do not become a second action queue. The [Home/navigation handoff](agent-first-implementation-handoff.md#b02--approved-home-and-scalable-client-navigation) contains the saved visual reference, scope rules and acceptance. Its next-decisions list supersedes the older This-week label below: urgency and unresolved work determine inclusion, including older reopened periods.

This revises the Home of the [dashboard specification](maturity.md#implementation-re-review-and-dashboard-specification-2026-10-02) and [workspace simplification](workspace-simplification.md), decided on 2026-10-10 with the design: the four outcome cards, the trust strip and the full decision list leave Home and become the **Closes** records page, one click away (`/dashboard/closes`, once the roster that renders there today, `ClosesList`, has moved to `/dashboard/clients` behind the Clients item in the records-pages PR, which lands before the Home stage so the decisions always have a page; that PR also moves every caller of the roster's old path with its query parameters, found by searching both apps and the backend for `/dashboard/closes` not followed by an id: the client search in `Layout.tsx`, the onboarding links in `setup.go`, Home's first-client action, and any other, so that no link meant for client selection opens the decisions page), where the four counts are its stat row, the trust strip is its context line (with when it was counted) and the decision list is its table, grouped by client with the most pressing close first, with the same bulk approval, hand-offs and dependency lists beneath. Home keeps the information in its own form: the line under the greeting states the counts that matter today and when they were read ("Three closes want you this week · 4 decisions · counted at 6:12"); the This-week list is the top of the decision list, grouped by client; "All closes" beneath it opens Closes; the Closes nav item carries the count of new (unread) decisions, as the Home badge did; the page states how many are waiting. A portfolio goal the agent links to (`/dashboard?goal=…`) is previewed in the thread as the exact closes it selects. Every count still comes from `GET /v1/closes/home` in one snapshot, so B02's population, counts and freshness are unchanged; only where they show has moved.

A page (a close) follows the [close-page canvas](https://claude.ai/artifact/9kJ14xr1tot7UiGqwvGXVD), one tab at a time. The header is the same on every tab:
- the breadcrumb row with the close's own controls on the right: the agent's live chip while it prepares, Sign off (disabled until readiness and the reader's role allow it, and the page's primary only when nothing else needs the person), and the ⋯ menu (plan this close, prepare a draft by hand, stop the agent);
- the lede: one sentence in Newsreader counting what needs the person in its units ("Three decisions and one blocker need you."), or saying the month is ready to hand to the agent, is ready to sign off, or what it waits on;
- one fact line: due date, whose it is, basis, and anything not checked, not read or not refreshed;
- the tabs, each with a badge counting the decisions whose action is on it.

Overview (`Overview.tsx`) has two columns. On the left:
- Needs your decision: at most three numbered cards, each with a title, one line and one action; the first action is the page's one primary, and the rest fold to a line. They come from the close's decisions (`GET /v1/closes/{id}/decisions`, the inbox's events for this close, read or snoozed included), with drafts ready for approval as one review (each still its own approval; Start review opens them in place, one after another: what the agent did, the effect on the books and whether the balance then ties, the sources, then Approve, one click, or Return with a note; each outcome stays as a line and the next decision is marked up next; payroll goes to its own page for its confirmations), then the brief's blockers that no decision stands for, and, before the hand-over, handing the month to the agent. When nothing needs the person, one calm line says so. A teammate's decisions, what waits on others, what was not checked and what is done each fold to a line.
- One table: the agent's work, each document and its status (Preparing, Needs your decision, Waiting for client, Ready for review, Approved, or why it is out) with a status filter; before the hand-over it is the agent's plan.
- The agent's plan, documents and accounts (the goal's details, coverage and each document's work), one fold away and open when a link points into it.

On the right is the rail: a few figures found nowhere else, the agent in one line with its activity folded, and more on the close.

Each tab keeps that frame and puts its own content in it. A tab's lede and fact line say where that tab stands, and its own actions head the ⋯ menu above the close's.

Books (`Books.tsx`):
- **Lede:** "The books tie out", "…except two accounts", or how many balance-sheet accounts tie, from the tie-out. Under it: which trial balance, from where, when it was captured, and Capture again.
- **Left column:** the decisions and blockers on Books, then the balance sheet against the trial balance. Each account shows its trial balance, what supports it, the difference and its status, with a filter: Differ, No support, Wait on a draft, Tie. Accounts that tie fold to a line after the first three.
- **Rail:** total assets, what differs, exceptions found and accounts moved since last month.
- **More on Books:** links to the tab's other views, each holding its existing section:
  - the ledger and its captures (QuickBooks and Xero);
  - the full tie-out;
  - exceptions;
  - changes from last month;
  - aging;
  - open invoices and bills;
  - documents against the ledger;
  - corrections in QuickBooks.

A view opens with `?tab=books&view=…` or with the anchor of a section inside it (`#quickbooks-ledger`, `#xero`, `#correct-…`, `#apply-…`), so existing links still land on it.

Workpapers (`Workpapers.tsx`):
- **Lede:** how many drafts are ready for review, or need another look, or that every draft is approved. Under it: the current drafts, how many are approved, and that approval is not posting.
- **Left column:** the decisions on Workpapers; drafts ready for approval open in place, as on Overview. Then the drafts as one table: the current versions, newest first, then the documents the agent is still preparing, waiting on the client for, or needing a decision on. Each row has its status and the amount of its adjustment, with a filter: Ready for review, Preparing, Waiting for client, Needs your decision, Needs another look, Approved. The old `?filter=ready|attention|approved` links still choose their filter.
- **Rail:** what waits for approval and what is approved, summed only within one currency, schedules carried from last month and earlier versions.
- **More on Workpapers:** recurring schedules, recurring accruals, every saved draft (earlier versions included) and carrying work into the next month, each its existing section.

Documents (`Documents.tsx`, the tab keyed `evidence`):
- **Lede:** how many of the client's documents the agent has read and how many wait for the person. Under it, whether a forwarding address is open and until when, and where documents are added. The address itself shows only when it is issued.
- **Left column:** the decisions on Documents (facts to confirm, failed preparation, forwarded files to release), then the documents as one table, newest first. Each row shows how the document arrived (`arrived` on the evidence list: upload, email, client request, client reply, Stripe and so on), when, and where its preparation stands (`job_state`, the latest job). A filter groups documents by what the agent read them as (`kind`), and Upload sits on the table's header row. Rows open the document's work (`?job=`) or the source itself. Replaced documents and ledger captures fold to their own lines.
- **Rail:** documents read, pages, invoices not in the ledger yet (from matching) and when the forwarding address closes.
- **More on Documents:** adding, forwarding and comparing documents; QuickBooks, Stripe and Google Drive with their connection state; saved preparation instructions; historical exports.

`#source-<id>` and `#historical-exports` open their views.

Requests (`RequestsTab.tsx`):
- **Lede:** where the requests stand: a reply held until the person checks who sent it, requests that have not reached the client, how many wait on it, a drafted request waiting for approval, or that every one is answered. Under it: to whom, and the longest wait.
- **Left column:** the decisions on Requests, then the requests as one table: the agent's draft waiting for approval, then every approved request, newest first. The list comes from `GET /v1/closes/{id}/requests/approved`, with when each reached the client, any answer, a held reply, its link, reminders and the work it is handled in. Each row shows a title from its questions, the recipient, when it was sent and where it stands: Waiting for client · N days, Partly answered, Reply held for review, Approved · not sent, Not delivered, Link expired, Answered or Withdrawn. A filter sits beside New request; rows open the work the request is handled in.
- **Rail:** replies held, requests not with the client or waiting, and the longest wait.
- **More on Requests:** drafting a request, email replies, and the client's requests across months.

`#client-request` opens the draft view. The canvas's contact name and role, the client's usual reply time and stopping reminders are not shown: no contact or reply-time record exists, and reminders are sent only when a person approves each one.

A records page (Clients, Returns) opens with its title row (title, context, search, one primary action), a filter segment and the table, whose rows open their record: a row on Clients opens the client's page. A client's page (`/dashboard/clients/<client>`) opens like a close: the breadcrumb row with its one action (the month in hand, else the month it owes), the lede on where the client stands, its cards (books, monthly close, currency and year, people), then its months as a table whose rows open each month. Every page has the composer at its foot, scoped to the page.

Below 860px the sidebar is a sheet opened from the brand button, the composer spans the width, and the stage's positions become flow.

## Components

Pages build from these instead of repeating markup and class names. Each takes its values from the tokens; the artifact's component of the same name is the reference for its markup, states and guidelines.

| Component | Use it for |
| --- | --- |
| `Sidebar`, `NavItem`, `NavGroup`, `ChatList`, `Notices`, `SidebarFoot` | The shell's sidebar and its parts, in the fixed order above. |
| `Composer`, `Suggestions`, `ActionsMenu` | The agent's input: the box with plus, scope and send (on Home's stage, or in its own band at a page's foot); the three offers on Home; the plus menu (attach, actions, Preview before acting, Move to the side). |
| `Stage`, `Thread`, `Steps`, `Citations` | Home before the first message; a conversation; what the agent is doing while it runs; where an answer came from. |
| `Breadcrumbs`, `Lede`, `Task`, `DoneLine` | A page's head, its one sentence, the things left, what the agent already did. |
| `RecordsHead`, `Segmented`, `Tabs`, `Table` | A records page: its title row, its filter, page tabs, the table. |
| `Card`, `Stat`, `SectionNav`, `EmptyState` | Settings cards and a client's cards (`Cards` and `Card`; every change in a Dialog), Measure's numbers, a settings page's sections, nothing yet. |
| `Button`, `IconButton`, `Input`, `Choice`, `Chip`, `Mark`, `Kbd` | The primitives: actions, icon-only actions (including send), fields, switches and checkboxes, status chips and dots, client, firm, agent and person marks, shortcut hints. |
| `Dialog`, `Toast`, `Tooltip`, `Skeleton` | One change in a focused window; a confirmation line; a label for an icon-only control; a block before its data arrives. |

`HexMap` and `DotChart` keep their drawing and take their colours from the tokens (parts in `accent` and `client-teal`, empty cells in `surface-side`).

To add a component, put `Name.tsx` and `Name.css` in `packages/ui/components/`, import the stylesheet from the component, use only tokens in it, add it to the artifact with a preview and guidelines, and add a row here. Import it as `@close/ui/components/Name`.

## Rules

- **Use tokens, not literals.** Every colour, radius, spacing step, shadow and size comes from `tokens.css`, named as in the artifact. A new colour becomes a token first, with a usage note in both places.
- **One thing per screen.** A page states where things stand in one sentence, then shows only what is left. Numbers, tables and history come after, or on a records page.
- **Hairlines, not shadows.** A card, a task, a notice, a table, an input and a white button have a 1px `ring` and no shadow. Hover adds depth (`raised`, `lift`), not colour. Only Home's composer and the actions menu float; a page's composer sits in its own band, and a dialog sits on the overlay.
- **Graphite is the action.** One `primary` button per view. The send button, the task numerals and the firm and agent marks are graphite too. Never two primaries, never a primary in a table row.
- **Blue means alive.** `accent` is the working and unread dots, the up-next dot, the wordmark's rule, the focus ring and links. Never a fill behind words, never the current nav item, never a switch.
- **Current is white.** The current page and the current chat are `surface` with a `ring` at weight 500.
- **Status has a word.** `pos`, `att` and `neg` chips pair a text colour with its tint and always carry a word. A client's hue is identity, never status.
- **Two faces, by role.** Geist for everything the person operates. Newsreader only for the wordmark, the greeting, a page's lede and a records title. Never the serif on a control, a table or a label.
- **Corners are concentric.** `radius-3xl` composer, `radius-2xl` dialog, `radius-xl` task and menu, `radius-lg` cards and tables, `radius-md` notices and rows, `radius-sm` nav items, buttons and inputs, `radius-xs` small tiles, `radius-full` pills and dots.
- **Motion is one easing and a short list of durations**, all off under `prefers-reduced-motion`: 120ms hover, 140ms rings, 160ms lifts, 180ms fades, 220ms rise and slide, 240ms folds and pops, 260ms a notice dropping in, 320ms the stage stepping aside, 380ms the composer's glide.
- **Light only.** There is no dark theme, by product decision.
- **Text passes AA.** Text is `ink`, `ink-2` or `ink-3`. `ink-4` is for icons at rest, chevrons and dividers, never words or numbers.
- **Focus is visible** on every control: a 2px `accent` ring offset by 2px of white. A hidden affordance (a row's arrow) appears on focus as well as hover.
- **No inline styles.** A one-off need becomes a class. Anything used on more than one page becomes a component.
- **No dead code.** Both apps' type-checks fail on unused locals; when a class stops being used, delete its rule in the same change.
- **The staff console follows the same rules** with the same sidebar; only patterns no firm page has live in `apps/admin/src/admin.css`.

## The agent comes first

**Behavior contract, reaffirmed 2026-10-10:** follow the build plan's [supervision experience contract](agent-first-build-plan.md#supervision-experience-contract). Preserve this chat-first Home and the current sidebar/client/month layouts. The agent progresses delegated preparation and coordination; the person receives a clear, scoped recommendation and approves, rejects or improves it. Use existing thread/task/proposal components and contextual drill-downs for new capabilities. The contract defines target behavior and acceptance; it does not certify that all of it is implemented.

The [close-page implementation refinements](agent-first-implementation-handoff.md#b02b10--concrete-changes-to-the-close-page-design) preserve the existing tabs and shell, replace generic human tasks with prepared decisions, clarify counts and forecast readiness, and reduce repeated summaries. They also specify that the scoped composer must not obscure page content.

- **Home opens on the composer.** The greeting says how the week stands; the composer is the first control; the three suggestions are the agent's own offers, worded as asks ("What needs me today?"). Send turns the stage into the thread.
- **Every page has the composer at its foot**, scoped to the page ("Acme · September", "Firm-wide"). The scope is a quiet control inside the box; the person can widen it. The box sits on a band of its own below the page (the side surface, a hairline above it), so the page scrolls above it and it never covers a row, a control or a dialog. Hide folds the band to one "Show the agent" line on that page only, for the browser tab; Show, or `/` typed outside a field, brings it back.
- **The composer has three controls**: plus, scope, send. Enter sends, Shift+Enter breaks, the box grows to seven lines. Send is disabled until there is text and becomes Stop while a turn runs. Preview before acting and Move to the side live in the plus menu and the open thread.
- **The agent shows its work.** While it runs: the mark breathes, "Looking at your closes" shimmers, each step slides in with a spinner that becomes a tick, the text streams with a caret. Done: the steps fold to one line with Show, citations follow the text as chips, an action row has at most one primary. The steps come from the chat's existing `application/x-ndjson` event stream ([agent chat](agent-chat.md)); the words stream only once the provider sends text deltas, which that doc says is not yet the case, so until then the text appears whole after the steps, with the same fold, and no caret.
- **Needs you is what the agent noticed**, one line each, at most four showing, with "All notices" beneath for the rest and for taken ones. Notices are live: a long-lived NDJSON request (`/attention/stream`) says when the person's unread items change, and the block reads again; the read with the page and every 60 seconds stand behind it.
- **Chats are titles.** No previews, timestamps or avatars in the list. A working chat pulses; one that answered shows a steady dot.
- **What the agent wrote carries its mark**, including the morning brief.
- **New suggested work shows its scope before confirmation.** Already delegated internal preparation and continuation run without repeated prompts or per-step confirmations. Messages, ledger writes, publication and sign-off retain the build plan's explicit authority boundaries.
- **Opening is not resolving.** Taking a sidebar notice marks it read; an unresolved accounting decision remains in the current close/decision view until its actual resolution. Notices, chat cards and close tasks refer to the same durable work. One changed source can group related decisions, with each required approval still explicit.

## Icons

Icons come from Hugeicons free, Stroke Rounded (MIT), copied from `@hugeicons/core-free-icons`. Each entry in `packages/ui/components/Icon.tsx` is the icon's element list as published, so it can be compared with the package. To add one:

1. Find the icon at hugeicons.com (free set, Stroke Rounded) and note its name, for example `CalendarCheck01`.
2. Copy its element list from `@hugeicons/core-free-icons/dist/esm/<Name>Icon.js` into `icons` in `packages/ui/components/Icon.tsx` under a short product name (`calendar-check`), dropping each element's `key`.
3. Use it as `<Icon name="calendar-check" />`. Icons are 16px in nav, controls, notices and chips, 18px on round buttons, 14px for chevrons and hidden arrows. `ink-4` at rest, `ink` when current, the tint's text colour on a tinted tile, white on the send button and marks.

## Checking the layout

`scripts/layout-check.mjs` opens every page of both apps in Chrome, at desktop and phone width (a synthetic firm is added straight to the database, since only the sign-in provider creates real ones, and the client request page's one read is answered in the browser with an open request), and fails when a page is wider than a phone's screen, a page throws, or the firm app's shell takes any style from the public site's `styles.css`. It waits for the sidebar (`.app-shell .ks-side`) before reading a page, finds the page's title through `.page-header h1` (the page header is the records title row; a close's lede comes with the close-page PR), opens the agent through New chat (`.ks-new-chat`, or the phone bar's `.ks-phonebar-new`) on Home and close routes, and asserts Krith's geometry: on a desktop the sidebar is one full-height column at the left of the page, 48–272px wide, with its sections in the fixed order (brand, firm line, New chat, pages, the app's sections, foot); on a phone the sidebar is off the screen and the phone bar spans the width with the page below it. It also asserts that the composer is in view on every workspace page (the stage's on Home, the box at the foot elsewhere). CI runs it whenever `apps/`, `packages/` or the dependencies change. Locally, start the API and both apps' dev servers, then run `WORKSPACE_TOKEN=… DATABASE_URL=… npm run check:layout` (`CHROME_PATH` picks another Chromium browser, `FRONTEND_URL`, `ADMIN_URL` and `API_URL` other ports). A new page joins the check by adding its route to the script.

## Rollout

The workspace moved to Tray in PRs #205–#222 and took the top bar and rail on 2026-10-07 ([workspace simplification](workspace-simplification.md)). Krith was chosen on 2026-10-10. It lands in this order, each step a PR that keeps both apps green and the layout check passing: tokens and fonts (#336, Krith's tokens under their names, with Tray's names aliased to them so unmoved pages render on Krith's values at once); the sidebar shell (#340, replacing the top bar and rail in both apps, with the layout check updated; the firm line shows the sign-in domain until the firm's name is kept; Tax and Reports join the pages as their pages land); the records pages (Clients at `/dashboard/clients` with every caller moved, the Closes decisions page at `/dashboard/closes` with Home's counts, trust strip and decision list rendered there as well as on Home, the Closes item carrying the count, and the shared element styles, buttons, inputs, chips, cards, the page header, segments and section tabs, moved to Krith for every page at once); the composer and the Home stage (Home's cards, trust strip and lists leave for Closes; the page composers on a close's Overview, Reports and Cash plan go, since the composer is at the foot of every page; the drawer stays the thread beside a page and gets the same box); the close page (the breadcrumb row with Sign off as its one action, enabled by readiness and the reader's role; the lede counting the areas that need the person; the tasks as numbered cards; what is done folded to a line; the close's pages as tabs; the plan in a dialog; the draft counts moved to Workpapers); the thread's motion on the existing NDJSON events (the mark breathes, the line shimmers, steps slide in and tick, the answer rises; words stream once the provider sends deltas); the live Needs you block (one long-lived NDJSON request, `GET /v1/closes/attention/stream`, says when the person's unread items change; the sidebar reads the inbox again then, and opens the request again when it ends). With every page on Krith, the Tray aliases in `tokens.css` were renamed to their Krith tokens at every use and deleted; `avatar`, `icon-tile` and `page-width` stayed as Krith sizes. No Tray name remains in the code. A screen added while the rollout is under way uses Krith's tokens and the Krith component where one exists, the Tray component otherwise, and adds no new Tray-only token or class.
