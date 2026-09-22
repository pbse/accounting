# Evidence-backed close preparation

This slice connects uploaded evidence, human-confirmed preparation instructions and durable execution of the existing calculators. It uses the existing single private workspace. It is not yet model-driven extraction or the full PRD evidence pipeline.

## Use it

1. Open a client close and upload authorized UTF-8 `.txt` or `.csv` evidence.
2. Open prepaid or reconciliation preparation from that close. For reconciliation, select uploaded CSVs to populate the form; confirm balances, page inventory and method. For prepaids, inspect the invoice text and enter its terms.
3. Prepare the preview, select the source files and their locations, then **Save source-linked instructions**. Reconciliation instructions use the uploaded CSV bytes, replacing pasted rows. Prepaid facts remain human-entered and unverified.
4. Return to the close, select the instruction revisions you intend to run, and choose **Prepare selected work**. The durable job returns immediately. You can leave the page or close the browser.
5. The independent worker creates immutable drafts. **Show prepared drafts** opens the results; **Inspect source** displays the exact uploaded text alongside the calculation. The export contains source hashes and originals under `evidence/{id}.txt`, preserving bytes with a non-executable text extension.

Alternative instructions are not additive. The product does not choose which revision represents the correct accounting treatment. Uploading a new file does not automatically invalidate old instructions; explicitly replace the old source to invalidate dependent review and block stale preparation. See [workpaper review](workpaper-review.md).

## Local startup

Fresh `npm run dev` starts API, frontend, Postgres and worker with all migrations. For an existing database, apply migration 003 explicitly:

```sh
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f infra/postgres/migrations/003_evidence_preparation.sql
```

For native development, export `DATABASE_URL` and run `go run ./cmd/worker` from `services/backend`, using `HTTP_ADDR=127.0.0.1:8081` alongside the API. The worker needs database access only; do not give it ledger credentials.

Jobs remain queued when the worker is unavailable. Their UI label is **Waiting for worker or processing**, because calculation and completion occur within one short transaction. Cancellation succeeds only before a worker claims the job. Failed validation produces a visible failed job; correct the instructions and submit a new selection. Repeating the same selection returns the existing job; explicitly resubmitting a cancelled selection requeues it.

## Storage and durability

- Reuses workspace authentication on every read/write. Every source, plan and job lookup includes its close ID. This does not add multi-firm or per-staff entity authorization.
- Text originals are immutable through the API, SHA-256 identified and deduplicated within the close. The first uploaded filename remains canonical for identical bytes.
- Uploads permit only bounded valid UTF-8 text without binary control characters. No PDF, image, archive, HTML rendering, embedded script execution, OCR, or external model transmission occurs. Display uses escaped React text; raw reads use attachment, no-sniff, no-store and sandbox headers.
- Limits: 512 KiB per file, 100 files / 20 MiB per close, 20 instruction revisions per job. Existing calculator limits still apply. The UI shows the latest 100 instructions and 20 jobs.
- Postgres stores the originals for this bounded slice. R2 and scanning/extraction services are deferred until richer document intake is supported. Production retention, deletion, backups and tenant isolation remain separate work.
- The worker locks a queued row with `FOR UPDATE SKIP LOCKED`, checks source hashes, calculates locally, and commits all draft IDs and completion together. Process loss before commit rolls back the attempt. Another worker resumes the still-queued job; uniqueness also prevents duplicate drafts on replay.
- No external effects occur in the job transaction. Slow extraction, evidence waits and external tools will need checkpoints before joining this loop.

## API

All routes use the existing workspace bearer authentication and configured frontend/backend URL separation:

| Route | Behavior |
| --- | --- |
| `POST /v1/closes/{id}/evidence` | Multipart `file` upload; returns source ID and hash |
| `GET /v1/closes/{id}/evidence` | Source inventory without file bodies |
| `GET /v1/closes/{id}/evidence/{source}` | Exact original text, scoped to close |
| `POST /v1/closes/{id}/preparations` | Save `{kind,input,sources:[{id,role,location}]}`; server supplies names and hashes |
| `GET /v1/closes/{id}/preparations` | Latest 100 immutable instructions |
| `POST /v1/closes/{id}/jobs` | Queue `{plans:["id",...]}`; returns durable job ID |
| `GET /v1/closes/{id}/jobs` | Latest 20 job states and resulting workpaper IDs |
| `POST /v1/closes/{id}/jobs/{job}/cancel` | Cancel an unclaimed job |

Roles are `invoice` for prepaids or separate `statement` and `ledger` for reconciliation. Agents can use these same routes, but cannot create accounting authority by attaching evidence or queueing work.

## Cloudflare

The GitHub staging workflow now deploys the existing preparation-worker container with only `DATABASE_URL`, after checking migration 003. A [Cron Trigger](https://developers.cloudflare.com/workers/configuration/cron-triggers/) wakes the container once a minute; Postgres polling owns delivery and replay protection. No queue consumer or second scheduler is required for these short deterministic jobs. This keeps the worker active and has a runtime cost; it is not a claim of scale-to-zero operation.

Hosted startup, container networking, secrets and actual deployment remain unverified until staging is configured. The root `deploy:staging` command still deploys the API/frontend only; GitHub Actions additionally deploys the worker. Docker/Cloudflare runtime execution was unavailable in the current local environment.
