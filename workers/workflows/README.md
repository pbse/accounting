# Durable close coordination

Boundary reserved for Cloudflare Workflows definitions. No deployed Workflow yet. The current short, deterministic preparation jobs use Postgres row locks and an independent Go worker; see `docs/evidence-and-preparation.md`. Workflows is not required for this bounded local calculation.

Add the first definition with the persisted close-preparation job: gather permitted evidence, dispatch supported preparation, wait for evidence/review, resume from a revision-bound signal, export. Keep accounting and authorization in Go. Steps pass durable IDs; Postgres owns job, approval and action truth. A Workflow retry must not issue a fresh posting.

Before enabling: prove interruption/resume, late signals, cancellation, source changes, budget limits and duplicate delivery with one real workpaper flow. Do not create a second scheduler or an in-memory job substitute for local development. Use Wrangler's local runtime with the same definition.
