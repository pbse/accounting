# Operations and recovery

This is a runbook for the implemented controls, not an enterprise certification. Define owners, contractual retention, recovery objectives and escalation contacts before admitting client data. Automatic deletion remains disabled until an approved retention/hold policy exists.

## Release and migration

Deploy the API and worker from the same reviewed revision. Fresh databases use migrations 001–017 in numeric order; existing databases apply only unapplied migrations. Stop worker and API traffic before a schema change, preserve a verified backup, and restart them after migration and readiness checks. The GitHub staging job checks required schema objects but does not perform production migrations or grant deployment authorization. See [onboarding](onboarding.md).

A healthy HTTP process is not proof of a functioning worker. Check API `/readyz` for the schema/database boundary and inspect the Work inbox for advancing durable state. Monitor failed jobs, old `waiting_evidence`/`interpreting`/`queued` work, extraction failures, email `unknown`, and sandbox `unknown`/`executing`. The current application does not provide alert delivery or an on-call integration. Do not report these manual checks as automated monitoring.

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
- Individual access: revoke client assignments and WorkOS membership/roles. Issued tokens may retain roles until expiry; token/session revocation integration remains pending.
- Unknown external result: preserve the attempt and inspect provider canonical records. Never reset state to `queued` simply to make the dashboard green.
- Key loss: stop delivery, recover the matching key from secure backup and verify decryption on an authorized record. Do not replace it with a random key. Rotation/re-encryption tooling remains pending.
- Data removal: no bulk delete or cascading cleanup against client data without the approved retention, legal-hold and audit requirements. Synthetic test cleanup is isolated from client datasets.

Pending enterprise controls include PostgreSQL row-level security, self-service membership administration, malware quarantine/scanning, firm-wide resource budgets, automatic alerting, retention/hold enforcement, key rotation and a hosted recovery exercise. Keep these gaps visible during any pilot review.
