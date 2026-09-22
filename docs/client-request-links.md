# Private client request links

The original text reply loop is now extended with [consolidated coverage requests and scanned attachments](coverage-requests.md): approve a request → issue a private link → share it manually → receive the original reply → resume the same preparation job → review proposed facts again.

## Reviewer experience

In Work, approve the exact recipient and wording using the existing request review. Choose **Create new private reply link**, copy the URL while it is visible, and share privately with that approved recipient through your authorized channel. Creating a link does not send email or claim delivery. Sending requires the separate [email approval](email-delivery.md).

Links expire after seven days. **Revoke reply link** invalidates an unused link immediately; creating a replacement also revokes the previous link. The raw secret is shown only at creation and is not recoverable from the database. A lost creation response can be resolved by issuing a replacement.

The link exposes only the approved request wording, client label and period. It cannot open workpapers, source documents, other requests or the firm workspace. Anyone holding it can read and reply, so it is suitable only where possession-based access is appropriate to the request. The app does not verify the holder's identity or ownership of the intended email address.

Changed evidence or selected workpaper review invalidates the request context. Use **Withdraw request and recheck evidence**, review the new proposals, and approve a fresh request. Old links stay unusable. Cancelling a preparation also stops replies. Link and request history remain in the close export, without secrets or their hashes.

## Client experience

The client opens the link without creating an account, sees the exact approved question, and submits one text reply after confirming the entity/period. Original wording is preserved, including whitespace. The page shows a receipt after durable storage and resumption. An identical retry is idempotent; a different second reply is rejected and directs the client to contact the firm.

A reply creates source evidence and a linked work reply atomically with returning the existing job to `waiting_evidence`. The worker uses the configured interpreter or local labeled-field parser. New facts still require human review and existing accounting confirmations. A reply never approves treatment, posting or firm sign-off.

Clients can attach PDFs through Cloudflare scanning and TXT/CSV through the separate on-demand ClamAV scanner. Mandatory malware scanning and firm release precede preparation; see [setup and limits](coverage-requests.md). Single-job and consolidated requests use the same approved Resend outbox, signed delivery tracking and individually approved reminders; see [email setup](email-delivery.md). Client accounts and verified-recipient authentication remain pending.

## Local setup and API

Apply `infra/postgres/migrations/009_client_request_links.sql` after 001–008 and restart the API. Fresh Compose databases apply it automatically. Existing volumes can use:

```sh
docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/009_client_request_links.sql
docker compose up -d --build api worker
```

Text replies require no new secrets. Attachments additionally require migration 018 and the Cloudflare setup described in the linked guide. Local development retains the private workspace token; hosted reviewers use the existing organization boundary and `workpapers:review` permission. Cloud activation and HTTPS deployment remain separate.

- Existing authenticated work actions add `link` and `revoke_link`, with current `revision` and approved `request_id`.
- `retry` on unanswered requests withdraws access and resumes evidence review.
- `GET /v1/client-request` reads a scoped request; `POST` submits `{body, confirmed:true}`.
- Public request calls authenticate using the link token as an Authorization bearer token. They never accept client/job/request IDs from the client.
- The browser receives the secret in a URL fragment, removes it from the address bar, keeps it in memory, and uses no-referrer/no-store requests. Refreshing the page requires reopening the original link. The client route does not initialize AuthKit.
- PostgreSQL stores only a SHA-256 token digest and issuance, expiry, revocation and consumption records. Go's `crypto/rand.Text` supplies at least 128 bits of entropy. Atomic row locks serialize reply, replacement, revocation and workspace actions.

The handler and existing staff reply capture reuse one evidence/queue writer (Ponytail rung 2). Randomness, hashing and HTTP use the standard library (rung 3); the UI uses native forms (rung 4). No client portal framework or second workflow engine was introduced.

## Verification boundary

`TestScopedClientReplyResumesJob` covers issuance authority, scope, rotation, revocation, expiry, stale context, exact original preservation, idempotent replay, receipt, resumption and withdrawal. Existing coordinator, agent and package checks cover the direct consumers. Live delivery, hosted access and model accuracy remain unverified.
