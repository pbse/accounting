# Approved procedures and client accounting authority

The client’s accounting professional approves accounting treatment. This feature records approved instructions and optional account names for the supported prepaid, accrual and reconciliation methods, and enables loan schedules per client (a loan procedure pins the `lender_statement_v1` method; see accounting-methods.md). It does not enable new arithmetic methods, invent materiality thresholds or treat instructions as proof of ledger booking.

## Use the flow

1. Open a client close → **Approved procedures and client overrides**.
2. Choose a firm default or a client override and the supported work kind. Enter approved instructions, optional prepaid account names, and the accounting professional/approval reference and reason. A reconciliation procedure can also approve **card accounts**. A card account is one the captured chart names a credit card. With this approval, the agent reconciles a card that ties without a person confirming its reading, as a card liability (`card_liability_month_v1`); without it, each card waits for a person (migration `173_card_procedures.sql`).
3. Select **Review affected closes** before publishing. The preview shows the scoped client periods, pinned procedure sources and draft counts (latest 100, with a total). Confirm authority and publish against that exact impact digest. The server stores a new immutable version, with the authenticated actor; stale editors or changed close inventories cannot silently reuse the preview.
4. A client override replaces the firm default for that work kind. A disabled client override suppresses the default for new context. Disabling does not erase existing pinned records, but a pinned copy that is no longer the scope's current, enabled version authorizes nothing further: saving a draft under it, queued straight-through work and approving its drafts are refused until the current version is applied to the month. Approving or posting from a draft is also refused once the scope's latest version is disabled (the method was retired), or when the draft records a method other than the current version's; an approval given before the change authorizes no posting.
5. On initial evidence intake, effective procedures attach as immutable source text when the close has no pinned version. Local proposals and model inventory include that context. Any proposed account mapping is cited and still needs review and booking confirmation.
6. Existing closes stay pinned when a new version is published. To change one, inspect the effective version, acknowledge the impact and choose **Apply version to this close**. The previous procedure source is superseded; dependent reviews/sign-off become stale. Recheck the job or prepare a revised draft from current evidence. Reapplying the same version does not duplicate sources.

## Learn a reviewed method rule

Open **Learn a procedure from evidence or reviewer edits** inside a client close. Upload a firm checklist or policy there; it is stored as a procedure source without starting an accounting preparation job or entering the agent's ordinary evidence inventory. Choose a supported work kind, method and either that current document or the client's prior revised workpapers. With OpenAI configured on the API, the model receives only the selected extracted document text (at most 16 KiB) or up to 30 approved field edits from this client (at most 16 KiB of serialized context), plus the chosen kind and method. It returns draft instructions and, for documents, an exact unique supporting quote. The API rejects incomplete, oversized, uncited or invalid output without saving it. With no model settings, the local review prompt highlights one exact document passage or edited fields. Either output is inactive and labeled by origin. PDF text must finish extraction before selection. An identical file already uploaded as ordinary work evidence keeps its original purpose; use a distinct source for policy drafting.

Rewrite the proposal as the accountant's actual eligibility, evidence, period and exclusion instructions. Typed learned rules currently cover prepaid and accrual methods, including declining-balance and mid-month depreciation (the convention is part of the method, as in `equal_full_months_v1+mid_month`) and pro-rata service accruals. Reconciliation account types continue to use reviewed guidance because a single method rule would incorrectly block the other account type. Select an already approved workpaper that uses the chosen method, inspect its source and result, and record the accountant's approval reference. The server replays that exact saved input through the current calculator and rejects any changed output, workpaper digest, review decision or source revision. An accountant can create and approve a synthetic workpaper first when no suitable example exists. Once an example is approved, start a new proposal if the rule text needs changes.

Choose **Preview affected clients** to inspect every scoped close (latest 100 shown, total counted), then record the accountant's approval and activate against the current preview digest. Activation creates an immutable procedure version. A version, published or activated, can take effect from a month (**Applies from**, migration `158_procedure_effective_from.sql`): a close of an earlier month keeps the version in effect for it, so a method changed mid-year does not rewrite the months before; the impact preview counts only the closes from that month. Without a month a version applies to every month, as before. A method rule is enforced on new manual and queued workpapers; a different method is blocked until the approved procedure context changes. Existing pinned closes keep their prior version and require the usual reviewed **Apply version** action. Legacy free-text guidance remains available where no active learned method rule applies. A learned method rule cannot be replaced by enabled guidance without a new proposal and example while it is the active version of its own scope (the client's, or the firm default): the firm's learned rule does not stop a client's own override, and a retired one does not stop guidance. Any earlier version can be restored from **Version history** (**Restore this version**): after the affected closes are previewed, it is published again as a new version with a reason, and pinned closes keep their copy until updated. Guidance and fixed-method versions (loan, payroll, basis, owner) restore this way; a learned method rule comes back only through procedure learning, which checks its approved examples again.

The replay proves the existing calculator output and selected method for the approved examples. It cannot validate the meaning of free-text instructions; the accountant must inspect the policy source, proposed boundaries and examples. Model output cannot grant approval or post to a ledger. Responses requests use strict structured output, `store: false`, and no tools; do not claim that this controls the provider's separate data-retention terms. The proposal records its model name, but never a credential.

An active rule can restrict other supported methods for the same work kind and client scope. Use a client override or a new reviewed rule before preparing a different method. Retention deletion blocks a close while it supplies an active rule's document, prior edit or approved example. An accountant can publish a disabled version to retire that rule; deletion then removes the example approvals and source links from retained draft history.

Conflicting invoice/reply/procedure values remain questions, not silently resolved preferences. Uploaded documents cannot publish procedures, grant permissions or authorize posting. Carry-forward preserves procedure source links and still requires fresh review. Exported originals contain the pinned procedure text/version/approval reference.

## Permissions

WorkOS organization membership is required in hosted mode. Grant `clients:all` only to intended firm staff. A client accounting professional should instead receive a client assignment, plus only needed action permissions:

- `workpapers:review`: review and preparation decisions.
- `procedures:manage`: record approved procedures; assigned users can change only their client overrides, never firm defaults.
- `close:signoff`: scoped sign-off/reopening, in addition to review authority.
- `members:manage` plus `clients:all`: administer assignments.

On the Client closes page, **Client access administration** records grants/revocations for exact WorkOS user IDs and existing client names. Obtain the ID from the verified membership in WorkOS. Assignment alone grants no action permission or organization membership. It covers every period of that client. Later requests enforce revocation; already downloaded data and valid firm-wide role tokens are not recalled. Remove `clients:all` in WorkOS when an account should become restricted.

The local token is a shared development identity. It can exercise these flows on synthetic data but cannot establish who a real accountant is. Invitations and WorkOS role changes remain operator-managed. Client identity currently uses the organization plus a case-insensitive client label; rename/merge workflows are not implemented.

## Setup and limits

Apply migrations through 036 in order, with API/worker stopped, then restart both on the same revision. Fresh Compose startup applies them automatically. Add the above exact permission names to hosted WorkOS roles before admitting users; a valid organization session without `clients:all` or an assignment has no client data access.

For model drafts, set both `OPENAI_API_KEY` and `OPENAI_MODEL` privately on the API and worker. Local Compose reads the root `.env`; staging passes the same server-only secret and model to the Cloudflare API container and queue worker. With only one setting, drafting fails visibly. With neither, local prompts remain available. Apply migration 036 before deploying the updated API. No new package is needed. The version-history screen supports 200 versions and fails explicitly beyond that; larger histories need a paginated view before broader rollout. New calculation methods and materiality thresholds remain separately reviewed accounting work.

API: `GET/POST /v1/closes/procedures`, `POST /v1/closes/{id}/procedures/apply`, `GET/POST /v1/closes/access`. Publishing/applying/access changes require explicit confirmation and current version references. Caller-supplied organization or actor metadata cannot change ownership.

## Accounting basis per client

Choose **Accounting basis** in **Approved procedures and client overrides** to record the basis the client's accountant approved: accrual, modified cash or cash. Set it once as the firm default and override it for individual clients. A basis version is always enabled; to change a client's basis, publish a new override.

- **Cash and modified cash clients:** new prepaid and accrued-expense drafts are refused, and so is a loan draft that accrues interest. Drafts saved before a basis change can't be approved, and prepaid schedules can't be posted to production. Reconciliations, Stripe settlements and loans without accrued interest are unaffected.
- **Accrual clients:** today's behavior, with per-draft accrual confirmations.
- **The close header shows the effective basis** and whether a client override sets it. Prepaid and accrual actions are hidden for cash and modified-cash clients.
- **Rollout exception:** a firm that hasn't set any basis keeps today's behavior (each prepaid or accrual draft confirms accrual basis) until it publishes a firm default.

## Owner treatments per client

Record each client's entity type under **Owner treatments** (a client override): sole proprietor or single-member LLC, partnership, S corporation, or C corporation. The firm default can hold the firm's owner policy and account naming, but never an entity type; that is a fact about one client. Owner reclassification drafts are refused until the client's entity type is recorded, and must use it.


## Automatic fixed incurred accruals

The agent can prepare a new `fixed_incurred_amount_v1` draft without field-by-field confirmation when the active learned client-specific accrual procedure, approved through the existing example-and-activation flow, names these unambiguous lines in its instructions:

```text
Expense account: Professional Fees
Liability account: Accrued Expenses
Reverse next month: true
QuickBooks company: 123
Vendor ID for Acme Services: 8
```

The company/vendor IDs above are illustrative. Record the client’s actual company ID and map each source issuer to its approved QuickBooks vendor ID; a firm-wide default cannot supply company-specific identities. The active connection, transaction capture and chart must agree with that company. Vendor IDs prevent a different ledger display name from hiding a booking. Confirmed client aliases remain valid billed-to names. Use the actual chart names and an explicit `true` or `false` reversal choice. This is approved policy, not permission an uploaded invoice can grant. The agent cites the account choices from the applied procedure copy. It needs one source identifying the issuer, document number, billed client, positive **unbilled** service amount and wholly incurred service dates in the close month and home currency. The current connected QuickBooks capture and accrual Trial Balance must explicitly establish a zero current-liability balance, unique accounts, no matching service booking and no liability activity. A missing row is not zero. An existing accrual draft on that liability also holds a new zero-balance adjustment, including one made by the recurring sweep. Nonzero balances, estimates, credits, amendments, extra related evidence, transactions whose posting accounts or counterparties cannot be resolved, and ambiguous facts remain specific decisions for a person. Fixed recurring accruals continue through the existing approved-rule sweep; this path does not infer a recurring rule from a first service.

The queued job rechecks its source inventory, current policy, books and duplicate-document lineage before saving. A source replacement can revise its own unposted agent-prepared draft through the existing lineage path; a posted or human-confirmed predecessor needs a person's correction. Sources, capture, chart and procedure remain attached to the draft and ordinary review remains required. This local capability is not accountant acceptance or live-provider validation.

## Automatic new assets

A newly capitalized purchase can advance from intake to a reviewable depreciation draft under the existing equal-month calculator. Activate a **client-specific learned prepaid procedure with a reviewed depreciation example**, then apply it to the close. A prepaid example alone does not enable assets. Keep the normal prepaid account fields for prepaids; asset mappings are explicit instruction lines:

```text
Asset cost account: Equipment
Depreciation expense account: Depreciation
Accumulated depreciation account: Accumulated Depreciation
Useful life months: 12
Salvage value: 0.00
Depreciation convention: full_first_month
QuickBooks company: 123
Vendor ID for Acme Equipment: 8
```

Use the client's actual accounts, life, salvage and provider identities. These illustrative values are not accounting advice or an activated firm policy. The approved full-first-month convention derives calendar allocation dates from the source-verified actual placed-in-service date; the draft preserves that actual date in its source reference. Delivery, invoice, planned or negated in-service dates do not qualify. Cost, issuer, invoice number, billed client and currency must be explicit on one undisputed purchase invoice. Other supporting records still need a person's judgment.

The current QuickBooks capture must show exactly one bill or purchase capitalizing the full invoice amount to the unique approved Fixed Asset cost account. Current company/vendor IDs, chart types and names must agree. The close-month accrual Trial Balance must explicitly show zero accumulated depreciation; a missing row or nonzero shared balance needs allocation by a person. Existing journal activity on the asset, accumulated-depreciation or depreciation-expense accounts, related credits/refunds and unknown posting details hold the draft. The worker repeats source, policy, example-approval, profile, alias and books checks before saving; repeated jobs do not prepare a completed draft again. All sources remain cited and normal review is required.

This bounded initial path does not enable declining balance, mid-month conventions, disposals, allocation of an existing accumulated balance, or missing in-service evidence. Those retain their existing reviewed preparation paths. Qualified accounting examples, actual-model mixed workloads and live provider acceptance remain separate gates.

### Answering an asset's missing in-service date

An authorized preparer can answer the existing job's question with the actual date, and the agent resumes that same job without re-entering its calculation fields. The date may be a bare ISO/US/written date or an explicit statement that the asset was placed in service. Its answer is retained beside the invoice as a cited source. Only stored ownership of that answer by this job qualifies it; model-selected source roles cannot grant that ownership. A deduplicated answer explicitly supplied to two jobs remains owned by both.

The answer can establish this date only. Invoice identity/cost, policy, capitalization and the zero booked-depreciation proof keep their existing guards. Multiple dates, uncertainty, monetary amounts, policy/account changes and conflicting invoice dates still need a person. Client-request replies and other supporting records remain outside this bounded automatic date path; they do not become policy authority.
