# Page speed

Every workspace page should show its data in under 400 ms. This document records what made pages slow on
2026-10-08, what changed, and how to check a page.

## How a page loads

The workspace is a single-page app. Nothing is rendered on the server. A page loads like this:

1. The browser loads `index.html` and the main script from Cloudflare's edge.
2. The route's code chunk loads.
3. The page asks the API for its data, one or more JSON requests under `/v1/closes`.

Each API request goes from the edge Worker to the API's Durable Object, then to its one container. The Go API in that
container reads Neon Postgres, which is in AWS us-east-2 (Ohio).

Server rendering would not help. The time goes to the API's database round trips, not to rendering. A server-rendered
page would wait on the same reads before sending anything.

## What was slow (2026-10-08)

Measured in production from the US east coast: attention 1.0 s, closes 1.4 s, roster 1.9 s. The same requests took
3–40 ms against a local database. The difference came from five causes.

| Cause | Effect |
| --- | --- |
| The API container was the smallest Cloudflare size: 1/16 vCPU, 256 MiB. | CPU work was throttled to 6% of a core. A 10 ms burst of TLS, token checks or JSON took about 160 ms. |
| The container ran in Dallas and the database in Ohio. | Each database round trip crossed that distance, and a page makes many. |
| The container slept after 5 idle minutes. | The next request waited seconds for a cold start and new database connections. |
| Every request made about five round trips before its handler started. | Switching the connection to all firms for the session lookup and back cost two. The firm check used two queries. A rollback forgot the connection's firm. The driver pinged connections idle over a second. |
| Some pages made one query per close. | Home read each owed close's export label separately: 33 of its 42 round trips locally. A close's brief ran its sections one after another, 27 round trips. |

## What changed

- **Hardware and placement.** Every container runs on `standard-4`, Cloudflare's largest standard size (4 vCPU,
  12 GiB): the API, the preparation worker, the context machine and the scanner. The three that read the database are
  constrained to eastern North America (`ENAM`), beside it. The API stays awake for 24 hours after its last request.
- **Connections.** The API keeps up to 20 database connections open and opens 8 at start. Sign-in sessions are read
  from a separate pool of 3, so the firm's connections stay on their firm. The driver pings only connections idle over
  a minute.
- **Fewer round trips.** The firm check is one query. A read-only rollback keeps the connection's firm. Home reads all
  export labels in one query. The close brief runs its reads in parallel.

## The way to the API

After the move, an API request that touches no database still took about 250 ms from the east coast, against 110 ms
for a static file. The API's Durable Object, which forwards each request to the container, ran in San Jose: a Durable
Object stays where it was first made. Every request went from the edge to San Jose and from there to the container in
Atlanta. The edge now uses a new object, `api-enam`, made with the location hint `enam`. The container application
allows two instances, because the old object's container keeps its instance until it sleeps, 24 hours after its last
request.

Every server is in eastern North America, the database's region (AWS us-east-2): the API, the preparation worker
and the scanner containers are constrained to `ENAM`, and their Durable Objects (`api-enam`,
`preparation-worker-enam`, `text-scanner-enam`) were made with the location hint `enam`. The earlier worker object ran
in Paris. Each container application allows two instances so a new object can start while the old one's container
sleeps. The context machine, off in production, keeps its object `context-machine`, because that object stores the
machine's rotated sign-in; its container is constrained to `ENAM`.

## In the browser

Workspace pages are now rendered on the server with their data; [server rendering](server-rendering.md) describes how.
The browser hydrates the page instead of fetching its data. Later navigations run each route's loader, which reads
the page's data before it renders, and a read the cache already holds is not repeated.

## Measured in production (2026-10-08, signed in, from the US east coast)

| Moment | Result |
| --- | --- |
| Clicking from one page to Home | All seven of Home's reads done in about 200 ms |
| Clicking to Clients | Done in 280–440 ms, most of it the roster's WorkOS reads |
| Clicking to Settings | Done in about 470 ms; its code loaded first, since nothing hovered the link |
| Reloading Home | First reads done at about 680 ms; the rest started near 2 s |

The reload was slow for three reasons, now fixed:

- **Scripts were revalidated.** Every built script was served with `max-age=0`, so a reload asked again for about 30
  files. `apps/frontend/public/_headers` now marks `/assets/*` immutable for a year; their names carry a content hash.
- **Pages suspended.** `React.lazy` suspends a page on its first render even when its code is already loaded, and
  React waits up to 300 ms before revealing suspended content. A launch suspended twice, for the layout and the page.
  Routes now load their code with React Router's `lazy`, in parallel with their loaders.
- **The layout's checks waited.** The layout shows nothing until it knows whether the person is an executive, then asks
  whether the firm has the worked example. Both are now prefetched at launch.

The roster also read the firm's roles and memberships from WorkOS one after the other on every read. They now run
together, and what the roster only shows comes from a copy up to a minute old. Anything that decides, such as an
assignment or a hand-off, still reads WorkOS. A role changed in Krith forgets the copy at once.

## How to check a page

Every API response carries a `Server-Timing` header, which the browser's network panel shows under Timing:

```
Server-Timing: app;desc="until the response began";dur=32.6, db;desc="42 round trips";dur=32.2
```

`app` is the API's time before the response began, and `db` counts its database round trips by then. For an ordinary
reply that is the whole request. A streamed agent reply begins with its first event, so its header covers only that.
A request slower than 300 ms in full is also logged by route, with all its round trips, as `slow request:`.

The budget for one request is a few round trips. Use one query with `ANY($1)` instead of a query per row, and run
independent reads in parallel.
