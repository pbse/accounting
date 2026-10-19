# Evidence-backed close preparation

Uploaded evidence starts durable intake and optional OpenAI interpretation. The worker proposes cited facts and specific questions, the reviewer confirms accounting assumptions, and the same deterministic calculators produce immutable workpapers. Without model settings, local proposals are explicitly limited. See [the Work inbox](work-inbox.md), [coordinator](close-coordinator.md) and [onboarding](onboarding.md).

## Use it

1. Open a client/month and upload authorized TXT/CSV, PDF or Excel (.xlsx) evidence. A workbook's first sheet is kept as CSV text (named `<file>.xlsx.csv`): values as shown in their columns, shared and inline text, date-formatted cells as YYYY-MM-DD and formulas as their saved results; the workbook itself is not kept, and other sheets are not read. The upload returns a durable source/job identity; PDF text extraction must finish before interpretation can use it.
2. Inspect the proposed facts and quoted source locations in Work. Resolve identity ambiguity, missing evidence and method questions. A proposal is not a verified booking fact.
3. Confirm the supported method and assumptions. The original job resumes and prepares a draft for review. For manually selected work, save source-linked instructions and choose **Prepare selected work**; this remains available beside agent intake.
4. Inspect the calculation, originals and blockers; approve or request changes. Client requests and scanned/released replies can resume the affected work: [coverage requests](coverage-requests.md).
5. Explicitly replace a changed source, then review/recalculate dependent work. Exact-byte copies reuse their evidence identity; a different new file alone does not establish supersession. [Source review](workpaper-review.md)
6. Without a live ledger connection, label the uploaded ledger exports as historical exports so the brief, package and setup say what they are: [try a historical period](onboarding.md#try-a-historical-period).

## Storage and processing boundaries

- Hosted access requires active firm membership, client access and applicable permissions. Source/plan/job references are checked against the close. The development token is shared and local-only.
- PostgreSQL stores original bytes, hashes, extracted text and revisions. Current original-evidence limits are 100 files / 20 MiB per close, 512 KiB per TXT/CSV (including a workbook's CSV text), 5 MiB per Excel workbook, and 5 MiB / 20 pages per PDF. R2 is not deployed.
- Staff direct uploads use file validation and bounded parsing; do not describe them as malware-scanned. Public client attachments are quarantined, scanned and explicitly released. Forwarded documents have a separate reviewed intake path. [Scanning](cloudflare-upload-scanning.md), [forwarding/Drive](source-sync-and-forwarding.md)
- Text is escaped in the UI; originals download with restrictive headers. Poppler/Tesseract run in bounded subprocesses with no provider/database credentials. With `GEMINI_API_KEY` on the worker, Google's Gemini reads every PDF page; a page's exact text layer is always kept, and Gemini's reading of it only checks it, flagging the page when the two differ. OCR, by Gemini or Tesseract, is not proof of complete or correct facts. [PDF details](recalculation-and-pdf.md)
- PostgreSQL jobs, fenced claims/checkpoints and source checks own recovery. A stopped worker leaves work visible rather than losing it. Deterministic calculation replay preserves identities; OCR/model recovery may repeat processing/cost. External posting/email has separate attempt rules.
- Supersession, revocation and changed approvals can block resumed work. A cancellation prevents future work where possible, but cannot recall already-sent provider requests.

## Manual capability routes

These routes remain usable beside the coordinator; hosted authentication and current client scope apply. [MCP](mcp.md) exposes a narrower permitted subset.

| Route | Behavior |
| --- | --- |
| `POST /v1/closes/{id}/evidence` | Multipart file intake; returns source/hash/job |
| `GET /v1/closes/{id}/evidence` | Source inventory without original bodies |
| `GET /v1/closes/{id}/evidence/{source}` | Scoped original download |
| `POST /v1/closes/{id}/preparations` | Save typed input and cited source instructions |
| `GET /v1/closes/{id}/preparations` | Instruction revisions |
| `POST /v1/closes/{id}/jobs` | Queue selected plan identities |
| `GET /v1/closes/{id}/jobs` | Durable states and resulting workpapers |
| `POST /v1/closes/{id}/jobs/{job}/cancel` | Request cancellation subject to current state |

## Runtime setup

Compose starts PostgreSQL, API, worker, frontend and scanner. Fresh volumes receive all numbered migrations; see [migration instructions](../infra/postgres/migrations/README.md) for upgrades. Native Go must receive environment variables explicitly.

The complete GitHub staging workflow deploys API/assets, worker and scanner. The worker receives only its required server credentials for enabled preparation, scheduled reads and approved delivery; API and worker share the encryption key. It is no longer a database-only worker. A cron wakes it once a minute; internal PostgreSQL polling handles work, with no subscribed Cloudflare Queue. This has ongoing container/database cost. [Environment settings](deployment.md#environment-settings)

Manual `POST /v1/closes/{id}/preparations` input cannot set the agent's verification or lineage metadata (`procedure`, `chart`, `billed_to`, `unrelated`, `payroll_precedent`, `recurring_accrual`). Those fields are assigned by the internal preparation paths, which verify their sources and policy. A manually prepared or amended draft remains manual; its supported method/input, source selection and explicit `revises` remain available. Carry-forward and register import retain their existing dedicated routes.

Invoice amount checks treat supported currency codes as tokens: letters such as `USD` embedded inside an issuer or reference identifier do not establish another monetary amount. Separate currency amounts, decimal figures and the existing source/total/charge checks retain their normal eligibility effects.

### Credits in a mixed supplier close

A supplier credit holds its supplier's invoice for review. A different supplier's invoice can continue when the same current company capture identifies exactly one original supplier bill/purchase and an unlinked credit with a different supplier ID and name. The source document must name that credit, supplier, client, currency and amount, be fully readable and name neither the current invoice nor its supplier. Missing or ambiguous identity, links to the current invoice, unknown accounts and credits on the prepaid account still hold preparation. Intake and the draft worker use the same current capture; this does not select a credit treatment or approve an entry.

Automatic prepaid preparation accepts account facts cited from its current applied procedure copy. The quoted account must still equal that approved account; another procedure/source cannot supply it. Invoice identity, amount, currency and service dates remain invoice facts, and current captured booking still determines eligibility. This avoids asking the preparer to re-enter accounts already approved in the procedure.

A separately uploaded payroll ledger whose own current model reading treats it as unsupported ledger support can be recognized through the payroll summary that cites it. An invoice reading is never overridden by that association. The payroll job must remain active/current, its primary summary must remain readable/current, and both saved source digests must match. The existing unread-page, image, vendor/invoice-reference and amount checks still decide whether that ledger is independent of an invoice. This removes an unrelated invoice hold; payroll setup/review and the ledger's own intake disposition remain separate.
