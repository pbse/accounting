# Cloudflare and GitHub delivery

**Client attachment requirement:** configure [Cloudflare WAF upload scanning](cloudflare-upload-scanning.md) before releasing client PDFs. Forwarded attachments use the on-demand scanner and explicit reviewer release; see [source sync and forwarding](source-sync-and-forwarding.md).

## What is configured

`workers/edge/wrangler.jsonc` deploys React assets and routes the synthetic reference API to the Go container. `workers/queue/wrangler.jsonc` deploys a separate Go worker container class and queue handler, with no consumer binding or public URL. Both use the same portable image; the worker overrides its entrypoint to `/worker`. `workers/scanner` deploys a separate ClamAV image for TXT/CSV with no cron or database access. The API invokes its authenticated endpoint only for scoped original attachments.

There is one bounded container per role. A one-minute cron wakes the preparation worker; PostgreSQL owns preparation jobs and PDF extraction leases. No subscribed Cloudflare Queue, R2 bucket, Workflow or hosted Postgres is created by deployment. Do not infer infrastructure readiness from a green frontend build.

Wrangler builds/pushes the image during deployment; a Docker engine and Linux/amd64-compatible image are required. The worker can become reachable before its container is ready. [Cloudflare Containers](https://developers.cloudflare.com/containers/get-started/)

## GitHub setup

This directory started with no commits and no remote. Creating/pushing a repository was not part of this scaffold. When ready for CommitPR, review the exact diff, initialize the shared main branch and add the intended GitHub remote; subsequent independent work uses fresh worktrees from updated origin/main.

1. Create a GitHub `staging` environment with required reviewers. Deployment credentials are restricted to this environment, not pull-request jobs.
2. Add environment secrets `CLOUDFLARE_ACCOUNT_ID` and a least-privilege `CLOUDFLARE_API_TOKEN` that can deploy the named Workers/Containers and associated Durable Object migrations. Confirm account plan, Containers availability and current token requirements in Cloudflare.
3. Require CI for main. CI installs locked dependencies, checks/builds TypeScript, runs focused Go/queue checks, validates Compose and builds the image. No cloud credentials enter CI.
4. Manually dispatch **Deploy staging** from main after the requested release approval. It runs CI on that same revision, then deploys both runtime adapters and frontend through Wrangler. Concurrency prevents overlapping staging deployments.
5. Inspect container rollout status and the deployed `/healthz`, `/v1/reference/prepaid`, and review screen. A successful upload alone is not runtime verification.

[Cloudflare GitHub Actions guidance](https://developers.cloudflare.com/workers/ci-cd/external-cicd/github-actions/)

No production workflow exists yet. Add a separate production configuration, separate resources/secrets, protected environment and approved release process after real authentication and data controls exist. Never reuse staging database, bucket or queue resources for production. This scaffold does not auto-deploy arbitrary pushes or PR previews.

## Local Cloudflare parity

With Node 24 and Docker running:

```sh
npm ci
npm run dev:cloudflare
```

This builds assets and starts Wrangler locally for the edge/API path. To inspect the inactive queue worker locally, use `npx wrangler dev -c workers/queue/wrangler.jsonc`. Queue consumption is deliberately not configured; its Go endpoint returns 503 and the adapter never acknowledges that response. Do not bind a real queue until durable processing and a dead-letter policy exist.

Compose is the daily development path with React hot reload. Wrangler is the adapter/runtime-parity path. Postgres is the same engine locally and in the future cloud deployment; select a managed provider/region only after the data-route requirements are approved. R2 and Workflow emulation should be added with their first implemented flow.

## Before enabling live data or jobs

- Authenticated identity, entity authorization, retention, private evidence access and restricted runtime credentials.
- Scoped Postgres persistence, migrations, inbox/outbox, leases and replay tests.
- Queue consumer/producer bindings, bounded retry policy, dead-letter queue and operational alerts.
- Restricted R2 binding adapter, quarantine and upload limits; model data permissions and budgets.
- Versioned approval and execution receipts, provider-specific unknown-outcome reconciliation, independent readback.

These are feature activation gates, not additional prerequisites for viewing the synthetic reference.

## Separate frontend/backend URLs

The frontend can be deployed as static SPA assets independently of Go. Configure `VITE_API_BASE_URL` during its build and forward the exact frontend origin via `FRONTEND_ORIGIN` to the Go API. Keep the `/index.html` fallback for public and dashboard URLs. The default configuration continues to serve both through the existing Cloudflare ingress, so no extra host is required locally or in staging.

For a separate backend, set the staging GitHub environment variable `VITE_API_BASE_URL`; the deployment workflow passes it into the frontend build. Leave it unset for same-origin access. Do not put it in a runtime-only Worker variable: Vite reads it at build time. Authentication/session cookies are not implemented by this URL configuration.

## Saved-close configuration

Saved closes need migrated PostgreSQL and server-side `DATABASE_URL` / `WORKSPACE_TOKEN` secrets. Edge forwards these to Go. Never expose them through VITE variables. No database or auth provider has been provisioned by this feature. Apply migrations separately from container rollout. See `saved-closes-and-reconciliation.md`.

## AuthKit and sandbox staging workflow

The current workflow deploys the frontend/API and preparation/OCR worker, uploads code and server secrets together, and verifies `/readyz`. It requires WorkOS organization configuration and a TLS PostgreSQL database with all numbered migrations already applied. See `auth-sandbox-staging.md` for the exact settings. The local development token is not accepted by the hosted edge. GitHub environment protection must be configured separately; it is not established merely by the workflow file. Enroll a synthetic WorkOS organization and test membership revocation, binder release, deletion preview, capacity and recovery before admitting client data.
