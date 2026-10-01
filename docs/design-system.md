# Workspace design system (Tray)

The product workspace (everything under `/dashboard`) uses **Tray**: a light-only look with a grey ground, white cards and grey fills inside them, Inter Display type and Hugeicons. Marketing pages and the client request page keep their own styles; Tray is scoped to `.app-shell`, the workspace layout's root.

The reference for every value is the Tray design system: tokens, type scale, components and icon guidance were measured from the dashboard the product is modelled on.

## Where things live

| What | File | Change it when |
| --- | --- | --- |
| Tokens: colours, radii, spacing, sizes, shadows, fonts | `apps/frontend/src/theme/tokens.css` | A colour, radius or spacing step changes for the whole workspace. |
| Workspace layout and shared patterns | `apps/frontend/src/theme/workspace.css` | The shell, the sidebar, the page header or a shared pattern (cards, stat tiles, rows, pills, the assistant) changes. |
| Font files (Inter, OFL) | `apps/frontend/src/theme/fonts/` | Inter is upgraded. Keep the Latin and Latin Extended subsets with their `unicode-range`s. |
| Shared components, each with its stylesheet beside it | `apps/frontend/src/ui/` | A pattern used on more than one page changes. |
| Icons | `apps/frontend/src/ui/Icon.tsx` | An icon is added or replaced. |
| Public and legacy element styles | `apps/frontend/src/styles.css` | Marketing, legal or client request pages change. |

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

To add a component, put `Name.tsx` and `Name.css` in `ui/`, import the stylesheet from the component, use only tokens in it, and add a row here.

## Rules

- **Use tokens, not literals.** `workspace.css` takes every colour, radius, spacing step and shadow from `tokens.css`. A new colour becomes a token first, with a comment saying where it is used.
- **Cards are white on the grey ground.** A section is a white card (`--surface`, `--radius-md`, no border). A section inside a card, a stat tile and a list row are white tiles with a hairline ring (`--shadow-tile`, `--radius-sm`). Grey fills (`--tile`, `--fill`) are for notes, inset controls and chat bubbles, never for cards.
- **Concentric corners.** Anything nested 4px inside a `--radius-md` container uses `--radius-sm`.
- **One type family.** Inter at optical size 32 at every size, weights 400 and 500 (600 for bold text and badge figures). Page titles are 20px, section titles 17px, card titles 15px, body 14px, controls and labels 13px, captions 11px.
- **Colour means something.** `--blue` is the brand and the focus ring. `--green` and `--red` paint status dots and delta discs; words use `--positive-text` and `--negative-text`, which pass AA. Avatar and chart hues (`--teal`, `--pink`, `--orange`, `--violet`) never carry status.
- **Light only.** There is no dark theme, by product decision.

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
