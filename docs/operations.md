# Operations and recovery

This is a runbook for the implemented controls, not an enterprise certification. Define owners, contractual retention, recovery objectives and escalation contacts before admitting client data. Deletion is always an explicit, admin-confirmed action; no automatic purge runs.

## Release and migration

Deploy the API and worker from the same reviewed revision. Fresh databases use migrations 001–024 in numeric order; existing databases apply only unapplied migrations. Stop worker and API traffic before a schema change, preserve a verified backup, and restart them after migration and readiness checks. Migration 024 preserves unattributed historical email delivery events; investigate them, because they block close deletion for that firm. The GitHub staging job checks required schema objects but does not perform production migrations or grant deployment authorization. See [onboarding](onboarding.md).

A healthy HTTP process is not proof of a functioning worker. Check API `/readyz` for the schema/database boundary and inspect the Work inbox for advancing durable state. Firm administrators can open **Firm operations and membership** or call `GET /v1/closes/operations` (`clients:all` and `operations:read`) for capacity, unattributed delivery-event counts and up to 100 aged/unknown preparation, attachment, email and posting alerts. Refresh after resolving items. An empty list is only a database snapshot; it is not an automated paging system. Configure external polling/on-call escalation before live service.

The default firm caps are 500 closes and 250 active preparation jobs. Close creation returns HTTP 429 when full; the database refuses new active jobs after the cap. Do not bypass either cap with direct inserts. A firm administrator may adjust `organizations.max_closes` and `max_pending_jobs` only through a reviewed database change, after checking capacity and backlog. Archive and delete only under the firm retention policy.

## Enrollment, revocation and data retention

WorkOS owns identity, MFA and role assignment; the application records firm membership decisions. First enrollment needs a WorkOS organization token with `org:enroll`, `members:manage` and `clients:all`. Use the dashboard enrollment form; do not hand-enter an organization ID into client data. Member management uses the exact WorkOS `user_` ID, an authority reason and optimistic history version. Revoking membership blocks subsequent workspace API calls; revoke the WorkOS session and role as well. Firm suspension requires `org:manage` and blocks workspace access and future worker claims. A suspended firm needs an authorized database change to resume after incident review; do not silently re-enable it.

For each close, a firm data administrator (`clients:all`, `data:manage`) records a retention date, optional legal hold and authority reference. `GET /v1/closes/{id}/retention` lists every known deletion blocker. No date, a future date, a hold, active jobs/links/attachments, uncertain email/posting attempts, connected or shared QuickBooks state, and unattributed delivery events matching this close's sent messages block deletion. Unrelated provider events remain visible in the firm operations count but do not hold its closes. Historical attribution requires a reviewed, close-specific database correction; there is no bulk-delete or self-service override. Export the current binder or review package first if policy requires it. `POST /v1/closes/{id}/delete` requires the exact client/month, current retention version, approved reason and explicit confirmation. A single transaction removes this close's active database rows and writes a minimal receipt (firm, close ID, period, SHA-256 digest of firm ID, lowercased client name and period separated by zero bytes, production posting count, actor, reason, time). The receipt contains no client name. Provider records and previously exported copies remain outside this database. Backups may retain deleted data until the provider's separately configured expiry; record that expiry in the client agreement. There is no automatic backup purge or legal discovery workflow.

## Backup

Use the managed PostgreSQL provider's encrypted point-in-time backups plus a tested retention policy. Restrict backup access: database backups contain client documents, replies, audit records and encrypted provider payloads. Keep the encryption key in a separate recoverable secret store. A database dump without the correct `SECRETS_KEY` cannot recover encrypted connections or queued email payloads.

For an explicitly approved database, native PostgreSQL custom-format exports preserve the schema and data. Supply connection details through your secret manager/`PG*` environment; do not paste URLs/passwords into a ticket or enable shell tracing:

```sh
umask 077
pg_dump --format=custom --no-owner --no-acl --file=/secure/approved-backup.dump
```

The operator chooses the protected output location and retention. This example is not an instruction to export production data automatically.

## Restore without repeating external actions

1. Restore to a new isolated database; never overwrite the live database to test recovery. Keep all workers stopped and outbound provider access disabled.
2. Match the schema/application revision and restore required secrets separately. Confirm database ownership/permissions. Do not start an old application against a newer schema without a compatibility review.
3. Verify organization ownership, client assignments, source hashes, original documents, review/sign-off history, preparation jobs, scope records and provider receipts. Test two-firm denied access using the API before opening traffic.
4. Review all email and posting attempts against provider history **before enabling workers**. A backup can precede an external action that actually succeeded. Database state alone cannot distinguish this case. Stable email keys only deduplicate within the provider's finite window; an old backup can reset the recorded first-attempt time. Do not let restored queued emails run automatically. Keep delivery disabled until each uncertain record is reconciled through an approved operational change.
5. Preparation calculations can resume using their durable identities after source/readback checks. OCR and model requests may incur repeat work/cost after recovery; do not confuse that with a repeated ledger write.
6. Record actual recovery time and the latest recovered transaction time. Only a timed hosted rehearsal with provider reconciliation can establish the deployment's RTO/RPO. Neither target is claimed by this repository.

## Runnable local rehearsal

With PostgreSQL native client tools and a local administrative user:

```sh
PGHOST=127.0.0.1 PGPORT=15432 PGUSER=close infra/postgres/check-restore.sh
```

Use your own local port/user. The script rejects non-loopback hosts, creates two uniquely named temporary databases, applies all migrations, inserts two synthetic firms with same-named clients, dumps/restores, verifies source hashes and durable jobs/scopes, and removes only its own databases and dump. No API, worker or provider is started. The rehearsal proves schema/data recovery for those fixtures, not live credentials, backup scheduling or hosted recovery objectives.

## Access and incidents

- Suspected firm exposure: stop provider activity and suspend the exact registered organization through an authorized database change; preserve evidence. Application checks reject subsequent requests/claims. Already-running work and already-downloaded data cannot be recalled.
- Individual access: revoke app firm membership and client assignments, then revoke WorkOS membership/roles/sessions. A previously issued token is denied by the app's current membership check on subsequent requests.
- If every firm administrator is revoked, app enrollment will not silently reclaim the firm. Restore an exact member only through an authorized, reviewed database change and WorkOS identity verification.
- Unknown external result: preserve the attempt and inspect provider canonical records. Never reset state to `queued` simply to make the dashboard green.
- Key loss: stop delivery, recover the matching key from secure backup and verify decryption on an authorized record. Do not replace it with a random key. Rotation/re-encryption tooling remains pending.
- Data removal: use the exact-close retention preview and deletion action; never bulk delete historical events to clear a blocker. Reconcile unresolved records and verify a deletion receipt plus absence of the exact close before reporting completion.

Pending enterprise controls include PostgreSQL row-level security, external alert delivery, key rotation, backup expiry verification, scoped MCP access and a hosted recovery exercise. Keep these gaps visible during any pilot review.
