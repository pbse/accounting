# Workspace design system (Tray)

The product (everything under `/dashboard`, and the client request page at `/request`) uses **Tray**: a light-only look with a light grey ground, white cards with soft shadows and 16px corners, pills for navigation, buttons and status, icons in tinted circles, Inter Display type and Hugeicons. Marketing and legal pages keep their own styles; Tray is scoped to `.app-shell`, the workspace layout's root. The client request page uses `.app-shell.standalone`, a single centred column without the top bar.

The reference for every value is the Tray design system: tokens, type scale, components and icon guidance were measured from the dashboards the product is modelled on, and refreshed on 2026-10-07 to the cleaner reference in [workspace simplification](workspace-simplification.md).

## Where things live

| What | File | Change it when |
| --- | --- | --- |
| Tokens: colours, radii, spacing, sizes, shadows, fonts | `packages/ui/theme/tokens.css` | A colour, radius or spacing step changes for both apps. |
| The shell both apps share: the top bar, the page column, element styles | `packages/ui/theme/shell.css` | The shell, the top bar or a shared element style (cards, buttons, fields, tables, pills) changes. |
| Font files (Inter and, for the word mark, Newsreader; both OFL) | `packages/ui/theme/fonts/` | Inter or Newsreader is upgraded. Keep the Latin and Latin Extended subsets with their `unicode-range`s. |
| Shared components, each with its stylesheet beside it | `packages/ui/components/` | A pattern used on more than one page or app changes. |
| Icons | `packages/ui/components/Icon.tsx` | An icon is added or replaced. |
| The firm app's own patterns (preparation forms, evidence pane, Work's rows, Attention, the agent panel) | `apps/frontend/src/theme/workspace.css` | One of those changes. |
| The staff console's own patterns (workflow flow, inspector, schema forms) | `apps/admin/src/admin.css` | One of those changes. |
| The browser toolbar colour on shell pages | `packages/ui/theme/useShellThemeColor.ts` | A new shell page opens outside the firm app's workspace layout. |
| The public site's styles (its own system: `k-` classes under `.k-site`; see [public site](marketing-site.md#design)) | `apps/frontend/src/styles.css` | A public page changes. |

Both apps import the design system as `@close/ui/...`, an alias to `packages/ui` set in each app's `vite.config.ts` and `tsconfig.json`. Each `main.tsx` imports `@close/ui/theme/tokens.css`, `@close/ui/theme/shell.css` and then its own stylesheet; the firm app imports the public site's `styles.css` first, for its marketing pages. Each component imports its own stylesheet, which loads with the page that uses it.

## Components

Pages build from these instead of repeating markup and class names. Each takes its values from the tokens.

| Component | Use it for |
| --- | --- |
| `PageHeader` | The title row at the top of a page: `icon` or a client's `avatar`, the 24px `title`, a one-line description as children, and `actions` on the right. |
| `BackLink` | The way back to the page this one belongs to, above its header. |
| `SectionHeader` | A heading above a block, with an optional aside (a count, a note or actions) as children. `level={1}` when it is the page's only heading. |
| `StatGroup`, `StatTile` | A row of number cards. A tile with `to` is a link, with `onClick` a button, with neither it only reads; the icon and caption are optional. |
| `HexMap` | A whole split into up to four parts that never overlap, as clusters of hex cells with a legend of counts and shares that add up to 100 (Measure's track record). Its label states every share for screen readers.
| `DotChart` | One series over time as dot columns over a full grid of empty dots (Measure's median human minutes by month). A point with no value stays empty rather than reading as zero; its label states every point.
| `Segmented` | A set of toggles (`onChange`) or page tabs (each item's `to`), with optional counts and an alert dot. |
| `VerticalTabs` | A settings page's sections as a column of links, under their group names, beside the selected section's cards (Settings); on a phone the column becomes a strip above that scrolls sideways. |
| `SettingGrid`, `SettingCard`, `StatusChip` | Cards of settings side by side: each its icon in a tinted circle, its state as a chip with a dot, its name, one line on where it stands, and an action at its foot (Settings → Policies). |
| `Dialog`, `DialogActions` | A focused window for one change: a title, one line on what it does, the form and its actions at the foot; the browser's own modal, closed by Escape, the backdrop or Cancel. Every change on the Settings page is made in one, never in a fold-out. |
| `RowList`, `EntityCell` | A card of rows with optional column names; `EntityCell` is a row's first cell with the client's avatar or another leading mark. Rows themselves are `.row` links, summaries or blocks. |
| `IconTile`, `Avatar`, `Icon` | A 36px tinted circle with an icon in its hue (blue, orange, teal, green, violet, red or gray), a client's initial in its colour, and an icon by name. |
| `AgentComposer` | The card that leads a page with the agent: title, what it can do, a large input, one-click asks and the caller's results below. |
| `Suggestions` | One-click asks as pills. The caller asks the picked text exactly as written; a suggestion never runs an action without its preview. |
| `AgentMark` | The agent's blue tile; with children it leads a label, such as the heading of something the agent wrote. |

To add a component, put `Name.tsx` and `Name.css` in `packages/ui/components/`, import the stylesheet from the component, use only tokens in it, and add a row here. Import it as `@close/ui/components/Name`.

## Rules

- **Use tokens, not literals.** `workspace.css` takes every colour, radius, spacing step and shadow from `tokens.css`. A new colour becomes a token first, with a comment saying where it is used.
- **Cards are white on the grey ground.** A section is a white card (`--surface`, `--radius-md`, `--shadow-card`, no border). A section inside a card and a list row are white tiles with a hairline ring (`--shadow-tile`, `--radius-sm`). Grey fills (`--tile`, `--fill`) are for notes, inset controls, segmented tracks and chat bubbles, never for cards.
- **Pills everywhere round.** Buttons, navigation items, segments, status pills and chips use `--radius-full`. The one primary action on a view is solid `--blue` with white words; every other button is white with a hairline ring. The current page and the selected segment are solid blue too.
- **Concentric corners.** Anything nested inside a `--radius-md` container uses `--radius-sm`.
- **One type family.** Inter at optical size 32 at every size, weights 400, 500 and 600. Page titles are 24px semibold, section titles 17px, card titles 15px, body 14px, controls and labels 13px, captions 11–12px; column names and eyebrows are 11px uppercase.
- **Colour means something.** `--blue` is the brand, the current page, the primary button and the focus ring. `--green` and `--red` paint status dots and bars; words use `--positive-text` and `--negative-text`, which pass AA on their tints. Icons sit in tinted circles (`IconTile`) whose hue says what the number is about. `--spotlight` marks the one thing to do first. Avatar and chart hues (`--teal`, `--pink`, `--orange`, `--violet`) never carry status.
- **Light only.** There is no dark theme, by product decision.
- **Text passes AA.** Text is `--ink` or `--ink-2` (7:1 on white); `--ink-3` is for dots, dividers and icons, never words or numbers.
- **The shell stands alone.** `shell.css` sets every element style its pages rely on, so the public site's `styles.css` can change without touching the product: the staff console doesn't load it at all, and the firm app's product rules live in `workspace.css`.
- **No inline styles.** A one-off need becomes a class: `.pre-line` keeps a message's line breaks, `.block` puts a line on its own, `.scroll-text` lets long source text scroll in its box (a `pre` already wraps), `.evidence-frame` sizes an embedded PDF. Anything used on more than one page becomes a component.
- **Every page opens with `PageHeader`**, titled as the top bar names the page. A form or panel placed straight on a page is a white card.
- **No dead code.** Both apps' type-checks fail on unused locals and parameters (`noUnusedLocals`, `noUnusedParameters`); when a class stops being used, delete its rule in the same change.
- **The staff console follows the same rules.** Its pages open with `PageHeader` and use the shared components; its navigation sits in the same top bar with icons like the firm app's; only patterns no firm page has (the workflow flow and inspector, schema forms, filter rows, grants) live in `apps/admin/src/admin.css`.

## Checking the layout

`scripts/layout-check.mjs` opens every page of both apps in Chrome, at desktop and phone width (a synthetic firm is added straight to the database, since only the sign-in provider creates real ones, and the client request page's one read is answered in the browser with an open request, since a request really sent needs a client's preparation work behind it), and fails when a page has no `PageHeader`, the top bar is not one full-width row on a desktop (or at most two on a phone) with the page column below it, the rail is not a narrow pill at the left of the page column on a desktop (or shows on a phone), a page is wider than a phone's screen, a page throws, or the firm app's shell takes any style from the public site's `styles.css`. CI runs it whenever `apps/`, `packages/` or the dependencies change. Locally, start the API and both apps' dev servers, then run `WORKSPACE_TOKEN=… DATABASE_URL=… npm run check:layout` (`CHROME_PATH` picks another Chromium browser, `FRONTEND_URL`, `ADMIN_URL` and `API_URL` other ports). A new page joins the check by adding its route to the script.

## The agent comes first

The agent is how people get work done, so it is never more than one click away:

- **Home opens with the outcomes**, not a prompt: the four cards, the trust strip and the decision list. The agent is one click away in the top bar, and portfolio goals ("prepare my clients due Friday") are asked in its panel, which shows the server's exact preview before anything runs.
- **A close's Overview opens with "Ask the agent"** until the close is handed over: the hand-over and the panel's close starter questions as suggestions, and any question opens the agent panel on that close. Once handed over, the same place shows the agent's progress.
- **Attention and Client closes open with "Ask the agent" too**, with the panel's firm starter questions; a question opens the panel's firm-wide conversation. These pages and the close's Overview share `AskAgent` (`apps/frontend/src/features/dashboard/AskAgent.tsx`), which hands every question to the panel, so nothing runs from the page itself.
- **Chats are many**: the rail's chats item (a history icon above Agent) opens a small menu with "New chat", the five latest chats (title over scope) and "All chats"; the panel's header has "+" and a chats button that swaps the conversation for the full list (grouped Today, Yesterday, This week, Earlier, with a filter); the chat's title sits under the scope chip and is renamed in place from the menu.
- **"Agent" sits in the top bar**, the one blue button there, and in the rail, and opens the agent panel with the cursor in its input; closing the panel hands focus back to it.
- **The panel starts the conversation** (for an executive: their three questions, "Your business" as the scope chip, no command list): an empty conversation offers only the scope chip and three starter chips, which send commands the fixed reads answer ("What needs me today?", or inside a close "What blocks sign-off?"); on a page that leads with its own `AskAgent` composer, the panel shows no chips of its own.
- **The agent panel is a conversation, not a help widget** (`apps/frontend/src/features/dashboard/Assistant.tsx`): a rounded composer with the send button inside it (Enter sends, Shift+Enter breaks, grows to five lines), "/" opening the palette's command list inline (a listbox the box names through `aria-activedescendant`; arrows choose, Tab completes the text, Enter sends), Clear under the header's menu, one line under the composer before the first message and the full statement behind the header's info button. Member text sits in a tinted bubble on the right; the agent's text is plain on the left with the small mark, in paragraphs, lists and bold only, with citation chips below. While an answer runs, the steps the agent makes show one by one (✓ read, … reading, – refused), then collapse to a line on the saved answer; three dots say it is thinking and "Waiting for the agent" after three seconds without a step. A card is the message. On a phone the panel is a full-screen sheet with a back button and the composer above the home indicator. The drawer slides in and messages rise 4px, all off under `prefers-reduced-motion`. The name is "Agent" everywhere: the header, the top bar and the rail.
- **What the agent wrote carries its mark** (`AgentMark`), as the morning brief does.

## Icons

Icons come from Hugeicons free, Stroke Rounded (MIT), copied from `@hugeicons/core-free-icons`. Each entry in `packages/ui/components/Icon.tsx` is the icon's element list as published, so it can be compared with the package. To add one:

1. Find the icon at hugeicons.com (free set, Stroke Rounded) and note its name, for example `CalendarCheck01`.
2. Copy its element list from `@hugeicons/core-free-icons/dist/esm/<Name>Icon.js` into `icons` in `packages/ui/components/Icon.tsx` under a short product name (`calendar-check`), dropping each element's `key`.
3. Use it as `<Icon name="calendar-check" />`. Icons are 16px in navigation, controls and card headers and 24px beside a page title.

## The shell

Both apps share one shell: a sticky top bar (`.topbar`) with the brand pill (the Krith word mark, `.wordmark`, in the public site's serif over its double blue rule), the navigation as pills (`nav` of `.nav-item`s, the current one solid blue) and the tools on the right (`.topbar-tools`: the client search and "Agent" in the firm app, and an account menu, `.topbar-account`, in both; the firm app’s menu also opens the member’s profile); under it the body row (`.app-body`): a floating rail down the left (`.rail`, `--rail-width`, the same places as icons in `.rail-group`s, the current one a blue square, and at its foot what the firm app asks of the agent) beside the page column (`.app-main > .page`), one centred column up to `--page-width` wide. Below 1100px the navigation takes a second row and scrolls sideways and the rail gives way to it; on a phone the bar also loses its subtitle, the account name and the search (the Clients page has its own), and the blue button keeps its icon alone. An open content grant shows on the staff console's account button at every width. The firm app's bar names three pages, Home, Clients and Settings (and the worked example for Krith's own firm); Settings holds the person's and the firm's sections (people, clients, policies and security), Measure opens from its Firm section, the preparation forms from a close. The staff console's bar names its ten.

## Rollout

The workspace moved to Tray in PRs #205–#222 (tokens, shared components, the agent first, the shared package and the staff console). On 2026-10-07 the shell took the cleaner reference look and the top bar; the pages themselves follow in [workspace simplification](workspace-simplification.md).
