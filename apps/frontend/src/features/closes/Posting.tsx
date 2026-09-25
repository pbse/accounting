import { useState } from "react";
import { workspaceRequest } from "./workspace";

type ProposedState = { items: { workpaper_id: string; state: string; review_state: string; digest: string; ledger_before: { prepaid: { name: string; balance: string; sync_token: string }; expense: { name: string; balance: string; sync_token: string } }; projected_balances: { prepaid: string; expense: string }; other_open_intents: string[] }[]; has_more: boolean; notice: string; rehearsal: { company: string; intent_count: number; accounts: { id: string; name: string; balance: string; sync_token: string; delta: string; projected: string }[]; blockers: string[]; review_required: string[] } };
export function ProposedStatePreview({ closeId }: { closeId: string }) {
  const [preview, setPreview] = useState<ProposedState>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function load() {
    setBusy(true);
    setError("");
    setPreview(undefined);
    try {
      setPreview(await workspaceRequest(`/${encodeURIComponent(closeId)}/proposed-state`));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Proposed state unavailable.");
    } finally {
      setBusy(false);
    }
  }
  return <section className="panel" id="close-rehearsal" aria-labelledby="proposed-state-heading">
    <h2 id="proposed-state-heading">Close ledger rehearsal</h2>
    <p>Combine saved production journal proposals against one set of account balances and versions. Refresh before any approval; QuickBooks may have changed since capture.</p>
    <button disabled={busy} onClick={() => void load()}>{busy ? "Loading…" : "Rehearse saved proposals"}</button>
    {error && <p role="alert">{error}</p>}
    {preview && <><p>{preview.notice}</p>{preview.items.length === 0 && <p>No unexecuted production journal proposal is saved for this close.</p>}
      {preview.rehearsal.blockers.length > 0 && <div role="alert"><p>The combined state cannot be calculated safely:</p><ul>{preview.rehearsal.blockers.map((blocker,i)=><li key={i}>{blocker}</li>)}</ul></div>}
      {preview.rehearsal.accounts.length > 0 && <div><h3>Combined proposed state · {preview.rehearsal.company}</h3><p>{preview.rehearsal.intent_count} saved journal proposal{preview.rehearsal.intent_count === 1 ? "" : "s"} included. These are versioned observations, not a live trial balance.</p><ul>{preview.rehearsal.accounts.map(account=><li key={account.id}>{account.name} (#{account.id}): ${account.balance} {account.delta.startsWith("-") ? "−" : "+"} ${account.delta.replace("-", "")} = ${account.projected} · version {account.sync_token}</li>)}</ul></div>}
      {preview.rehearsal.review_required.length > 0 && <p role="status">Accountant re-review is required for draft{preview.rehearsal.review_required.length === 1 ? "" : "s"} #{preview.rehearsal.review_required.join(", #")}. The rehearsal grants no approval.</p>}
      {preview.items.map(item => <section key={item.workpaper_id}><h3>Draft #{item.workpaper_id} · {item.state} · review {item.review_state}</h3>
        <ul>{(["prepaid", "expense"] as const).map(account => <li key={account}>{item.ledger_before[account].name}: ${item.ledger_before[account].balance} → proposed ${item.projected_balances[account]} · account version {item.ledger_before[account].sync_token}</li>)}</ul>
        {item.other_open_intents.length > 0 && <p role="alert">Other open intents use these accounts: {item.other_open_intents.join(", ")}. Resolve before approval.</p>}
        {item.review_state !== "approved" && <p role="status">This workpaper needs current accountant review.</p>}
      </section>)}
      {preview.has_more && <p>Showing 100 recent proposals; inspect older intents individually.</p>}
    </>}
  </section>;
}

type Intent = {
  state: string;
  request_id?: string;
  digest?: string;
  realm?: string;
  journal?: unknown;
  external_id?: string;
  error?: string;
  approval_expires_at?: string;
  configured?: boolean;
};
export function Posting({
  closeId,
  paperId,
}: {
  closeId: string;
  paperId: string;
}) {
  const [intent, setIntent] = useState<Intent>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const path = `/${encodeURIComponent(closeId)}/workpapers/${encodeURIComponent(paperId)}/posting`;
  async function run(action?: string) {
    setBusy(true);
    setError("");
    try {
      setIntent(
        await workspaceRequest(
          path,
          action
            ? { action, digest: intent?.digest ?? "", confirmation }
            : undefined,
        ),
      );
      setConfirmation("");
    } catch (e) {
      setIntent(undefined);
      setError(
        e instanceof Error
          ? e.message
          : "Outcome could not be confirmed. Refresh status before any further action.",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <section>
      <h3>QuickBooks sandbox posting</h3>
      <p>
        Test company only. Review the exact company, accounts, date and amount
        before approval. Approval does not post an entry, and approving the
        posting of a pending draft approves the draft too. Sources remain
        manually confirmed; this is not production posting.
      </p>
      <button disabled={busy} onClick={() => void run()}>
        Check posting status
      </button>
      {error && <p role="alert">{error}</p>}
      {intent && (
        <>
          <p role="status">
            State:{" "}
            {intent.state === "executing"
              ? "Attempt recorded; outcome must be investigated before any further write"
              : intent.state}
          </p>
          {intent.configured === false && (
            <p>
              The sandbox connection and account mapping are not configured.
            </p>
          )}
          {intent.error && <p>{intent.error}</p>}
          {intent.realm && <p>Sandbox company ID: {intent.realm}</p>}
          {intent.external_id && <p>Journal entry ID: {intent.external_id}</p>}
          {intent.journal !== undefined && (
            <details open>
              <summary>Exact journal payload for review</summary>
              <pre>{JSON.stringify(intent.journal, null, 2)}</pre>
            </details>
          )}
          {intent.state === "not_prepared" && (
            <button
              disabled={busy || intent.configured === false}
              onClick={() => void run("preview")}
            >
              Prepare sandbox posting preview
            </button>
          )}
          {(intent.state === "preview" || intent.state === "approved") && (
            <>
              <p>
                Approval expires after 10 minutes. An attempt can execute only
                once; uncertain outcomes stay unresolved.
              </p>
              <label>
                Type{" "}
                {intent.state === "preview"
                  ? "APPROVE SANDBOX DRAFT"
                  : "POST TO SANDBOX"}
                <input
                  value={confirmation}
                  onChange={(event) => setConfirmation(event.target.value)}
                  autoComplete="off"
                />
              </label>
              <button
                disabled={
                  busy ||
                  confirmation !==
                    (intent.state === "preview"
                      ? "APPROVE SANDBOX DRAFT"
                      : "POST TO SANDBOX")
                }
                onClick={() =>
                  void run(intent.state === "preview" ? "approve" : "execute")
                }
              >
                {intent.state === "preview"
                  ? "Approve this exact sandbox proposal"
                  : "Post approved entry to sandbox"}
              </button>
              {intent.state === "approved" && (
                <>
                  <p>
                    Approval expires:{" "}
                    {intent.approval_expires_at
                      ? new Date(intent.approval_expires_at).toLocaleString()
                      : "Unknown"}
                  </p>
                  <button
                    disabled={busy}
                    onClick={() => {
                      setIntent({ ...intent, state: "preview" });
                      setConfirmation("");
                    }}
                  >
                    Review and renew approval
                  </button>
                </>
              )}
            </>
          )}
          {intent.external_id && (
            <button disabled={busy} onClick={() => void run("verify")}>
              Verify saved entry in QuickBooks
            </button>
          )}
        </>
      )}
      {busy && <p role="status">Checking sandbox posting…</p>}
    </section>
  );
}

type ProductionIntent = Intent & {
  id?: string;
  grant_id?: string;
  ledger_before?: { company: string; accounts: { id: string; name: string; classification: string; balance: string }[] };
  projected_balances?: Record<string, string>;
  other_open_intents?: string[];
  ledger_after?: unknown;
  verified_at?: string;
};
export function ProductionPosting({ closeId, paperId }: { closeId: string; paperId: string }) {
  const [intent, setIntent] = useState<ProductionIntent>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [phrase, setPhrase] = useState("");
  const [journalID, setJournalID] = useState("");
  const path = `/${encodeURIComponent(closeId)}/workpapers/${encodeURIComponent(paperId)}/production-posting`;
  async function run(action?: string) {
    setBusy(true);
    setError("");
    try {
      setIntent(await workspaceRequest(path, action ? {
        action, digest: intent?.digest ?? "", confirmation: phrase, external_id: journalID,
      } : undefined));
      setPhrase("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "Outcome unknown. Refresh status and inspect the saved attempt.");
      await workspaceRequest(path).then(setIntent).catch(() => setIntent(undefined));
    } finally {
      setBusy(false);
    }
  }
  const phraseRequired = intent?.state === "preview" ? "APPROVE PRODUCTION DRAFT" : "POST TO PRODUCTION";
  return <details>
    <summary>Reviewed production posting</summary>
    <p>Requires a client-specific grant, exact company and accounts, unchanged live balances, and a person's posting approval. Approving the posting of a pending draft approves the draft too, if you can review drafts; one approval covers both. No agent posts automatically.</p>
    <button disabled={busy} onClick={() => void run()}>Check production posting status</button>
    {error && <p role="alert">{error}</p>}
    {intent && <>
      <p role="status">State: {intent.state === "executing" ? "Attempt recorded; investigate before any further write" : intent.state}</p>
      {intent.error && <p role="alert">{intent.error}</p>}
      {intent.id && <p>Saved intent #{intent.id} · grant #{intent.grant_id} · company realm {intent.realm}</p>}
      {intent.request_id && <p>Provider request ID: <code>{intent.request_id}</code></p>}
      {intent.external_id && <p>QuickBooks journal #{intent.external_id}</p>}
      {intent.verified_at && <p>Canonical readback saved {new Date(intent.verified_at).toLocaleString()}</p>}
      {intent.journal && <details open><summary>Exact journal to review</summary><pre>{JSON.stringify(intent.journal, null, 2)}</pre></details>}
      {intent.ledger_before?.accounts && <table>
        <thead><tr><th>Account</th><th>Type</th><th>Balance now</th><th>After this journal</th></tr></thead>
        <tbody>{intent.ledger_before.accounts.map(account => <tr key={account.id}><td>{account.name} · #{account.id}</td><td>{account.classification}</td><td>${account.balance}</td><td>${intent.projected_balances?.[account.id] ?? "—"}</td></tr>)}</tbody>
      </table>}
      {intent.projected_balances && <p>A preview from versioned balances, not an official financial statement. Every balance is rechecked before approval and posting.</p>}
      {(intent.other_open_intents?.length ?? 0) > 0 && <p role="alert">Other open proposals use these accounts: #{intent.other_open_intents?.join(", #")}. Resolve them before approving or posting.</p>}
      {intent.ledger_after && <details><summary>Saved canonical readback and balances</summary><pre>{JSON.stringify(intent.ledger_after, null, 2)}</pre></details>}
      {intent.state === "not_prepared" && <button disabled={busy} onClick={() => void run("preview")}>Prepare production preview</button>}
      {(intent.state === "preview" || intent.state === "approved") && <>
        <p>Review the exact account balances and journal. Approval expires after 10 minutes. A reserved execution attempt cannot be repeated, even if the provider outcome is unknown.</p>
        <button disabled={busy} onClick={() => void run("preview")}>Refresh preview from live ledger and clear approval</button>
        <label>Type {phraseRequired}<input value={phrase} onChange={e => setPhrase(e.target.value)} autoComplete="off" /></label>
        <button disabled={busy || phrase !== phraseRequired || (intent.other_open_intents?.length ?? 0) > 0} onClick={() => void run(intent.state === "preview" ? "approve" : "execute")}>
          {intent.state === "preview" ? "Approve exact production proposal" : "Post once to production QuickBooks"}
        </button>
      </>}
      {intent.external_id && <button disabled={busy} onClick={() => void run("verify")}>{intent.state === "verified" ? "Recheck journal identity" : "Recheck saved journal and balances"}</button>}
      {intent.state === "unknown" && !intent.external_id && <>
        <p>Investigate the provider request ID in QuickBooks. If you find the exact matching journal, enter its numeric ID for readback. This UI will not issue another write.</p>
        <label>QuickBooks journal ID<input value={journalID} onChange={e => setJournalID(e.target.value)} inputMode="numeric" /></label>
        <button disabled={busy || !/^\d{1,30}$/.test(journalID)} onClick={() => void run("verify")}>Verify found journal against saved intent</button>
      </>}
      {intent.state === "executing" && !intent.external_id && <p>Investigate the saved provider request ID. This UI will not issue another write.</p>}
    </>}
    {busy && <p role="status">Checking production QuickBooks…</p>}
  </details>;
}
