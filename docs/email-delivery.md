# Approved client email delivery

Email is optional. Local development works without Resend: approve a request and share its secure link manually. Provider checks use synthetic messages and a mocked HTTP transport; no live delivery has been validated.

## Configure a hosted firm

1. Apply all migrations to the development/staging database using the migration steps in [onboarding](onboarding.md). Back up hosted data before migrations.
2. Generate a private 32-byte encryption key (`openssl rand -hex 32`) and store it as `SECRETS_KEY` in both API and worker environments. Never commit it. Set API `PUBLIC_APP_URL` to the exact HTTPS SPA origin, without a path, query or fragment. The same key must be available after restarts and restores.
3. In Resend, verify the firm's sending domain, enable sending and disable click tracking. The API key needs permission to read that domain and send email. Use a firm-specific key; never share one firm's credential with another firm.
4. A hosted firm administrator needs `clients:all` and `integrations:manage`. In Client closes → Firm email, enter the sender address, Resend domain ID and key, then confirm firm ownership. The server verifies the exact domain before saving encrypted credentials. Responses never return the key.
5. Configure delivery tracking before relying on reminders. In Resend, add an HTTPS webhook at `<API origin>/v1/webhooks/resend/<connection id>` for `email.sent`, `email.delivered`, `email.delivery_delayed`, `email.bounced`, `email.complained`, `email.failed` and `email.suppressed`. The firm-email settings show the exact URL. Save its `whsec_...` signing secret there; it is encrypted and never returned. Use the same firm-specific account as the sender. In Cloudflare, allow provider POSTs to this route without browser challenges or Access login; signature verification protects it. Do not rewrite the body or strip `svix-id`, `svix-timestamp` or `svix-signature`. Keep server time synchronized. Rotate by saving the replacement secret; retained connections still accept signed late events after a sender is replaced.
6. A reviewer needs `workpapers:review`, `requests:send` and access to the client. Give client accounting professionals an explicit client assignment; do not grant `clients:all` unless firm-wide access is intended. WorkOS membership and role setup are described in [onboarding](onboarding.md).

GitHub staging deployment accepts optional `SECRETS_KEY` as an environment secret and derives `PUBLIC_APP_URL` from `STAGING_URL`. The API and queue worker containers both receive the encryption key. Configuration is not activation: verify the sender and send a separately authorized synthetic request after deployment.

## Reviewer and client flow

Upload evidence → review proposed facts/questions → approve the exact client request and recipient → expand email delivery → check the current verified sender and recipient → explicitly approve sending. Approval creates one durable encrypted outbox record and a new seven-day private link; it revokes unused older links for that request. The message contains generic instructions and the private link, not accounting facts.

The worker checks current evidence, request, client access, organization, link, sender and approval window. Sending is limited to 30 minutes or the approving session's expiry, whichever is earlier. The worker re-verifies the sender. A new request is not sent merely because an agent proposed it. A client reply uses the existing scoped reply flow and resumes only the affected jobs. In **Coverage and client evidence → Approved consolidated requests**, the same approval sends one link covering all selected questions and pinned jobs, including requests created before any job exists.

For inbound email replies, enable Cloudflare Email Routing plus addressing and set `EMAIL_INTAKE_ADDRESS` to the receiving address and `EMAIL_INTAKE_KEY` to a separate private 32-byte key on both the edge and API. Route the address to the edge Email Worker; the approved outbound message will use a `+reply-<outbox id>` reply-to address. The edge forwards only the bounded raw message and envelope to the internal API. Every reply starts in **held** state, with card, bank account and identity numbers and sign-in secrets already removed (see [client request links](client-request-links.md)); the stored duplicate check uses the redacted text, not the raw message. A reviewer sees the envelope and message From addresses and text in the close, then releases or rejects it. Release requires an exact approved-recipient address match, a live request/link, unchanged scope, and a recorded independent sender-verification reference from a known contact channel; matching email headers alone do not prove identity. A mismatched sender remains held and cannot resume preparation. Attachments sent by email are not accepted through this path; use the scanned secure request upload instead. Local development still works without inbound routing: clients use the secure request link.

The Work inbox shows queued, sending, accepted, failed, unknown or cancelled. **Accepted means Resend returned an email ID; it does not prove delivery or a client reply.** Delivery tracking separately shows sent, delivered, delayed, bounced, complained, failed, suppressed or unconfirmed. Delivered means the recipient mail server accepted the message, not that the client read it. Signed duplicate events are deduplicated; out-of-order sent events cannot erase delivery, and delivery cannot erase adverse events. Events arriving before the send response are retained and matched by connection, provider ID and recipient. Binders include approval, attempt and delivery metadata, never credentials or link tokens.

## Individually approved reminders

After a message is **accepted / delivered**, expand **Approve a reminder**, review the sender and recipient, and approve that specific message. Its subject identifies it as a reminder; it reuses the private link and original expiry. Each approval creates its own immutable outbox record linked to the previous email. Duplicate clicks/retries return that same record. History shows every initial email and reminder with approval attribution.

There is no automatic schedule. The API and worker both require an unanswered, current request, current client access, the same enabled sender and an unexpired, unused, unrevoked link. A bounce, complaint, failure or suppression on any email for the request blocks further reminders. A new reminder requires delivery confirmation for its predecessor. A reply, changed evidence/scope, or expired approval stops queued work; a provider request already in flight cannot be recalled. Resolve a blocker and obtain fresh review rather than blindly creating another request.

## Recovery and boundaries

- An uncertain response retries the same encrypted payload and idempotency key, at most three attempts and within 23 hours of the first attempt. Resend documents a 24-hour idempotency window. Approval expiry may stop retries sooner.
- `unknown` requires investigation in Resend using the recorded ID/key and time. Do not create another request or send manually until the prior outcome is resolved. There is no automatic retry of a terminal unknown record.
- `failed` means the attempt did not obtain acceptance; review the reason and provider records before issuing another approved request. `cancelled` means preconditions changed before any attempted send.
- Missing/wrong encryption configuration leaves the queued record visibly blocked, retrying configuration once per minute until its approval expires. Restore the correct key; changing the key cannot decrypt existing records.
- Disabling/replacing a sender prevents queued work from using it. An already running provider request may finish. Revoking a client assignment affects subsequent checks; role changes in WorkOS remain bounded by issued token expiry. The app cannot recall already sent email.
- Keep the encryption key securely backed up separately from database backups. The offline `rotate-secrets` command in [operations](operations.md) re-encrypts the three stored secret stores with the old and new keys inside one database transaction while API and worker are stopped. Never put the previous key in a running API, worker, Cloudflare secret or GitHub action. Terminal-outcome reconciliation and provider-side deletion remain separate work.

## Local checks and staging acceptance

Local development needs no Resend account. Manual private links work with the workspace token; live approval requires a hosted firm session. With all migrations applied, run the focused synthetic check:

```sh
TEST_DATABASE_URL="$DATABASE_URL" go -C services/backend test ./internal/delivery ./internal/transport -run 'TestWebhookOfficialVector|TestApprovedEmailDurableRetryAndScope' -count=1
```

Use a disposable development database and keep other workers stopped while running integration checks. The test injects a synthetic hosted actor and replaces the HTTP transport; it never sends real email. It covers two-job consolidated delivery, durable retries, forged and duplicate webhooks, early/out-of-order delivery, per-reminder approval, link reuse and a bounce cancelling a queued reminder.

Before activation, separately authorize a synthetic recipient on staging: configure the verified domain and webhook, approve a grouped email, verify both provider and app delivery history, approve one reminder, submit a secure reply, and verify affected jobs resume and no reminder remains eligible. Exercise a provider test bounce and webhook retry. Docker/Cloudflare execution and live provider delivery must be validated in that environment; local mocks do not prove them.

Provider references: [send email](https://resend.com/docs/api-reference/emails/send-email), [idempotency keys](https://resend.com/docs/dashboard/emails/idempotency-keys), [domain verification response](https://resend.com/docs/api-reference/domains/get-domain).

Webhook references: [Resend verification](https://resend.com/docs/webhooks/verify-webhooks-requests), [Svix signing format](https://docs.svix.com/receiving/verifying-payloads/how-manual).
