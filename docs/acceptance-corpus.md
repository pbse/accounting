# Acceptance corpus

The release corpus required by the [build plan's accuracy contract](agent-first-build-plan.md#accuracy-as-a-release-contract) and [phase A](agent-first-build-plan.md#delivery-sequence-and-launch-gates). Labeled client-months run through the real pipeline, and the result is scored by work type with its denominators, never as one blended accuracy figure. A clean run on a finite corpus is a gate for that corpus, not a general guarantee.

## What exists

- **The harness.**
  - `services/backend/internal/acceptance`: the case format, scoring and report.
  - `transport.RunAcceptanceCase`: loads a case and drives the pipeline.
  - `cmd/acceptance`: the command line.
- **A synthetic sample** in `services/backend/acceptance/sample`. It has seven cases with recorded answers:
  - one goes through alone and, once approved, leaves the close ready;
  - one is asked about a wrong reading;
  - one is held as the wrong client;
  - one is asked about for want of a ledger capture;
  - one has a held document that keeps an approved close from being ready;
  - one has a Trial Balance that would not tie with its draft, so its approved close is blocked;
  - one uploads the same invoice twice.

  `TestTheSampleCorpusRunsThroughThePipeline` keeps it, and the harness, working.
- **Not yet assembled: the release corpus itself.** It needs authorized, de-identified client months (roughly 20 client-months and 100 documents across at least two periods, as a planning floor) and an independent accountant's labels. It is kept **outside this repository**, because it holds client data.

## A case

A case is one client-month, in a directory of its own: `case.json` with its documents beside it.

```json
{
  "id": "acme-2026-09",
  "split": "development",
  "client": "Acme LLC",
  "period": "2026-09",
  "tags": ["wrong_client"],
  "setup": {
    "prepaid_procedure": {"method": "equal_full_months_v1", "expense_account": "Insurance", "prepaid_account": "Prepaid Expenses"},
    "quickbooks": {"home_currency": "USD",
                   "accounts": [{"id": "12", "name": "Insurance", "type": "Expense"}, {"id": "13", "name": "Prepaid Expenses", "type": "Other Current Asset"},
                                {"id": "20", "name": "Accounts Payable", "type": "Accounts Payable"}],
                   "bills": [{"vendor": "Acme Insurance Co", "number": "INV-778", "amount": "1200.00", "date": "2026-09-03", "account_id": "12"}],
                   "trial_balance": [{"account_id": "12", "debit": "1200.00"}, {"account_id": "13"}, {"account_id": "20", "credit": "1200.00"}]}
  },
  "documents": [{"file": "invoice.pdf", "model": "invoice.model.json",
                 "label": {"work_type": "prepaid", "facts": {"amount": "1200.00"},
                           "drafts": [{"debit_account": "Prepaid Expenses", "credit_account": "Insurance", "amount": "1100.00"}]}}],
  "signoff": {"description": "Prepaid insurance for September", "approve_drafts": true},
  "expect_ready": true
}
```

- **`split`.** `development` cases may guide changes. `held_out` cases are kept out of tuning and are reported separately. Leave out later facts and final adjustments from a historical case's inputs.
- **`setup`.** The firm's configuration before the documents arrive: an enabled prepaid procedure, and a QuickBooks capture of the month. The capture holds the chart of accounts (with QuickBooks account types), the month's bills and the month-end Trial Balance, which every real capture holds. Readiness ties the supported balances out against that Trial Balance, counting proposed entries, so a balance that would not tie blocks sign-off. Other setup is added as cases need it.
- **`label`.** The accountant's judgment of the document, not of any reading:
  - `work_type`: `prepaid`, `accrual` and so on, or `none` for a document with no accounting work.
  - `facts`: the values a correct reading gives.
  - `drafts`: every journal entry correct preparation proposes, across its workpapers (a loan's or payroll's several entries, a reversing accrual's reversal, a disposal's lines and a settlement's refund and revenue drafts included), each with `date`, `debit_account`, `credit_account` and `amount`. A field left out is not judged. `"drafts": []` says nothing is to be posted (a one-month prepaid fully expensed, say); leaving out `drafts` leaves the drafts unjudged.
  - `exception`: a material problem a person must see before anything goes on. A problem counts as caught only when the app's own sign for it shows, so the harness accepts only problems it has a sign for:
    - `wrong_client`: the identity block, or a hold reason that leads with "The invoice is billed to "…", not the client or a confirmed alias". A rejected bill-to citation is not the sign: it tells the person nothing about whose invoice it is.
    - `duplicate`: a file whose uploaded bytes repeat an earlier one's, kept by the app as that document. A workbook is compared as the CSV of its first sheet, as it is uploaded: the harness and the upload route share that conversion, and a file over the route's 5 MiB limit is refused. Such a file must be labeled `duplicate`, and the case is refused otherwise.

    Other problems the plan requires (an OCR decimal error, missing pages, conflicting totals) are added to `acceptance.Exceptions` with their sign as the app learns to report them. Until then, a case can still label the facts and drafts of such a document.
- **`signoff`.** The accountant's part once the agent is done, applied after the pipeline settles and recorded as "acceptance reviewer". With `approve_drafts`, every current draft is approved; the scope is defined as covering every draft. Readiness is then read as sign-off reads it. Without `signoff`, nothing defines the scope, so the close is never ready: a case with `expect_ready` true and no `signoff` is refused.
- **`expect_ready`.** Whether the accountant's label says the close may be signed off once that is done.
- **`selection`.** A recorded evidence-selection answer, `{"selected_ids": ["$primary", "$source:<file>"]}`. It is needed only when the close is too large to read whole: more than 20 sources, or 128 KiB of text. The model then first chooses what to read with this document; without a recorded selection, that document's job fails and the run reports it as not processed, with the reason.
- **`model`.** A recorded answer, in the model's answer shape. `"$primary"` stands for this document's evidence ID, and `"$source:<file>"` for another document's. Its quotes must be lines of the document, as a real reading's are.

## Running it

Use a fresh local database migrated with `infra/postgres/migrate.sh`. The command refuses anything but localhost, a loopback address or a socket, and any database that already holds closes, because its steps take work from the whole queue. Each case gets a firm of its own, disabled afterwards.

```sh
cd services/backend
go run ./cmd/acceptance -corpus ../path/to/corpus -database postgres://…@127.0.0.1/acceptance -model recorded -report report.json
```

- **Model.**
  - `-model recorded` replays each document's recorded answer. It tests everything around the model. Every document the app keeps needs one, and the command refuses the corpus otherwise.
  - `-model live` reads with the model configured in the environment (the same settings as the API: `LLM_PROVIDER`, `OPENAI_*` or `CONTEXT_MACHINE_*`). The release corpus is run this way, with the configured model version. The command refuses to start unless the reading and evidence-selection models are configured, since the pipeline would otherwise fall back to local reading.
- **Firm.** By default each case is a firm of its own, worked as "acceptance" and disabled afterwards. Context Machine reads only for the firm and person its access profile serves (`CONTEXT_MACHINE_PROFILES`). A live run through it therefore passes `-organization org_… -subject …` with that firm and person, and every case becomes a close of that one firm. A client has one close a month and one ledger connection there, so no two cases may share a client; the command refuses such a corpus. Run those cases separately, each on a fresh database. Each case's unfinished jobs are cancelled once it has been observed.
- **PDFs** need `pdftotext`, `pdfinfo` and `tesseract`. Run PDF cases where those exist, such as the [release-check image](operations.md#release-revision-check).

The command prints a Markdown report. It exits 1 on any critical failure, and on any document the pipeline never decided on, since the run has not evaluated it. Abstentions and expected-ready closes still blocked do not fail a run. They are reported with their denominators for the coverage and effort gates, where a uselessly high rate cannot pass. `-report` writes the full report as JSON, including every document's observed outcome.

## What is scored

Every document counts once, in its split and its labeled work type. A document the agent went on with alone is judged on every count at once: a problem it went past, its facts and its drafts.

| Count | Means | Critical |
| --- | --- | --- |
| Went on alone | The agent confirmed the reading itself and prepared the work | — |
| Asked a person (nothing wrong) | An abstention: a clean document put to a person. It is reported because a high rate fails the coverage and effort gates. | no |
| Problems held | A document with a labeled exception whose job settled waiting for a person with the app's sign for that problem, or a duplicate kept as the earlier document | — |
| False accepted facts | A fact the agent went on with that the label contradicts (one per field) | **yes** |
| Incorrect drafts | Work the agent prepared alone that is of the wrong kind, has more or fewer drafts than the label, or has a labeled entry no draft matches on date, accounts and amount, each draft matching at most one entry (one per problem) | **yes** |
| Missed problems | A labeled exception, or a document with no work, that the agent went past, or held without its sign (for another reason, so the person was not told) | **yes** |
| False exceptions | A clean document refused as no work, or flagged with a problem's sign (as another client's) | no |
| Wrong values put to a person | Wrong values a person would have to correct | no |
| Not processed | No job started, it failed (the report gives the job's error), or it had not settled. A job still waiting for evidence, such as one pending a retry after the firm's model-call allowance ran out, has not settled. Nothing was decided, so a labeled problem here is not counted as held. | fails the run |

Per case it also counts:
- **False-ready closes** (critical): nothing blocks sign-off while the label says the close is not ready.
- **Expected-ready closes still blocked.**
- **Posting attempts of any kind** (critical): preparation makes none.
- **Documents with more than one live preparation job** (critical).

Amounts (the fields the app reads as amounts, from invoice amounts to statement balances and gross wages) compare by value: 1,200.00 is 1200. Everything else, identifiers included, compares as text ignoring case and spacing, so invoice number 0012 is not 12.

## Not covered yet

This first version exercises document intake, reading, preparation, drafts and close readiness. The plan's other required checks need case kinds of their own, added as the corpus grows:
- partial client replies;
- late entries and changed policies;
- locked periods;
- a timeout after a write;
- revoked access;
- a hidden payroll shortfall;
- a worker restart.

Straight-through preparation in a case needs the prepaid procedure and the QuickBooks capture above. Other work types are read and put to a person.
