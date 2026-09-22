# Next-month prepaid carry-forward

From a client close, open **Carry work into the next month** and select **Preview next month**. Select up to 20 eligible prepaid schedules **or** up to three enabled current firm procedures, optionally include unresolved follow-up, acknowledge the scope, and queue preparation. Schedules and procedures are separate actions because applying a newer procedure can invalidate a carried schedule. The app opens the next calendar month's close, creating it if needed while preserving existing work.

## What carries forward

Only a current, approved prepaid workpaper with linked evidence and service coverage in the next month is eligible. Fully amortized schedules and unreviewed/stale versions are explained in the preview. Reconciliation requires new monthly evidence and is excluded. The preview inspects at most 100 prepaid workpaper versions; larger histories stop explicitly.

The existing deterministic calculator supplies the next month's expense and remaining balance, including the final-cent residual. Each selected schedule gets a durable job. Retrying the same source workpaper/review receipt reuses its job. Cancelled or failed jobs can be explicitly selected again after their inputs are current; the same job resumes. A changed, newly approved predecessor creates a fresh historical version rather than rewriting prior work.

Source originals and extracted text are copied into the target close within existing evidence quotas, deduplicated by digest. Each new workpaper records the predecessor close, period, draft digest, review receipt and old-to-new source IDs. Existing same-close evidence checks remain active. A change to any ancestor's sources or review invalidates dependent carried work, including later generations. The worker also rechecks the predecessor before preparing.

Selected current approved firm/client procedures are applied as cited evidence in the next close. The preview shows their exact version IDs and disabled status. Applying a newer procedure to an existing next-month close can invalidate work that used the previous version; the same active-attempt and evidence-capacity guards apply. No procedure authorizes a journal or replaces per-draft accountant approval.

The preview also lists unresolved prior jobs and unapproved/blocked drafts. An explicit option carries up to 20 as incomplete external follow-up steps in the next close's scope. Each step names the originating close and job/draft ID, remains a readiness blocker, and is not a new-period accounting calculation or automatically resumed prior-period job. Refresh the preview if prior work changes. Repeating the same selection does not duplicate steps. A signed-off target must be explicitly reopened first. To resolve a step, inspect the prior work, collect fresh period evidence where needed, and record a completion reference in the next close's scope.

## Accounting and review boundary

This is a **schedule carry-forward**, not a ledger roll-forward or recurring journal service. The opening scheduled prepaid balance comes from the prior workpaper; it is not verified against the ledger. The initial method's full-expense-booking assumption cannot simply be repeated after prior adjustments.

New drafts therefore contain no journal proposal or prior month's booked balance/reference, reset the method/booking confirmation flags, and inherit no review or posting approval. Historical credits still affect the schedule, but their prior ledger confirmation is not reused. Reviewers can approve the schedule itself after inspecting current terms and evidence; that does not authorize a journal. Existing sandbox code rejects a workpaper without a proposal, and carried drafts show no posting action.

To correct carried terms, revise and review the originating schedule, then carry the new version forward. The existing initial-booking revision form does not accept carried drafts. Prior versions stay in history and become stale through lineage. Repeating this procedure for another month requires that month's schedule to receive its own review first.

Non-goals: automatic ledger balance verification, journal creation/posting, reconciliation calculation carry-forward, client requests, sign-off, provider activation and cloud deployment.

## Setup and implementation

Apply migration 010 after 001–009 and restart the API and worker. For an existing Compose volume:

```sh
docker compose exec -T postgres psql -U close -d close -v ON_ERROR_STOP=1 < infra/postgres/migrations/010_carry_forward.sql
docker compose up -d --build api worker
```

Fresh Compose databases apply all migrations. No new dependency, service or secret is required.

- GET /v1/closes/{id}/carry-forward previews eligibility and the following month.
- POST to that path requires reviewer authority and `confirmed=true`. Schedules use the exact `id`, `digest` and `review_id` from the preview; procedures use their exact version IDs; unresolved follow-up uses the preview's digest.
- The target client/month is chosen server-side from the predecessor. Clients cannot supply a different target or fabricate carry-forward metadata through the ordinary preparation route.
- Existing preparation jobs and calculators perform the work (Ponytail rung 2); PostgreSQL source/review views preserve transitive freshness (rung 4). No second scheduler or recurring-procedure framework.

Acceptance is covered by TestCarryForwardRoundingLineageAndFreshReview: reviewed-only selection, year rollover, retry reuse, multi-source lineage, fresh approvals, final rounding, end-of-term exclusion, two-generation invalidation and enqueue-to-execution staleness. Existing evidence, review, agent, sandbox and export checks cover the direct consumers. No live ledger result is implied.

Local synthetic example: September close 121 and October close 122 are retained for inspection. They contain no real client data.
