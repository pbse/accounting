# Workspace design system (Tray)

The product (everything under `/dashboard`, and the client request page at `/request`) uses **Tray**: a light-only look with a grey ground, white cards and grey fills inside them, Inter Display type and Hugeicons. Marketing and legal pages keep their own styles; Tray is scoped to `.app-shell`, the workspace layout's root. The client request page uses `.app-shell.standalone`, a single centred column without the sidebar.

The reference for every value is the Tray design system: tokens, type scale, components and icon guidance were measured from the dashboard the product is modelled on.

## Where things live

| What | File | Change it when |
| --- | --- | --- |
| Tokens: colours, radii, spacing, sizes, shadows, fonts | `apps/frontend/src/theme/tokens.css` | A colour, radius or spacing step changes for the whole workspace. |
| Workspace layout and shared patterns | `apps/frontend/src/theme/workspace.css` | The shell, the sidebar, the page header or a shared pattern (cards, stat tiles, rows, pills, the assistant) changes. |
| Font files (Inter, OFL) | `apps/frontend/src/theme/fonts/` | Inter is upgraded. Keep the Latin and Latin Extended subsets with their `unicode-range`s. |
| Shared components, each with its stylesheet beside it | `apps/frontend/src/ui/` | A pattern used on more than one page changes. |
| Icons | `apps/frontend/src/ui/Icon.tsx` | An icon is added or replaced. |
| Public and legacy element styles | `apps/frontend/src/styles.css` | Marketing or legal pages change. |

`main.tsx` imports `styles.css`, then `theme/tokens.css`, then `theme/workspace.css`, so workspace rules override the public element styles inside the shell. Each component imports its own stylesheet, which loads with the page that uses it.

## Components

Pages build from these instead of repeating markup and class names. Each takes its values from the tokens.

| Component | Use it for |
| --- | --- |
| `PageHeader` | The 68px bar at the top of a page: `icon` or a client's `avatar`, `title`, a one-line description as children, and `actions` on the right. |
| `SectionHeader` | A heading above a block, with an optional aside (a count, a note or actions) as children. `level={1}` when it is the page's only heading. |
| `StatGroup`, `StatTile` | A row of numbers. A tile with `to` is a link, with `onClick` a button, with neither it only reads. |
| `Segmented` | A set of toggles (`onChange`) or page tabs (each item's `to`), with optional counts and an alert dot. |
| `RowList`, `EntityCell` | A list of rows with optional column names; `EntityCell` is a row's first cell with the client's avatar or another leading mark. Rows themselves are `.row` links, summaries or blocks. |
| `IconTile`, `Avatar`, `Icon` | A glossy 32px icon tile in a tone, a client's initial in its colour, and an icon by name. |
| `AgentComposer` | The card that leads a page with the agent: title, what it can do, a large input, one-click asks and the caller's results below. |
| `Suggestions` | One-click asks as pills. The caller asks the picked text exactly as written; a suggestion never runs an action without its preview. |
| `AgentMark` | The agent's blue tile; with children it leads a label, such as the heading of something the agent wrote. |

To add a component, put `Name.tsx` and `Name.css` in `ui/`, import the stylesheet from the component, use only tokens in it, and add a row here.

## Rules

- **Use tokens, not literals.** `workspace.css` takes every colour, radius, spacing step and shadow from `tokens.css`. A new colour becomes a token first, with a comment saying where it is used.
- **Cards are white on the grey ground.** A section is a white card (`--surface`, `--radius-md`, no border). A section inside a card, a stat tile and a list row are white tiles with a hairline ring (`--shadow-tile`, `--radius-sm`). Grey fills (`--tile`, `--fill`) are for notes, inset controls and chat bubbles, never for cards.
- **Concentric corners.** Anything nested 4px inside a `--radius-md` container uses `--radius-sm`.
- **One type family.** Inter at optical size 32 at every size, weights 400 and 500 (600 for bold text and badge figures). Page titles are 20px, section titles 17px, card titles 15px, body 14px, controls and labels 13px, captions 11px.
- **Colour means something.** `--blue` is the brand and the focus ring. `--green` and `--red` paint status dots and delta discs; words use `--positive-text` and `--negative-text`, which pass AA. Avatar and chart hues (`--teal`, `--pink`, `--orange`, `--violet`) never carry status.
- **Light only.** There is no dark theme, by product decision.
- **No inline styles.** A one-off need becomes a class: `.pre-line` keeps a message's line breaks, `.block` puts a line on its own, `.scroll-text` lets long source text scroll in its box (a `pre` already wraps), `.evidence-frame` sizes an embedded PDF. Anything used on more than one page becomes a component.
- **Every page opens with `PageHeader`**, titled as the sidebar names the page. A form or panel placed straight on a page is a white card.

## The agent comes first

The agent is how people get work done, so it is never more than one click away:

- **Work opens with "Ask the agent"** (`AgentComposer`), above the numbers. Its suggestions only filter the page or show the server's exact preview; nothing runs until a person confirms.
- **"Ask the agent" sits in the sidebar** under Essentials and opens the agent panel with the cursor in its input. The floating button says the same, in the brand blue.
- **The panel starts the conversation**: an empty conversation offers starter questions the panel's fixed reads answer ("What needs me today?", or inside a close "What blocks sign-off?").
- **What the agent wrote carries its mark** (`AgentMark`), as the morning brief does.

## Icons

Icons come from Hugeicons free, Stroke Rounded (MIT), copied from `@hugeicons/core-free-icons`. Each entry in `ui/Icon.tsx` is the icon's element list as published, so it can be compared with the package. To add one:

1. Find the icon at hugeicons.com (free set, Stroke Rounded) and note its name, for example `CalendarCheck01`.
2. Copy its element list from `@hugeicons/core-free-icons/dist/esm/<Name>Icon.js` into `icons` in `ui/Icon.tsx` under a short product name (`calendar-check`), dropping each element's `key`.
3. Use it as `<Icon name="calendar-check" />`. Icons are 16px in navigation, controls and card headers and 24px beside a page title.

## Rollout

The workspace moves to Tray in small pull requests:

1. **Foundation**: tokens, fonts, icons and the restyle of the shell and shared patterns.
2. **Shared components**: page header, stat tiles, segmented control, section header and row list as React components in `apps/frontend/src/ui/`, replacing the copies on each page.
3. **Agent first**: the agent leads Work, sits in the sidebar and offers starter questions in its panel.
4. **Remaining pages**: example pages, the client request page and the last inline colours.
5. **Clean-up**: remove styles and code no page uses.
