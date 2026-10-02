# AGENTS.md

## Documentation

- Start with [INDEX.md](INDEX.md) to locate the governing plan, relevant feature docs and validation evidence.
- Follow [docs/agent-first-build-plan.md](docs/agent-first-build-plan.md) for remaining B01–B12 scope, phases A–E and launch gates. Earlier reviews and deferrals do not override it.
- Update the index when adding or moving project documentation.

## Database

- Local development, testing and checks always use local PostgreSQL: `docker compose up -d postgres`, or a disposable native PostgreSQL. Point `DATABASE_URL` and `TEST_DATABASE_URL` at it.
- Never create a Neon branch for this project, for any purpose.
