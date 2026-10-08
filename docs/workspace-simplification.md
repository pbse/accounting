# Workspace simplification: the home, the shell and what a person sees

Decided on 2026-10-07 after an audit of every workspace page. The audit found that the Work page stacked thirteen sections, five tiles, an agent console and eight filters on one screen; that Attention duplicated it as a second inbox; that Client closes opened with a form; that a close's Overview stacked twelve components; that Firm settings put five policy sections above the client roster; and that user-facing copy named internal things (model IDs, token budgets, permission scopes, provider names). A new firm saw nearly all of it as empty states and explanatory prose.

The [build plan](agent-first-build-plan.md#seamless-agent-first-experience) and the [dashboard specification](maturity.md#implementation-re-review-and-dashboard-specification-2026-10-02) already say what the home should be: four outcome cards, a trust strip and one prioritized decision list, in the shared shell, with detail behind progressive disclosure. This document sequences that work. It changes what is shown and where, never what the server allows: every permission, preview, confirmation and audit record stays as it is.

## The look

The workspace keeps the Tray system ([design system](design-system.md)) and takes its colours, edges and layout from a cleaner reference: a light grey ground, white cards with 16px corners and a soft shadow, a top bar with the navigation as pills and the current page in solid blue, tinted status pills, icons in tinted circles, and one large page title. The sidebar is gone; the page is one centred column. Both apps share the change through `packages/ui`.

## Sequence

One pull request per row, each reviewed by Codex until clean and checked in the browser at desktop and phone width in both apps.

| PR | Scope | Done when |
| --- | --- | --- |
| 1 Shell | Tokens, shell, shared components and both apps' layouts in the new look; a top bar in place of the sidebar; the firm app's navigation cut to Work, Attention, Clients and Firm (Security and Measure open from Firm; the preparation forms open from a close; the examples keep their routes); the layout check asserts the top bar. | Every page of both apps passes the layout check; no gloss, graphite or sidebar rule remains. |
| 2 Home | `GET /v1/closes/home` with the four outcome counts and the trust strip; a Home page of four cards, the trust strip and one decision list grouped by client (from the attention events), with "Ready for review" bulk approval and "Handed to the agent" below it only when they have rows; Work's firm-wide branch and the Attention page retired (their routes open Home); the per-close Work inbox kept. | A new firm's home shows the four cards, a one-line trust strip and one "Add your first client" action, and nothing else. |
| 3 Clients | The Clients page lists the roster with each client's open and owed months, opens a month with one tap, and adds a client by name alone; the create form and the agent card go. | A client is added and its first month opened in two taps. |
| 4 Close | Overview = where it stands, next decisions, blockers; the books sections (exceptions, aging, open items, matching, tie-out, changes) under a Books tab; corrections, recurring, carry-forward and replay under More; one "Prepare by hand" menu in place of nine header buttons; budget, attempt and refresh text removed from work items. | Overview fits in two screens with no section the person cannot act on. |
| 5 Firm | Firm settings opens with people, clients and email; policies in one collapsed block; agent access once; Security's operator tables collapsed under Advanced; Measure hides its zero-state paragraphs. | A new firm's Firm page shows three things to do. |
| 6 Copy | No internal names in user-facing text; no caveat paragraph above a list; no refresh notes; state words only. | `grep` for PRD, WorkOS, MCP, token, reasoning, context-machine and permission scopes finds none in `apps/frontend/src/features` outside Security and agent access. |

## What stays reachable

Nothing is deleted from the server. Routes that leave the navigation (the preparation forms, Measure, Security, the examples) still open by link. Portfolio goals ("prepare my clients due Friday") stay available through the agent panel. The Attention preferences (which kinds count, browser notifications) move to the Home decision list's settings.

## Record

Progress is recorded in [implementation-backlog.md](implementation-backlog.md) and validation evidence in [validation.md](validation.md).
