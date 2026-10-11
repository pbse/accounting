# AGENTS.md

## Documentation

- Start with [INDEX.md](INDEX.md) to locate the governing plan, relevant feature docs and validation evidence.
- Follow [docs/agent-first-build-plan.md](docs/agent-first-build-plan.md) for remaining B01–B12 scope, phases A–E and launch gates. Earlier reviews and deferrals do not override it.
- Update the index when adding or moving project documentation.

## Database

- Local development, testing and checks always use local PostgreSQL: `docker compose up -d postgres`, or a disposable native PostgreSQL. Point `DATABASE_URL` and `TEST_DATABASE_URL` at it.
- Never create a Neon branch for this project, for any purpose.

## Priorities

Every task aims at the smallest change that is correct, checked as cheaply as possible. Spend effort on understanding the problem, not on building or testing more than it needs.

## Understand first

- Read the request and the code it touches, and trace the real flow end to end, before editing. Searching or guessing does not replace reading the code.
- State the goal, the non-goals and what counts as done; then stay inside that.
- Fix the root cause once, where every caller passes through. Do not patch only the path a report names, and do not add a compatibility layer or a second implementation beside the old one.

## Minimal implementation

Stop at the first step that works:

1. Does this need to exist at all? If the need is speculative, skip it and say so.
2. Is it already in this codebase? Reuse it.
3. Does the standard library, the platform (a database constraint, native HTML or CSS) or an installed dependency do it? Use that.
4. Otherwise write the least code that works.

Choose a smaller solution when you notice yourself adding an abstraction, config option or dependency the task does not need, designing for possible future use, or changing unrelated files.

## Minimal tests

Tests cost time and tokens. Run only what the change can break.

- Run the closest existing tests for the changed file or package: `go test ./internal/<package>/...` from `services/backend`, or `node --experimental-strip-types --test <file>`. Widen to direct consumers only when the change crosses that boundary.
- Do not run `npm run check`, `npm test` or `npm run check:go` by default, and never "to be safe". CI runs all of them on every pull request that changes more than documentation. Run one locally only when shared infrastructure changed or a required gate demands it.
- Do not re-run tests that passed unless the code they cover has changed since.
- If existing tests prove the change, add none. Add a test only when behavior changed without coverage: the main changed path and, at most, one key failure path. No new test infrastructure and no filling of old coverage gaps.
- A new regression test must be seen to fail without its fix.

## Review

- One review, by the other model, on the final diff before it is committed: Codex wrote the code, Claude reviews; Claude wrote the code, Codex reviews. The author never reviews its own work, and no third reviewer is added.
- No review during implementation, per file or per commit.
- The reviewer only reads: the diff against the surrounding code and its callers. It does not run tests, type checks or lint; the author states what was run and the results.
- Fix findings that are bugs, missed acceptance criteria, security problems, regressions, or over-engineering that a deletion fixes. Other suggestions do not widen the task.
- After a fix, the reviewer sees only the fix, not the whole diff again.
