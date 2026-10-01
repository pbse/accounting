# Workspace design system (Tray)

The product workspace (everything under `/dashboard`) uses **Tray**: a light-only look with a grey ground, white cards and grey fills inside them, Inter Display type and Hugeicons. Marketing pages and the client request page keep their own styles; Tray is scoped to `.app-shell`, the workspace layout's root.

The reference for every value is the Tray design system: tokens, type scale, components and icon guidance were measured from the dashboard the product is modelled on.

## Where things live

| What | File | Change it when |
| --- | --- | --- |
| Tokens: colours, radii, spacing, sizes, shadows, fonts | `apps/frontend/src/theme/tokens.css` | A colour, radius or spacing step changes for the whole workspace. |
| Workspace layout and shared patterns | `apps/frontend/src/theme/workspace.css` | The shell, the sidebar, the page header or a shared pattern (cards, stat tiles, rows, pills, the assistant) changes. |
| Font files (Inter, OFL) | `apps/frontend/src/theme/fonts/` | Inter is upgraded. Keep the Latin and Latin Extended subsets with their `unicode-range`s. |
| Icons and avatars | `apps/frontend/src/features/dashboard/Icon.tsx` | An icon is added or replaced. |
| Public and legacy element styles | `apps/frontend/src/styles.css` | Marketing, legal or client request pages change. |

`main.tsx` imports `styles.css`, then `theme/tokens.css`, then `theme/workspace.css`, so workspace rules override the public element styles inside the shell.

## Rules

- **Use tokens, not literals.** `workspace.css` takes every colour, radius, spacing step and shadow from `tokens.css`. A new colour becomes a token first, with a comment saying where it is used.
- **Cards are white on the grey ground.** A section is a white card (`--surface`, `--radius-md`, no border). A section inside a card, a stat tile and a list row are white tiles with a hairline ring (`--shadow-tile`, `--radius-sm`). Grey fills (`--tile`, `--fill`) are for notes, inset controls and chat bubbles, never for cards.
- **Concentric corners.** Anything nested 4px inside a `--radius-md` container uses `--radius-sm`.
- **One type family.** Inter at optical size 32 at every size, weights 400 and 500 (600 for bold text and badge figures). Page titles are 20px, section titles 17px, card titles 15px, body 14px, controls and labels 13px, captions 11px.
- **Colour means something.** `--blue` is the brand and the focus ring. `--green` and `--red` paint status dots and delta discs; words use `--positive-text` and `--negative-text`, which pass AA. Avatar and chart hues (`--teal`, `--pink`, `--orange`, `--violet`) never carry status.
- **Light only.** There is no dark theme, by product decision.

## Icons

Icons come from Hugeicons free, Stroke Rounded (MIT), copied from `@hugeicons/core-free-icons`. Each entry in `Icon.tsx` is the icon's element list as published, so it can be compared with the package. To add one:

1. Find the icon at hugeicons.com (free set, Stroke Rounded) and note its name, for example `CalendarCheck01`.
2. Copy its element list from `@hugeicons/core-free-icons/dist/esm/<Name>Icon.js` into `icons` under a short product name (`calendar-check`), dropping each element's `key`.
3. Use it as `<Icon name="calendar-check" />`. Icons are 16px in navigation, controls and card headers and 24px beside a page title.

## Rollout

The workspace moves to Tray in small pull requests:

1. **Foundation**: tokens, fonts, icons and the restyle of the shell and shared patterns.
2. **Shared components**: page header, stat tiles, segmented control, section header and row list as React components in `apps/frontend/src/ui/`, replacing the copies on each page.
3. **Agent first**: the agent leads Work, sits in the sidebar and offers starter questions in its panel.
4. **Remaining pages**: example pages, the client request page and the last inline colours.
5. **Clean-up**: remove styles and code no page uses.
