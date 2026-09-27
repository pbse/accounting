# AGENTS.md

## Database

- Local development, testing and checks always use local PostgreSQL: `docker compose up -d postgres`, or a disposable native PostgreSQL. Point `DATABASE_URL` and `TEST_DATABASE_URL` at it.
- Never create a Neon branch for this project, for any purpose.
