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

## In the browser

A page used to load its pieces one after another: the main script, the layout's code, the session check, the page's
code, then its data. Now:

- The session is asked for as soon as the script runs.
- At launch the app starts the current page's code and first reads together. A workspace link does the same when the
  pointer rests on it or it takes focus (`apps/frontend/src/features/dashboard/prefetch.ts`).
- A read asked for again while still under way shares it. A prefetched read is kept for the page's first ask within ten
  seconds. Any other finished read is forgotten at once, so a refresh always asks the server. A write or a new token
  forgets every read.
- A close's brief is never read ahead, because it is stamped with the time it arrives.

Prefetching runs only with the cookie session production uses (`VITE_AUTH_SESSION=server`).

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
