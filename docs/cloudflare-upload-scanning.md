# Required attachment scanning: Cloudflare and isolated ClamAV

Client PDF attachment release requires **Cloudflare WAF Malicious uploads detection** on a proxied custom API hostname. Obtain the Enterprise entitlement/add-on before enabling this flow. Buying the entitlement alone is insufficient: enable scanning, the managed result header, and the blocking rules below. PDFs use Cloudflare. TXT/CSV use a separate on-demand ClamAV service because Cloudflare excludes those formats. ClamAV is absent from the API and preparation worker images; it exists only in the scanner image. There is no runtime bypass.

## Trust and data flow

1. A scoped client link saves the reply and attachments in private quarantine. Saving does not establish a clean scan.
2. The browser sends each original PDF separately as a raw body to `POST /v1/client-request/scan`, using the same bearer link. One file per request avoids mistaking a clean object in a mixed upload for a verdict on every attachment.
3. The WAF must establish exactly one completely scanned, clean PDF. The managed `Malicious-Uploads-Detection: 3` header alone only means that an object was detected; it is **not sufficient without the blocking rules**.
4. The edge checks its configured hostname, Cloudflare request metadata and managed header, removes any caller-supplied internal attestation, and adds its private `UPLOAD_SCAN_KEY` for the container. It forwards the original body unchanged.
5. The API requires that private key plus the unexpired, consumed, non-revoked request link. It hashes the received bytes and finds the matching attachment within that exact request. Only this attachment receives a clean scan receipt. Firm release, scope checks and the 24-hour scan freshness requirement still apply.

The API origin/container must not be publicly reachable. The separate text-scanner Worker has a server-to-server endpoint authenticated before container startup; it never exposes an unauthenticated scan operation. `workers.dev` and preview URLs are disabled; do not enable an alternate ingress that bypasses these zone rules. Do not add WAF Skip rules for this route. Keep the scan key server-side, out of browser builds, logs and support tickets. Cloudflare processes document contents as part of scanning; include it in your firm's data-processing disclosures.

## Required setup

1. Use a proxied DNS hostname in the entitled zone. Enable **WAF → Malicious uploads detection** for that zone.
2. Enable **Transform Rules → Managed Transforms → Add malicious uploads detection header**. Do not create your own constant `Malicious-Uploads-Detection: 3` header.
3. Add the following two **Block** custom rules. Replace `api.example.com` with the exact API hostname. The spoofing rule executes in the WAF phase, before the managed transform adds its header.

Reject caller-supplied scan/attestation headers on the API hostname:

```text
http.host eq "api.example.com" and (
  any(http.request.headers.names[*] eq "malicious-uploads-detection") or
  any(http.request.headers.names[*] eq "x-close-upload-scan-key")
)
```

Reject every incomplete or unacceptable scan request:

```text
http.host eq "api.example.com" and
http.request.uri.path eq "/v1/client-request/scan" and
http.request.method ne "OPTIONS" and (
  http.request.method ne "POST" or
  http.request.body.size gt 5242880 or
  cf.waf.content_scan.truncated or
  cf.waf.content_scan.has_failed or
  cf.waf.content_scan.has_malicious_obj or
  cf.waf.content_scan.num_obj ne 1 or
  any(cf.waf.content_scan.obj_results[*] ne "clean") or
  any(cf.waf.content_scan.obj_types[*] ne "application/pdf")
)
```

OPTIONS is reserved for CORS preflight and cannot store a scan receipt. Rules must use Block, not Log or a browser challenge. Verify expression acceptance in your entitled zone before deployment; local tests cannot prove that zone rules are active. Enable no later transform that fabricates or replaces the scan result header.

4. Generate a random secret, for example `openssl rand -hex 32`. Install it directly in Cloudflare with `node scripts/cloudflare-credentials.mjs UPLOAD_SCAN_KEY staging < private-key-file`; use `production` for production. The helper scopes it to the API, queue and scanner. Do not paste it into source files. Set GitHub variable `UPLOAD_SCAN_HOST` to the bare API hostname. It must match `VITE_API_BASE_URL` when supplied, otherwise `STAGING_URL`. The deployment workflow requires these values and creates the Worker route `${UPLOAD_SCAN_HOST}/*`. Existing WorkOS, database and other deployment requirements still apply.
5. Apply migrations through 018. Remove old ClamAV workers before starting the new revision. Existing clean receipts retain their original attribution and 24-hour validity; pending files are not automatically scanned by a polling worker anymore. Clients must reopen an unexpired link and retry the original file through its scanner. Rejected/detected files cannot be overridden.

The shared secret authenticates the private edge-to-container hop. It does not activate the WAF or replace the mandatory rules. `/readyz` checks database schema, not this external security configuration.

## Isolated TXT/CSV scanner setup

The client sends each original text file to `POST /v1/client-request/scan-text`. The API authenticates the consumed request link, matches the file hash and validates UTF-8/type/size **before** invoking the scanner. It drops database locks while waiting, then rechecks link expiry/revocation, organization access and attachment disposition before storing the result. Caller-provided scan headers never authorize this path.

A client asked for a month's books ([books sources](books-sources.md#client-provided-books)) sends each CSV or Excel workbook to `POST /v1/client-request/scan-file` instead. The API checks it the same way, as a books file (a CSV without NUL bytes, or a workbook's zip signature, up to 50 MiB, on a request for books only), and forwards it whole to the scanner's binary `/scan-file` endpoint at the same address. The scanner takes binary files up to 50 MiB and scans them within larger limits than text (`--max-filesize=256M --max-scansize=1024M --max-files=1000`, 150 seconds), because a workbook is a zip whose sheets unpack to many times its size; anything past a limit is an alert, never a pass. A close keeps up to 250 MiB of books files apart from its 20 MiB of documents.

`services/scanner/Dockerfile` builds `/scanner` with ClamAV/FreshClam. It accepts only authenticated UTF-8 text uploads up to 512 KiB, has no database/model credentials, scans one file at a time and returns busy for concurrent requests. It updates official signatures on demand (at most every six hours per live process); scans reject definitions older than two days. Missing/stale signatures, failures, detections and timeouts never produce a clean receipt. Signature updates have a two-minute deadline and each scan a 90-second deadline, within a three-minute request budget.

`workers/scanner` exposes only the authenticated `/scan` operation. It starts a single `standard-4` scanner container (4 vCPU, 12 GiB) on demand, with no cron, and sleeps it after two idle minutes. This **reduces idle runtime, not the engine's peak memory requirement**. Cold starts may redownload signatures and take several minutes; interrupted/busy scans are retried from the receipt page. Monitor actual usage before claiming a cost saving.

Set GitHub variable `TEXT_SCANNER_URL` to `https://close-upload-scanner-staging.<your-workers-subdomain>.workers.dev/scan`. The deployment workflow preserves the scanner's installed `UPLOAD_SCAN_KEY` and updates the API scanner URL; the direct installer keeps the API/queue/scanner key coordinated. The scanner URL does not need the PDF WAF rules; it rejects requests before starting a container unless their server credential is valid. Keep this URL server-side and use HTTPS in hosted environments. No scan endpoint accepts a user-supplied destination URL.

## Local development

Generate `UPLOAD_SCAN_KEY` once (`openssl rand -hex 32`) and save it privately in `.env`. Run `docker compose up --build`: the private `scanner` service runs a small HTTP listener; ClamAV and signature updates run only when requested. Compose passes its internal URL to the API and exposes no scanner host port. API and preparation containers do not install ClamAV or reserve scanner memory.

Native development can run the separate binary with `UPLOAD_SCAN_KEY`, `LISTEN_ADDR=127.0.0.1:8082` and optional `CLAMAV_DATABASE_DIR`; install `clamscan`/`freshclam` only on that scanner host. Set the API's `TEXT_SCANNER_URL=http://127.0.0.1:8082/scan` and matching key. Outbound access to official ClamAV signature mirrors is required. Start the scanner with `go run ./cmd/scanner` from `services/backend`; its startup does not load signatures.

Text replies and TXT/CSV scanning work locally with this service. **PDF release still needs a real Cloudflare scan** on the configured custom hostname. Local emulation does not emulate WAF and has no fake clean-result switch. Use staging with synthetic PDFs to verify that path.

Application checks use mocked text-service and synthetic edge receipts to verify routing, identity and release. The opt-in scanner check exercises real clean/EICAR scanning:

```sh
node --experimental-strip-types --test workers/edge/src/upload-scan.test.ts
cd services/backend
TEST_DATABASE_URL='postgres://close@127.0.0.1:15432/accounting_dev?sslmode=disable' \
  go test ./internal/transport -run 'TestCoverageRequestsQuarantineAndResolution|TestScopedClientReplyResumesJob'
TEST_CLAMAV_DATABASE_DIR=/tmp/close-clamav go test ./internal/scanning -run TestIsolatedScanner
```

## Usage and activation checks

The client submits once; the browser then scans each original file. If scanning fails after reply receipt, the reply remains saved and that file remains quarantined. The receipt page offers **Retry attachment scanning**, which accepts only previously submitted original bytes. It cannot append a new document or change the reply. After the private link expires, use a fresh reviewed request. Staff cannot force a clean verdict or enqueue a local scanner.

Before enabling real client uploads, verify on the actual custom hostname:

- A harmless PDF receives a clean receipt with matching stored digest; firm release resumes preparation.
- A second, different file cannot acquire the first file's receipt. Mixed/multiple objects, non-PDF objects, truncated/failed scans and absent managed headers cannot pass the scan endpoint.
- An EICAR test upload is blocked and creates no clean receipt. Use only synthetic files and Cloudflare's official test guidance, never a live malicious document.
- Forged scan headers, direct origin requests and alternate hostnames cannot establish a clean receipt.
- Text-only replies still work; scan retries are idempotent. Revoked/expired links and another request's files fail.

Cloudflare does not scan every file format; notably CSV is excluded, which is why text files use the isolated scanner. Never describe extension/type validation as an antivirus scan. Also verify a clean TXT/CSV and EICAR through the separate service, plus refusal when its credential/signatures are unavailable. No scanner guarantees that every threat will be detected. These instructions are required setup; the repository's passing checks do not claim hosted WAF activation or live malware detection.

Primary references: [Malicious uploads detection and limitations](https://developers.cloudflare.com/waf/detections/malicious-uploads/), [managed result header](https://developers.cloudflare.com/rules/transform/managed-transforms/reference/#add-malicious-uploads-detection-header), [example blocking rules](https://developers.cloudflare.com/waf/detections/malicious-uploads/example-rules/).
