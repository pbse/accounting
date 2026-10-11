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

The run creates fresh databases on a local PostgreSQL server and writes to no hosted service. It produces:

| File | What it is |
| --- | --- |
| `manifest.json` | The revision, whether it had uncommitted or untracked changes, the time, the corpus and which settings were present (names only, never values). |
| `providers.md`, `providers.json` | `cmd/provider-check`: one structured model call per configured route, and the OCR route (Gemini, when configured) reading a rendered page of known text, which must come back exactly (nothing missing or added); Krith's email sender (key, verified domain, sending enabled, click tracking off). These checks are read-only and send nothing. |
| `acceptance-recorded.md`, `.json` | `cmd/acceptance` with recorded model answers: everything around the model, scored by work type. |
| `acceptance-live.md`, `.json` | The same corpus read by the configured model; the release corpus is scored this way. |
| `agent-eval.md`, `.json` | The chat golden set replayed against the configured chat model: ≥90% of questions, every off-scope refusal. |
| `summary.txt` | One line per harness: passed, FAILED, or the dependency it is waiting on. |

Each harness can also run alone. See [acceptance corpus](acceptance-corpus.md), [agent chat evaluation](agent-chat.md) and `go run ./cmd/provider-check -h`.

## What it covers today, and what waits

| Evidence (handoff B12 item) | How it is produced | State |
| --- | --- | --- |
| 1. Independent expected results for every advertised method, with a held-out set | Corpus cases with a written treatment specification and hand-derived expected values per case, confirmed or corrected by the independent accountant | **Prepared, partly.** The sample covers prepaid intake only, with labels written by the builder. Extending the corpus to accruals, reconciliations, payroll, loans, depreciation, deferred revenue, owner items and settlements, plus the review package, is the next change. Accountant confirmation is a dependency. |
| 2. Actual model runs by task and denominator | `acceptance -model live`, `agent-eval` replay, provider check (model routes) | **Waiting:** model access. |
| 3. Provider operations: reads, pagination, refresh, rate limits, write and read-back, unknown-outcome recovery | Provider check (model, OCR, email). QuickBooks and Xero through the app's own connect, capture and sandbox-posting flows, recorded in the Security view (`sandbox_checked`); see [provider capabilities](provider-capabilities.md) | **Waiting:** QuickBooks sandbox app and company, Xero demo company, Gemini key, one controlled email recipient. |
| 4. Three clients across two consecutive periods, toward about 20 client-months and 100 documents | The release corpus, run by `evidence-run.sh <corpus-dir>` | **Waiting:** authorized records. |
| 5. Hosted scanner, mail and model; alert delivery; a timed restore | Hosted drills recorded in [validation](validation.md) | **Waiting:** a hosted drill window. The restore drill must not create a database branch (AGENTS.md); the user runs it, or approves a method that does not create one. |
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

It also has a place to confirm, correct or reject each expected value and each draft, and to record time spent. Corrections become the case's labels. A case the accountant has not confirmed stays builder-labelled and is reported apart, never as accountant-accepted. It is generated from a corpus and a run's report by the next change. Until then, the close review package (`GET /v1/closes/{id}/export`, [close review package](close-review-package.md)) gives the drafts and evidence of any one close.

## What I need from you

Each item names what it unblocks. Credentials go through the user-run credential script (`scripts/cloudflare-credentials.mjs` for hosted, a local `.env` you fill yourself for local runs), never through chat.

1. **Model access for evaluation.** `LLM_PROVIDER` with either an OpenAI key and model per purpose (`OPENAI_API_KEY`, `OPENAI_MODEL`, and `LLM_MODELS` for evidence selection, preparation and chat), or a local Context Machine with its profiles (`CONTEXT_MACHINE_ENDPOINT`, `CONTEXT_MACHINE_PROFILES`), plus a spending limit you are comfortable with for repeated runs. Through a Context Machine, also the firm and member its profile serves, as `ACCEPTANCE_ORGANIZATION` and `ACCEPTANCE_SUBJECT`. Unblocks items 2 and 4.
2. **Gemini OCR.** `GEMINI_API_KEY` with `GEMINI_PAID_SERVICE=confirmed` (the paid service, required before client documents are sent), with `LLM_PROVIDER` unset or `api`, where Gemini is the OCR route. Unblocks the OCR check and scanned-PDF corpus cases.
3. **Email.** Krith's sender settings (`PLATFORM_EMAIL_KEY`, `CLIENT_EMAIL_ADDRESS`) in the evaluation environment, and one mailbox you control as the recipient for a single, separately approved live send and reply. Unblocks the email row of item 3.
4. **QuickBooks.** An Intuit developer app's sandbox keys (`QBO_CLIENT_ID`, `QBO_CLIENT_SECRET`, `QBO_REDIRECT_URI`) and a sandbox company. You give consent in the app; I run capture, pagination and sandbox write/read-back. Unblocks item 3.
5. **Xero.** A Xero app's keys (`XERO_CLIENT_ID`, `XERO_CLIENT_SECRET`, `XERO_REDIRECT_URI`) and the demo company, with your consent in the app. Unblocks item 3.
6. **Authorized records.** At least three clients across two consecutive months: each month's source documents (invoices, statements, payroll summaries, loan statements) and its ledger (QuickBooks sandbox import, export files or read access). Then a broader set toward about 20 client-months and 100 documents, with permission to process them through the configured model and OCR providers, de-identified where you prefer. Kept outside the repository. Unblocks items 1, 4 and 6.
7. **An independent accountant** who:
   - confirms or corrects the treatment specifications and expected results per method;
   - labels the held-out cases;
   - completes timed sessions on the same scopes, once with their normal tools and once with the product.

   Unblocks items 1, 6, 7 and 8.
8. **A hosted drill window** for the timed restore and alert-delivery drills, run by you or by a method you approve that creates no database branch. Unblocks item 5.
