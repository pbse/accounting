# Infrastructure plan: the API beside the database

**Status: future plan, not built.** Recorded 2026-10-11 after a review of production speed. The owner keeps Neon,
stays on Cloudflare for everything it does well, stays on Neon's Free plan for now, and will decide later when to build
this. Nothing here changes production until then.

## Why

Pages wait on database round trips, not on CPU or memory. Measured in production on 2026-10-11:

| Fact | Value |
| --- | --- |
| Neon project | `aws-us-east-2` (Ohio), Free plan, autoscaling 0.25–2 CU, sitting at 0.25 CU |
| API Durable Object `api-enam` | Ashburn, Virginia (`IAD`) |
| API container | Atlanta (`atl13`); on 2026-10-08 it ran in Columbus (`cmh02`) |
| One database round trip from the API | 33 ms, steady; the query itself runs in 0.05 ms |
| API work apart from the database | close to 0 ms on `standard-4` (4 vCPU, 12 GiB) |

Several reads make many round trips one after another (counted locally on the demonstration seed): Home's `/home` 87,
`/effort` 84, a close's `/goal?verify=1` 68 and `/brief` 49, `/management-pack` 37, `/roster` 34, the Clients list 28,
`/readiness` 28. At 33 ms each, Home's data takes about 2.9 s on the server. In Columbus the same reads cost 1–2 ms a
trip, which is why pages met the 400 ms budget on 2026-10-08 ([page speed](performance.md)).

Cloudflare cannot keep the container there. A container may be constrained only to a region (`ENAM`) or a
jurisdiction, never a city; it starts in "the nearest location with a pre-fetched image", not necessarily beside its
Durable Object, and a deploy or a sleep can move it
([placement](https://developers.cloudflare.com/containers/concepts/placement/),
[architecture](https://developers.cloudflare.com/containers/concepts/architecture/)).

## The rule

Code that talks to Postgres many times per request runs within 1–2 ms of Neon. Everything else stays on Cloudflare.

## Target

```
Browser
 └─ Cloudflare: DNS, WAF, CDN, public site, static files                     (unchanged)
     └─ Edge Worker: routing, sign-in cookies, CSP, server rendering          (placement.region "aws:us-east-2")
         └─ Workers VPC binding → Cloudflare Tunnel (private; no public port)
             └─ Go API + preparation worker: one always-on host, AWS us-east-2
                 └─ Neon, aws-us-east-2: same AWS region, about 1 ms
Stays on Cloudflare: upload scanner, context machine, queues, Email Routing, contact email.
```

- **The host.** The same `services/backend` image, run in AWS us-east-2 because it is Neon's own region: ECS Fargate
  (2 vCPU, 4 GiB, ARM; nothing to patch; about $60–70 a month) or one Lightsail VM running Docker (cheapest, about
  $24 a month). It runs the API and the preparation worker. AWS App Runner is
  [no longer open to new customers](https://docs.aws.amazon.com/apprunner/latest/dg/what-is-apprunner.html), so it is
  not an option.
- **The link.** `cloudflared` runs beside the API and dials out to Cloudflare. The edge Worker reaches the API through a
  [Workers VPC](https://developers.cloudflare.com/workers-vpc/) service binding instead of the `api-enam` Durable
  Object; the API has no public address, load balancer or NAT gateway. Workers VPC is in beta; the fallback is a Tunnel
  hostname that only a Cloudflare Access service token may call.
- **The edge Worker's placement.** `"placement": { "region": "aws:us-east-2" }` runs the Worker in the Cloudflare
  location closest to that region ([placement hints](https://developers.cloudflare.com/changelog/post/2026-01-22-explicit-placement-hints/)),
  so a page's several loader reads each cost 1–2 ms instead of 30–60. Static files are still served from the edge
  nearest the visitor.
- **What the API needs from Cloudflare.** Only the context machine, which the container now reaches through
  `outboundByHost` (`context-machine.internal`) and a service binding. Its Worker has no public route
  (`workers_dev: false`), so the move adds one: a route on our zone behind a Cloudflare Access service token, with
  the bearer token the context machine already requires on every request. The scanner, email and model providers are already ordinary URLs.
- **Deploys and secrets.** Deploy production builds the image, pushes it to ECR and updates the service; migrations
  run as now. The owner sets the API's secrets in AWS with a script run in their own terminal, as for Cloudflare.

Expected: a database round trip from 33 ms to about 1 ms, Home's data from about 2.9 s to about 0.1 s, and a page
from New York in about 150–250 ms, with the location chosen by us rather than by each deploy.

## Neon on the Free plan

The Free plan gives 100 CU-hours a month per project and suspends the compute until the next month when they run out;
scale to zero after 5 idle minutes cannot be turned off ([Neon pricing](https://neon.com/pricing),
[Free plan limits](https://neon.com/faqs/free-plan-limits-and-quotas)). Today the API holds connections open all day,
so the compute never sleeps: 70.8 CU-hours were used by 2026-10-11, on course for the cap around 2026-10-15.

On Free, whichever host runs the API, the database must be allowed to sleep. The API closes idle connections after a
few minutes and opens none at start. The preparation worker is the harder part: its workflow engine polls every 10
seconds when idle and writes a heartbeat every 30 (`services/backend/internal/wf/engine.go`), so as it runs today the
database never sleeps. It has to run only while there is work, woken by the queue or the schedule and stopping once
idle, before a quiet firm uses no compute. The first request after about five quiet minutes then waits
roughly half a second while Neon wakes. Neon's Launch plan removes that wait (scale to zero can be turned off; about
$20 a month at a 0.25 CU floor, $0.106 per CU-hour) and lifts the 1 GB storage limit and the 6-hour restore window.

## Considered and not chosen

| Option | Why not |
| --- | --- |
| Cloudflare containers only | Placement is region-wide, so a round trip is 1–33 ms depending on where a deploy lands. Steering (restarting until it lands near Ohio) is best effort. |
| GCP (Columbus, `us-east5`) | Another cloud than Neon's; the path between them is not guaranteed to stay local. |
| Railway | No Ohio region (US East Metal is in Virginia), so Neon would first move to `aws-us-east-1` in a new project, and the path from Railway to AWS would need measuring. |
| Moving Neon to Virginia, beside the Durable Object | A region migration (a new project; a project's region is fixed), and the container would still float. |

## Steps when built

1. Probe: a throwaway task in AWS us-east-2 times a round trip to Neon; continue only if it is about 1 ms.
2. Build the service, the Tunnel, the Workers VPC binding behind a switch, the context machine's protected route
   and the deploy step; keep the `api-enam` container as the fallback the switch returns to.
3. Switch over, measure `Server-Timing` on the main pages, keep the fallback for a week, then remove the API and
   preparation-worker containers.

## Independent of hosting

Reads recheck signed-off closes through `closeReadiness` (about 24 round trips a close), and the owner keeps that
recheck exact: Home every signed-off close owed up to this month or due within a week (`outstanding` in
`morning.go`), the Clients list and the roster those from two months ago onward, Measure the twelve months up to the
month shown. Home's cost grows with every month signed off even at 1 ms a trip, so the rechecks should run in parallel on one shared snapshot (`pg_export_snapshot`), and `closeReadiness`
should fold its independent reads into fewer statements.

## Measuring

- A round trip: `GET /v1/client-request` with a made-up `Authorization: Bearer` value of 26–128 characters makes
  exactly one query (a shorter one is refused before any); read `db` in its `Server-Timing`.
- Where the container runs: the Containers API, `GET /accounts/{account}/containers/applications/{id}/instances-v2`.
- Where a Durable Object runs: GraphQL `durableObjectsInvocationsAdaptiveGroups`, dimension `coloCode`.
