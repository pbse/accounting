# Slice 6 evidence: runbook and dependencies

**Status (2026-10-11): prepared for execution, not complete.** The commands below run today and record dated evidence for a revision. Every live run they need is blocked on a named dependency in [What I need from you](#what-i-need-from-you). Synthetic fixtures and agent-prepared drafts are not proof of live accounting accuracy. Nothing here claims a live model, provider or accountant result until a recorded run shows one.

Slice 6 of the [implementation handoff](agent-first-implementation-handoff.md) ("B12 and phase E") requires dated evidence for the release revision:
- actual model and provider runs;
- independently specified expected results across every method, with a held-out set;
- hosted recovery;
- accountant acceptance and measured human effort.

Claude owns the commands, the fixtures and their expected results, running them when access exists, capturing results, investigating failures and assembling the accounting-review package. The user arranges access, authorized records and an independent accountant (decision of 2026-10-11).

## Run it

One command records a run in `$EVIDENCE_DIR` (default `~/krith-evidence`), outside the repository and readable only by you, because a real corpus holds client data. Each run gets a directory named for its time and revision:

```sh
scripts/evidence-run.sh postgres://close:<password>@127.0.0.1:5434 [corpus-dir]
```

The run creates fresh databases on a local PostgreSQL server and writes to no hosted service.

**The model.** Production sends every model call through its context machine, on Codex `gpt-6.1-sol` at medium effort, paid by the user's ChatGPT/Codex subscription, and page reading (OCR) goes the same way. The hosted context machine has no outside route by design, so evaluations use a local one with the same settings. Start it in a terminal of its own and leave it running; the first time, it opens Codex's browser login in a home of its own (`~/.krith-eval`), separate from your usual Codex login:

```sh
scripts/local-context-machine.sh postgres://close:<password>@127.0.0.1:5434
```

While it runs and answers its readiness check, `evidence-run.sh` uses it for the live runs and the provider check. Without it, they are recorded as dependencies. When the login's token has less than two hours left, the script asks you to sign in again before starting. One evidence run goes at a time.

**The daily ceiling.** The live runs share a ceiling of `EVAL_DAILY_MODEL_CALLS` model calls a day (default 200). It is the user's $20 a day at roughly $0.10 a call, an API-price estimate for a reading call: the calls cost nothing extra on the subscription, but production shares its allowance. Revisit the figure once runs record real token use. `$EVIDENCE_DIR/model-calls-<date>` counts the day's calls, and each harness gets what is left (`-max-model-calls`). When it is spent, the cases or questions left are listed as not run, unevaluated, never as passed, and the summary says "ceiling reached for today". A full golden-set replay can take more than one day's ceiling; `agent-eval -only <prefix>` runs part of it.

It produces:

| File | What it is |
| --- | --- |
| `manifest.json` | The revision, whether it had uncommitted or untracked changes, the time, the corpus, which settings were present (names only, never values), the day's ceiling and the calls made before the run. |
| `providers.md`, `providers.json` | `cmd/provider-check`: one structured model call per configured route, and the OCR route (Gemini, when configured) reading a rendered page of known text, which must come back exactly (nothing missing or added); Krith's email sender (key, verified domain, sending enabled, click tracking off). These checks are read-only and send nothing. |
| `acceptance-recorded.md`, `.json` | `cmd/acceptance` with recorded model answers: everything around the model, scored by work type. |
| `acceptance-live.md`, `.json` | The same corpus read by the configured model; the release corpus is scored this way. |
| `review-package.md` | The accounting-review package from the live run when the model is configured, otherwise from the recorded run. |
| `revisions.md`, `revisions.json` | `cmd/revision-eval`: returned drafts' notes (`acceptance/revisions/cases.json`) read by the configured model as a close's goal reads them. Each outcome is scored against its case: the facts the note should change, or the one question it should lead to. The cases cover a start date from the order form, an ambiguous date, an unsupported method change, conflicting documents, a repeated return, a booked balance from the ledger, an account named in the note, an instruction planted in a document, and an accrual's actual invoice. Two gates decide the run. **Safety:** a change where a question was due, a wrong value, a value a case forbids (a false acceptance) or a model error fails it. **Task completion:** every note must end as its case expects: each correction made in full and recalculated, each question asked. A question where a change was due, an incomplete change, or a correct change the calculation refuses (the goal would ask instead) fails completion while the run stays safe. A note the model did not read leaves completion undecided, never passed. The return → goal → worker → revised-draft-for-review journey is exercised end to end by `TestAReturnedDraftIsRevisedFromTheNote` with a stand-in model, and with the live model in the connected demonstration. |
| `agent-eval.md`, `.json` | The chat golden set replayed against the configured chat model: ≥90% of questions, every off-scope refusal. |
| `summary.txt` | One line per harness: passed, FAILED, the dependency it is waiting on, or the ceiling reached; then the day's model calls against the ceiling. |

Each harness can also run alone. See [acceptance corpus](acceptance-corpus.md), [agent chat evaluation](agent-chat.md) and `go run ./cmd/provider-check -h`.

## What it covers today, and what waits

| Evidence (handoff B12 item) | How it is produced | State |
| --- | --- | --- |
| 1. Independent expected results for every advertised method, with a held-out set | Corpus cases with a written treatment specification and hand-derived expected values per case, confirmed or corrected by the independent accountant | **Prepared, partly.** The sample has ten cases, each with a written specification (`spec.md`): prepaid end to end, plus an accrual, a payroll summary and a loan statement read exactly and put to a person. Labels are the builder's. Drafts for methods other than prepaid, and the reconciliation, depreciation, deferred revenue, owner and settlement methods, need the harness to set up their procedures and ledger evidence. Accountant confirmation and the held-out set are dependencies. |
| 2. Actual model runs by task and denominator | `acceptance -model live`, `agent-eval` replay, provider check (model routes), through the local context machine | **Waiting:** your one-time local Codex login (`scripts/local-context-machine.sh`). On the synthetic sample these prove the pipeline with the real model, not accounting accuracy. |
| 3. Provider operations: reads, pagination, refresh, rate limits, write and read-back, unknown-outcome recovery | Provider check (model, OCR, email). QuickBooks and Xero through the app's own connect, capture and sandbox-posting flows, recorded in the Security view (`sandbox_checked`); see [provider capabilities](provider-capabilities.md) | **Waiting:** QuickBooks sandbox app and company, Xero demo company (you are setting both up), one controlled email recipient. |
| 4. Three clients across two consecutive periods, toward about 20 client-months and 100 documents | The release corpus, run by `evidence-run.sh <corpus-dir>` | **Waiting:** authorized records. |
| 5. Hosted scanner, mail and model; alert delivery; a timed restore | Two practice runs on the live system, recorded in [validation](validation.md); see item 7 below | **Waiting:** an alert destination and an uptime monitor; a restore method that creates no database branch. |
| 6. Human-effort measurement against the 40% gate | The app's effort counters (`/v1/closes/effort`), set against the accountant's timed baseline on the same scopes | **Waiting:** the accountant's baseline sessions. |
| 7–8. Accountant acceptance and unaided reviewers | The accounting-review package, then timed sessions with and without the product | **Waiting:** an independent accountant. |

Still unverified, and stated as such until a recorded run shows otherwise:
- agent-prepared bank reconciliations against real statements;
- straight-through prepaid drafts on real invoices;
- payroll checks against a real QuickBooks capture;
- line explanations on real ledgers;
- a first real email send through Krith's sender.

## The accounting-review package

For each case, the package gives the accountant:
- the source documents;
- the treatment specification (method, rounding, dates, signs);
- the expected values with how each was derived;
- the values the app produced, and every difference.

It also has a place to confirm, correct or reject each expected value and each draft, and to record time spent. Corrections become the case's labels. A case the accountant has not confirmed stays builder-labelled and is reported apart, never as accountant-accepted.

`cmd/acceptance -review <file>` writes it from a corpus and its run, and `evidence-run.sh` writes `review-package.md` beside the run's reports. The source documents are the case directories themselves. For any one close, the close review package (`GET /v1/closes/{id}/export`, [close review package](close-review-package.md)) gives its drafts and evidence.

## What I need from you

As of 2026-10-11. Credentials go through the user-run credential script (`scripts/cloudflare-credentials.mjs` for hosted, files you create yourself for local runs), never through chat.

**Done.**
- **Model access.** Production's context machine is set up, and every model call goes through it by default. For evaluations, run `scripts/local-context-machine.sh` once and sign in to Codex in the browser it opens; nothing else is needed. The cap is the ceiling above.
- **Page reading (OCR).** Stays on Codex through the context machine, your choice of 2026-10-11. The Gemini key installed in Cloudflare is unused in this mode.
- **Email.** Krith's sender settings, webhook and keys are installed.

**Still needed.** Each item names what it unblocks.
1. **One local Codex login**: `scripts/local-context-machine.sh postgres://close:<password>@127.0.0.1:5434`, left running while evidence runs. Unblocks item 2.
2. **One mailbox you control** as the recipient for a single, separately approved live send and reply. Unblocks the email row of item 3.
3. **QuickBooks** (you are setting it up): an Intuit developer app's sandbox keys (`QBO_CLIENT_ID`, `QBO_CLIENT_SECRET`, `QBO_REDIRECT_URI`) and a sandbox company. You give consent in the app; I run capture, pagination and sandbox write/read-back. Unblocks item 3.
4. **Xero** (you are setting it up): a Xero app's keys (`XERO_CLIENT_ID`, `XERO_CLIENT_SECRET`, `XERO_REDIRECT_URI`) and the demo company, with your consent in the app. Unblocks item 3.
5. **Authorized records** (not yet): at least three clients across two consecutive months, each month's source documents and its ledger, then a broader set toward about 20 client-months and 100 documents, with permission to process them through the context machine, de-identified where you prefer. Kept outside the repository. Unblocks items 1, 4 and 6.
6. **An independent accountant** (not yet) who:
   - confirms or corrects the treatment specifications and expected results per method;
   - labels the held-out cases;
   - completes timed sessions on the same scopes, once with their normal tools and once with the product.

   Unblocks items 1, 6, 7 and 8.
7. **The two hosted drills.** Unblocks item 5; both are needed before partner firms operate, not now.
   - **Alert drill.** Choose where alerts go: an email address or a chat channel's webhook, installed as `OPERATIONS_ALERT_WEBHOOK_URL` ([operations](operations.md)). Add an outside uptime monitor on `https://getkrith.com/healthz/worker`. Then send a test alert and confirm it reaches you, timing how long it takes.
   - **Restore drill.** Practise recovering the database and time it against the targets: serving again within 4 hours, losing at most 5 minutes of data. Neon's usual restore creates a branch, which this project never does, so the drill needs another method. One is restoring a backup into a temporary local database, which copies client data to your computer: you run it, or approve the method first.
