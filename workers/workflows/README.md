# Durable close coordination

This directory once reserved a Cloudflare Workflows boundary. Durable, multi-step work now runs in the Go workflow engine instead (`services/backend/internal/wf`), with its state in PostgreSQL and each workflow a directory under `services/backend/workflows`. See [the workflow engine](../../docs/workflow-engine.md), including why no hosted workflow service is used.
