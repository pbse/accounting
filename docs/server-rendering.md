# Server rendering

Workspace pages are rendered on the server, at the edge, with their data. The browser receives the finished page and
hydrates it. This document is the architecture; [page speed](performance.md) has the measurements that led here.

## The pattern

The app follows React Router's data-router pattern with TanStack Query as its data layer:

1. **Routes declare their data.** `apps/frontend/src/routes.tsx` defines every route. A page's code loads with `lazy`,
   in parallel with its `loader`. A loader prefetches the queries its page reads.
2. **Every read is a query keyed by its API path.** `apps/frontend/src/data/queries.ts` defines the query client, the
   key (`["workspace", path]`) and `prefetch`. Components read with `useQuery(workspaceQuery(path))`. Every change
   made through `workspaceRequest` marks all workspace queries stale (`onWorkspaceWrite` in `main.tsx`), so whatever
   is on screen reads again and the rest on its next visit. An explicit Refresh calls `refetch`.
3. **The API stays one endpoint per resource.** Pages compose resources; the server never adds a page-shaped endpoint.
   A resource returns only what its readers need. The People section, for example, reads `/security/clients`, the
   assignable clients alone, not the whole security read.

## On the server

The edge Worker (`workers/edge/src/workspace.ts`) handles `/dashboard` and every path under it:

1. Without a session cookie, or with `AUTH_SESSION` other than `server`, it returns the ordinary app page.
2. Otherwise it calls `render` from the frontend's server build (`apps/frontend/src/entry-server.tsx`). React
   Router's static handler runs the matched routes' loaders. Their reads go to the API through the Worker's binding,
   in parallel, each with the person's cookie, through the same session and firm checks as any request.
3. Nobody signed in means the ordinary app page, which signs in.
4. React renders the page from the query cache. Reads that failed are dropped, so the browser asks again and shows
   that section's own error.
5. The page goes out with the query cache (`<script id="krith-query" type="application/json">`) and the loaders'
   results (React Router's hydration script).

Any failure falls back to the ordinary app page, logged without the page's data.

The response is the person's own data, so it is sent with:

| Header | Value |
| --- | --- |
| `Cache-Control` | `private, no-store` |
| `Vary` | `Cookie` |
| `Content-Security-Policy` | `script-src 'self' 'nonce-…'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'` |
| `X-Frame-Options` | `DENY` |

The nonce is new on every response and marks the router's hydration script and React's inline scripts.

## In the browser

`apps/frontend/src/main.tsx` restores the query cache and React Router's hydration data. It loads the current route's
code, then hydrates. Later navigations run the same loaders in the browser. The page's data is read before it renders,
and a read the cache already holds is not repeated.

## Rules for code that renders on the server

- **No browser APIs during render.** Use `window`, `document`, `localStorage` and `Notification` only in effects and
  event handlers.
- **External stores need a server value.** `useSyncExternalStore` needs a third argument.
- **Dates come from `useDates()`** (`apps/frontend/src/data/time.ts`). It formats in one locale and the person's own
  time zone from their settings, so the server and the browser write the same text. `toLocaleString()` would differ
  and React would discard the server's HTML.
- **Nothing per request in module state.** One Worker renders pages for different people at once. Per-request data
  lives in the query client and the router context created for that render.

## Status

| Page | Rendered on the server with its data |
| --- | --- |
| Workspace layout (executive check, worked example, unread count, the person's settings) | Yes |
| Settings: Profile, Notifications, Connections | Yes |
| Settings: People (people and roles, invitations, client assignments, membership) | Yes |
| Settings: Firm, Clients, Policies, Security | Yes; Security's usage snapshot reads when opened |
| Home (counts, review queue, goals, get started, dependencies, new in the books, closes handed to the agent) | Yes; other decision views and filters read when chosen |
| Clients (the roster, recent closes and a search's matches) | Yes |
| A close's Overview (the close and its drafts, the agent's goal, where it stands, the coordinator and its coverage, the work from its evidence) | Yes |
| Preparation (the view a link names, or Active) and Measure (effort and the track record) | Yes |
| Executive overview | Yes; it shows the layout's read |
| A close's other tabs, a workpaper, Executive reports, cash plan and decisions | Layout (and the close's header); the rest reads in the browser |

A page moves to "Yes" when its loader prefetches its reads and its components read them with `useQuery`.

## Checking it

- `node --experimental-strip-types --test workers/edge/src/workspace.test.ts` covers the Worker's choices, headers and
  fallbacks.
- Build with `VITE_AUTH_SESSION=server`, then `npm run build:ssr -w @close/frontend`, and render a route with fake
  reads to see its HTML.
- In production, a server-rendered page makes no API request in the browser on load.
