# Revised calculations and PDF evidence

Open a saved draft and choose **Revise and recalculate**. The existing preparation form loads prior values and the latest replacement evidence. Statement/ledger CSV rows refresh from those sources. Review balances, page inventory, invoice terms and source locations; all method/booking confirmations start unchecked. Prepaid amounts and dates remain human-confirmed: OCR text does not establish accounting facts.

**Confirm revision and recalculate** saves the immutable instructions and queues the calculation in the same transaction. Identical retries reuse the job and result. The worker creates a new draft with `revises` pointing to its predecessor; the predecessor becomes out of date, its review history remains, and the new draft starts pending review. In-flight or uncertain sandbox posting prevents recalculation until resolved. An earlier unused posting approval is invalidated when its replacement draft commits. Evidence replacement alone invalidates affected work; it cannot safely infer changed invoice terms or accounting assumptions and therefore requires this confirmation step.

PDF intake supports 5 MiB and 20 pages per file. Text/CSV remains limited to 512 KiB. Originals stay immutable in the authenticated close, with a combined 100-file/20-MiB original-storage ceiling. PDF extraction runs in the independent worker, page by page:

- Poppler reads each page's text layer.
- With `GEMINI_API_KEY` on the worker, every page is also rendered (`pdftoppm`, 2,000 px PNG) and sent to Google's Gemini (`generateContent`, model `GEMINI_OCR_MODEL`, default `gemini-3.8-flash`, temperature 0, 30 seconds a page).
  - On a page with a text layer, Gemini's reading is used only when it says exactly the same thing, apart from whitespace and line breaks. Every character still counts: figures, signs, parentheses, commas, decimal points, words, case and order. Otherwise the exact text layer is kept, so a misread figure, a swapped row or a changed word is never taken.
  - On a scanned page, Gemini's reading is used and labelled to verify against the original.
  - Each Gemini call counts against the firm's daily model-call limit.
- Tesseract English OCR reads a scanned page when Gemini is not configured, fails, returns nothing, or is ruled out by the firm's limit or the document's time (the last 40 seconds of its 150 are kept for local reading).

Each extracted page is numbered and labeled with its method: `PDF text`, `Gemini <model version>; the same text as the PDF text layer`, `PDF text; Gemini's reading differed`, `Gemini <model version> (verify against original)` or `OCR (English; verify against original)`. The key goes only in the `x-goog-api-key` header, never to a subprocess or a log line. Gemini sits outside the [LLM gateway](llm-gateway.md), whose requests are text; it is a small client in `documents/gemini.go`. Model and request shape were checked on 2026-10-07 against https://ai.google.dev/gemini-api/docs/models (updated 2026-10-06) and https://ai.google.dev/api/generate-content. Firm settings (**Reading PDFs**) and the security view show whether Gemini is in use, as the worker reports it in its heartbeat. Text-bearing pages can contain additional unextracted image content; always inspect the downloaded original. PDF tables are not converted automatically to transaction CSV, and OCR accuracy is not a completeness certification.

Extraction claims use a three-minute fenced lease. A process crash leaves work reclaimable; extraction has a 90-second total deadline, 512-KiB output limit and 2,000-pixel raster limit. Linux commands also have CPU, address-space and file-size limits through `prlimit`. Parser subprocesses receive no database/provider credentials, use fixed arguments without a shell, and temporary files are removed after each attempt. Native macOS uses the deadline/output/page limits; Linux process resource limits are verified by CI, not the native runtime. Failed extraction remains visible and can be retried. Pending or failed extraction cannot supply new preparation instructions.

Inspect source text and download original are separate actions. Close packages include exact original PDF bytes, extracted text, extraction status/errors, source replacement metadata and all workpaper versions. Extraction content is rendered as escaped text in the UI.

## Local development

Compose builds the API/worker image with Poppler, Tesseract English data and `prlimit`; no extra configuration is needed for PDF extraction. For native Go development on macOS, run `brew install poppler tesseract`. For an existing database, apply all outstanding migrations using the [upgrade procedure](../infra/postgres/migrations/README.md#existing-database-upgrades) before restarting API and worker. A fresh Compose database applies every migration. PDF/OCR checks require these tools and generate a synthetic two-page mixed text/scanned fixture at test time.

Tool reference: [Tesseract command-line usage](https://tesseract-ocr.github.io/tessdoc/Command-Line-Usage.html).

## Hosted validation

Follow [staging setup](deployment.md) and the [acceptance matrix](onboarding.md#staging-acceptance-matrix). Verify a synthetic multipage PDF upload/extraction, a confirmed revised draft and its pending review state. Client-submitted attachments additionally require [malware scanning](cloudflare-upload-scanning.md) and firm release. CI or a deploy does not prove OCR accuracy, provider configuration or accountant acceptance.
