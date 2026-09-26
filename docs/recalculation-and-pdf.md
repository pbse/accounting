# Revised calculations and PDF evidence

Open a saved draft and choose **Revise and recalculate**. The existing preparation form loads prior values and the latest replacement evidence. Statement/ledger CSV rows refresh from those sources. Review balances, page inventory, invoice terms and source locations; all method/booking confirmations start unchecked. Prepaid amounts and dates remain human-confirmed: OCR text does not establish accounting facts.

**Confirm revision and recalculate** saves the immutable instructions and queues the calculation in the same transaction. Identical retries reuse the job and result. The worker creates a new draft with `revises` pointing to its predecessor; the predecessor becomes out of date, its review history remains, and the new draft starts pending review. In-flight or uncertain sandbox posting prevents recalculation until resolved. An earlier unused posting approval is invalidated when its replacement draft commits. Evidence replacement alone invalidates affected work; it cannot safely infer changed invoice terms or accounting assumptions and therefore requires this confirmation step.

PDF intake supports 5 MiB and 20 pages per file. Text/CSV remains limited to 512 KiB. Originals stay immutable in the authenticated close, with a combined 100-file/20-MiB original-storage ceiling. PDF extraction runs in the independent worker: Poppler reads the text layer page by page, then Tesseract English OCR runs on pages without text. Each extracted page is numbered and labeled with its method. Text-bearing pages can contain additional unextracted image content; always inspect the downloaded original. PDF tables are not converted automatically to transaction CSV, and OCR accuracy is not a completeness certification.

Extraction claims use a three-minute fenced lease. A process crash leaves work reclaimable; extraction has a 90-second total deadline, 512-KiB output limit and 2,000-pixel raster limit. Linux commands also have CPU, address-space and file-size limits through `prlimit`. Parser subprocesses receive no database/provider credentials, use fixed arguments without a shell, and temporary files are removed after each attempt. Native macOS uses the deadline/output/page limits; Linux process resource limits are verified by CI, not the native runtime. Failed extraction remains visible and can be retried. Pending or failed extraction cannot supply new preparation instructions.

Inspect source text and download original are separate actions. Close packages include exact original PDF bytes, extracted text, extraction status/errors, source replacement metadata and all workpaper versions. Extraction content is rendered as escaped text in the UI.

## Local development

Compose builds the API/worker image with Poppler, Tesseract English data and `prlimit`; no extra configuration is needed for PDF extraction. For native Go development on macOS, run `brew install poppler tesseract`. For an existing database, apply all outstanding migrations using the [upgrade procedure](../infra/postgres/migrations/README.md#existing-database-upgrades) before restarting API and worker. A fresh Compose database applies every migration. PDF/OCR checks require these tools and generate a synthetic two-page mixed text/scanned fixture at test time.

Tool reference: [Tesseract command-line usage](https://tesseract-ocr.github.io/tessdoc/Command-Line-Usage.html).

## Hosted validation

Follow [staging setup](deployment.md) and the [acceptance matrix](onboarding.md#staging-acceptance-matrix). Verify a synthetic multipage PDF upload/extraction, a confirmed revised draft and its pending review state. Client-submitted attachments additionally require [malware scanning](cloudflare-upload-scanning.md) and firm release. CI or a deploy does not prove OCR accuracy, provider configuration or accountant acceptance.
