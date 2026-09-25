# Next-month prepaid carry-forward

From a client close, open **Carry work into the next month** and select **Preview next month**. Select up to 20 eligible prepaid schedules **or** up to three enabled current firm procedures, optionally include unresolved follow-up, acknowledge the scope, and queue preparation. Schedules and procedures are separate actions because applying a newer procedure can invalidate a carried schedule. The app opens the next calendar month's close, creating it if needed while preserving existing work.

Signing off a close does this automatically: every eligible reviewed schedule that is not already carried (up to 20) is queued for next-month preparation, under the same rules and as the reviewer who signed off; the worker then prepares each draft for review. The sign-off still stands if the carry-forward cannot run; the page says how many were queued, or that next month needs to be started from **Carry work into the next month**. Loans are not carried: next month needs the lender's new statement.

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

## Approve the schedule, not every month

On an approved prepaid schedule, a reviewer can grant a **standing approval** with a reason (`POST /v1/closes/{id}/workpapers/{paper}/standing-approval`, `{"action":"grant","reason":…,"confirmed":true}`). The worker then checks carried schedules once a minute. It approves each later month's carried schedule under the granter's name, noting the standing approval, when all of these hold:

- the draft is in the schedule's carry-forward lineage (on the granted paper or any carried descendant);
- it is unreviewed and unchanged: its predecessors' drafts and approvals are current, it is not a revision (including a monthly comparison with no adjustment), it proposes no journal entry, and it has no blockers;
- its close is not signed off;
- the grant is not revoked, and the granting schedule is still approved with the same approval it was granted on;
- the firm is enabled and the granter is still an enabled member of it;
- the ordinary review checks pass: linked evidence, accounting basis, and the firm's independence rule, applied to the granter.

**Recurring accruals and loans** can carry a standing approval the same way. A grant on an approved accrual using the fixed recurring method covers each later month's recurring accrual in its lineage: that accrual carries the same amount, accounts and rule, and a person linked this month's incurred-service evidence. A grant on an approved loan draft covers later months' loan drafts for the same client whose terms all match the granted paper: lender, agreement, currency, rate, day count, accounts, interest and current-portion choices, schedule reference, rate changes, fees and method. Only this month's figures may differ (statement, payments, principal, booked balances, principal due in 12 months). A loan draft with any open question is not covered; the loan calculation asks one whenever the lender's interest or the ledger's principal does not tie. Accrual and loan drafts may propose their month-end entries, because posting any entry still needs a person's posting approval. A carried prepaid schedule still must propose no entry.

A grant rests on the granter's review authority when they granted it. It stops when they leave the firm; if their review role changes, revoke it. **Revoke** on any covered draft ends it. A revised schedule is a new schedule and needs its own grant. Monthly entries still need a person. The review history shows each automatic approval. The schedule page says when a grant is no longer in effect, for example after the schedule was reviewed again. Each approval locks the granting schedule's close, the grant and the firm's row before its final check, so a revoke, a new review of the schedule or a firm suspension cannot race it.

## Refresh a recurring month

In the new close, use **QuickBooks sandbox ledger evidence → Capture this month’s accrual reports and plan coverage**, then **Refresh recurring prepaid work → Compare current period**. The latter shows each latest carried schedule, its expected closing prepaid balance, and the matching QuickBooks prepaid account's Trial Balance row and columns. The account name must match the schedule after trimming spaces and ignoring case, its subtype must be `PrepaidExpenses`, and a USD accrual Trial Balance row must exist exactly once. Missing or ambiguous rows stop the comparison; a missing row is never treated as zero. Download the full snapshot to inspect the report cutoff and raw cells. For large captures, the linked evidence is an index and the full snapshot supplies the cited row.

**The agent prepares this comparison itself** once a minute when the schedule's Trial Balance row is unambiguous and on its normal side (debit for prepaids, credit for deferred revenue and accumulated depreciation). It reads the booked balance from that row and queues the same revision a person would, as `scheduled-recurring-comparison`, with a ledger reference that says the balance was read by the agent and must be confirmed before approving. It skips a schedule when the row is ambiguous, on the other side, unreadable or not a whole number of cents, when the snapshot is stale, the close is signed off or the firm is disabled, when this capture was already used for it, or when any earlier comparison of it against this capture exists, including a person's failed one. Each schedule is queued on its own, so one refusal does not undo the others, and reaching the firm's job limit stops the rest until the next minute. The resulting draft is pending: a person confirms the balance, account, cutoff, method and entry when approving it, and posting keeps its own approval. Otherwise the client’s accounting professional confirms the actual booked balance, account, cutoff, method and synthetic examples here. Enter the balance in exact USD cents; the page shows the difference from the carried schedule. **Prepare reviewed comparison** queues a durable revision using the current snapshot as linked evidence. The existing calculator proposes any supported difference for fresh workpaper review. A repeated submit of an in-flight comparison reuses its job. New captures supersede earlier snapshot evidence, make source-dependent reviews stale and allow a new comparison from the latest version. Prior reviews and posting records remain in history. A signed-off close must be reopened before queuing new work.

The QuickBooks account directory's `CurrentBalance` is never used as a month-end balance. Outside the agent-prepared comparison above, the system does not infer debit/credit signs from report cells or confirm a provider balance automatically, and it never posts a journal. If an account name differs from the schedule, resolve the mapping in the prior reviewed schedule or prepare manually with supported evidence. Without a sandbox connection, carry-forward schedules still work locally, but this QuickBooks comparison remains unavailable.

Non-goals: automatic ledger balance certification, journal posting, reconciliation calculation carry-forward, client requests, sign-off, provider activation and cloud deployment.

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
