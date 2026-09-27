import { useEffect, useState, type FormEvent } from "react";
import { EmailHistory, PasscodeNotice, SendRequestEmail, type EmailRecord, type ReminderStatus } from "./Email";
import { workspaceRequest } from "./workspace";

type Requirement = { account_id: string; name: string; kind: string; question: string; binding_id: string; job_ids: string[]; suggested_job_ids: string[]; suggested_ledger_queries: string[]; excluded: boolean; reason: string; state: string };
type Status = {
  coverage: { snapshot_id: string; stale: boolean; digest: string; items: Requirement[]; jobs: { id: string; name: string; state: string }[] };
  requests: { email?: EmailRecord; emails: EmailRecord[]; reminder?: ReminderStatus; id: string; body: string; recipient: string; link?: { used_at?: string; revoked_at?: string; expires_at: string }; replied: boolean; live: boolean; open?: boolean; stale?: string }[];
  attachments: { id: string; request_id: string; name: string; state: string; error: string; scan_version: string; scanned_at?: string; source_id?: string; decision_note?: string }[];
};
export function Coverage({ closeId }: { closeId: string }) {
  const [status, setStatus] = useState<Status>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [selected, setSelected] = useState<string[]>([]);
  const [body, setBody] = useState("");
  const [link, setLink] = useState("");
  const [passcode, setPasscode] = useState("");
  const [version, setVersion] = useState(0);
  const [accountSearch, setAccountSearch] = useState("");
  const [accountPage, setAccountPage] = useState(0);
  useEffect(() => {
    let live = true; let timer: ReturnType<typeof setTimeout>;
    async function load() {
      try { const value: Status = await workspaceRequest(`/${closeId}/coverage`); if (live) setStatus(value); }
      catch (e) { if (live) setError(e instanceof Error ? e.message : "Coverage unavailable."); }
      finally { if (live) timer = setTimeout(() => void load(), 5000); }
    }
    void load(); return () => { live = false; clearTimeout(timer); };
  }, [closeId, version]);
  async function act(input: object, attachment?: string) {
    setBusy(true); setError("");
    try {
      const result = await workspaceRequest(attachment ? `/${closeId}/attachments/${attachment}` : `/${closeId}/coverage`, { digest: status?.coverage.digest, ...input });
      if (result.link_token) { setLink(`${window.location.origin}/request#${result.link_token}`); setPasscode(result.passcode ?? ""); }
      setVersion(v => v + 1);
    } catch (e) { setError(e instanceof Error ? e.message : "Decision could not be confirmed."); }
    finally { setBusy(false); }
  }
  async function download(id: string, name: string) {
    try { const blob = await workspaceRequest(`/${closeId}/attachments/${id}`, undefined, true); const url = URL.createObjectURL(blob); const a = document.createElement("a"); a.href = url; a.download = name; a.click(); URL.revokeObjectURL(url); }
    catch (e) { setError(e instanceof Error ? e.message : "Download unavailable."); }
  }
  function bind(event: FormEvent<HTMLFormElement>, account: string) {
    event.preventDefault(); const data = new FormData(event.currentTarget);
    void act({ action: "bind", account_id: account, jobs: data.getAll("jobs"), excluded: data.has("excluded"), reason: data.get("reason"), confirmed: data.has("confirmed") });
  }
  const coverage = status?.coverage;
  const matchingAccounts = coverage?.items.filter(item => `${item.name} ${item.account_id} ${item.kind} ${item.state}`.toLowerCase().includes(accountSearch.toLowerCase().trim())) ?? [];
  const page = Math.min(accountPage, Math.max(0, Math.ceil(matchingAccounts.length / 50) - 1));
  const visibleAccounts = matchingAccounts.slice(page * 50, (page + 1) * 50);
  return <section id="coverage-work" aria-labelledby="coverage-title">
    <h3 id="coverage-title">Coverage and client evidence</h3>
    {error && <p role="alert">{error}</p>}
    <button disabled={busy} onClick={() => { setError(""); setVersion(v => v + 1); }}>Refresh coverage</button>
    {!coverage ? <p>Loading coverage…</p> : !coverage.snapshot_id ? <p>Capture period reports in the QuickBooks panel above to define account coverage.</p> : <>
      <p>Ledger snapshot #{coverage.snapshot_id}. Confirm the complete set of required jobs for each account. Resolution then follows their current, approved drafts automatically. Uploads, matching names and client replies cannot approve accounting decisions.</p>
      {coverage.stale && <p role="alert">Ledger source or connection changed. Recapture before changing coverage.</p>}
      <p>{coverage.items.filter(i => i.state === "resolved").length} resolved · {coverage.items.filter(i => i.state === "excluded").length} explicitly excluded · {coverage.items.filter(i => !["resolved", "excluded"].includes(i.state)).length} unresolved</p>
      <label>Find an account or status<input type="search" value={accountSearch} onChange={e => { setAccountSearch(e.target.value); setAccountPage(0); }} placeholder="Name, ID, prepaid, missing…" /></label>
      <p>Showing {visibleAccounts.length ? `${page * 50 + 1}–${page * 50 + visibleAccounts.length}` : "none"} of {matchingAccounts.length} matching accounts.</p>
      {visibleAccounts.map(item => <details key={`${coverage.snapshot_id}-${item.account_id}-${item.binding_id}`}>
        <summary>{item.name || item.account_id} · {item.state.replaceAll("_", " ")}</summary>
        <p>{item.question}</p>
        <p>{item.reason}</p>
        {!!item.suggested_job_ids.length && <p>Agent suggestion: work #{item.suggested_job_ids.join(", #")} cites this exact account name or ID. Confirm against the source and ledger before binding; a match is not coverage approval.</p>}
        <form onSubmit={e => bind(e, item.account_id)}><fieldset disabled={busy || coverage.stale}>
          <legend>Required work for {item.name}</legend>
          {coverage.jobs.map(job => <label key={job.id}><input type="checkbox" name="jobs" value={job.id} defaultChecked={item.binding_id ? item.job_ids.includes(job.id) : item.suggested_job_ids.includes(job.id)} /> {job.name} · #{job.id} · {job.state.replaceAll("_", " ")}</label>)}
          {coverage.jobs.length === 0 && <p>No intake jobs yet. Request the missing documents below.</p>}
          <label><input type="checkbox" name="excluded" defaultChecked={item.excluded} /> Exclude from supported preparation scope; deselect all jobs and document the reason or external-work reference.</label>
          <label>Scope rationale<textarea name="reason" defaultValue={item.reason} maxLength={2000} required /></label>
          <label><input type="checkbox" name="confirmed" required /> I reviewed the account and ledger evidence. This is the complete required work set, or a justified exclusion.</label>
          <button>Confirm coverage scope</button>
        </fieldset></form>
        <LedgerSelection key={`${coverage.snapshot_id}:${item.account_id}`} closeId={closeId} snapshotId={coverage.snapshot_id} accountId={item.account_id} stale={coverage.stale} suggestions={item.suggested_ledger_queries} />
      </details>)}
      {page > 0 && <button type="button" onClick={() => setAccountPage(page - 1)}>Previous accounts</button>}
      {(page + 1) * 50 < matchingAccounts.length && <button type="button" onClick={() => setAccountPage(page + 1)}>Next accounts</button>}
      <details><summary>Consolidated missing-evidence request</summary>
        <form onSubmit={e => { e.preventDefault(); const data = new FormData(e.currentTarget); void act({ action: "request", accounts: selected, jobs: [], body, recipient: data.get("recipient"), confirmed: data.has("confirmed") }); }}>
          <fieldset disabled={busy || coverage.stale}>
            <legend>Unresolved requirements · choose up to 20</legend>
            {coverage.items.filter(i => !["resolved", "excluded"].includes(i.state)).map(item => <label key={item.account_id}><input type="checkbox" checked={selected.includes(item.account_id)} onChange={e => setSelected(ids => e.target.checked ? [...ids, item.account_id] : ids.filter(id => id !== item.account_id))} /> {item.name}</label>)}
            <button type="button" onClick={() => setBody("Please help us complete this month's preparation:\n\n" + coverage.items.filter(i => selected.includes(i.account_id)).map((i, index) => `${index + 1}. ${i.name}: ${i.question}`).join("\n\n"))}>Draft selected questions</button>
            <label>Exact request<textarea value={body} onChange={e => setBody(e.target.value)} rows={8} maxLength={20000} required /></label>
            <label>Client recipient<input name="recipient" type="email" required /></label>
            <label><input type="checkbox" name="confirmed" required /> I reviewed the wording, recipient and selected requirements. The reply may be shared with every job bound to these requirements.</label>
            <button disabled={selected.length === 0}>Approve request and create private link</button>
          </fieldset>
        </form>
        <p>Share the link manually or approve email delivery below. Creating a request does not send an email. Anyone with the link can reply unless the firm requires a passcode, which you give the client another way.</p>
      </details>
    </>}
    {link && <label>Copy this private reply link now<input readOnly value={link} onFocus={e => e.currentTarget.select()} /></label>}
    <PasscodeNotice passcode={passcode} />
    {!!status?.requests.length && <details><summary>Approved consolidated requests ({status.requests.length})</summary>{status.requests.map(q => <article key={q.id}>
      <h4>Request #{q.id} · {q.recipient}</h4><pre style={{ whiteSpace: "pre-wrap" }}>{q.body}</pre>
      <p>{q.link?.revoked_at ? "Link revoked" : q.link && !q.live ? `${q.replied ? "Reply received; the link expired" : "Link expired"} ${new Date(q.link.expires_at).toLocaleString()}` : q.replied ? (q.open ? "Reply received; the client can add to it until the link expires" : "Reply received. It takes no more answers now that its work or coverage moved on; send a new request for anything still missing.") : q.stale ? `The client can no longer answer it: ${q.stale}` : q.link ? `Expires ${new Date(q.link.expires_at).toLocaleString()}` : "No current link"}</p>
      {!!q.emails?.length && <EmailHistory emails={q.emails} />}
      {!q.replied && q.live && !q.stale && (!q.email || (q.email.state==="accepted" && q.email.delivery==="delivered") || (q.reminder?.left ?? 1) <= 0) && <SendRequestEmail key={q.email?.id ?? "initial"} closeId={closeId} requestId={q.id} recipient={q.recipient} previousId={q.email?.id} reminder={q.reminder} onQueued={(p)=>{if(!q.email){setLink("");setPasscode(p);}setVersion(v=>v+1);}} />}
      {(!q.link?.used_at || !q.link.revoked_at) && <>{(q.replied ? q.open !== false : !q.stale) && <button disabled={busy} onClick={() => void act({ action: "link", request_id: q.id, confirmed: true })}>Replace private link</button>}<button disabled={busy} onClick={() => void act({ action: "revoke", request_id: q.id, confirmed: true })}>Withdraw request</button></>}
    </article>)}</details>}
    <details open={!!status?.attachments.length}><summary>Client attachments ({status?.attachments.length ?? 0})</summary>
      <p>Malware scanning is mandatory: Cloudflare for PDFs, a separate on-demand antivirus service for TXT/CSV. Clean results permit inspection; firm release is still required. Quarantined files never enter OCR, model inputs or workpapers.</p>
      <p>For a missing or expired scan, ask the client to reopen their unexpired link and retry scanning the same original file. Local uploads cannot be released without a trusted scan.</p>
      {status?.attachments.map(a => <article key={a.id}>
        <h4>{a.name} · {a.state.replaceAll("_", " ")}</h4><p>Request #{a.request_id}{a.source_id ? ` · evidence #${a.source_id}` : ""}</p>
        {a.error && <p role="alert">{a.error}</p>}<p>{a.scan_version}{a.scanned_at ? ` · ${new Date(a.scanned_at).toLocaleString()}` : ""}</p>
        {["clean", "released"].includes(a.state) && <button onClick={() => void download(a.id, a.name)}>Download scanned file</button>}
        {!["released", "rejected"].includes(a.state) && <form onSubmit={e => {e.preventDefault(); const data = new FormData(e.currentTarget); void act({ action: data.get("action"), reason: data.get("reason"), confirmed: data.has("confirmed") }, a.id);}}><fieldset disabled={busy}>
          <label>Decision<select name="action"><option value="reject">Reject and retain in quarantine</option>{a.state === "clean" && <option value="release">Release to preparation and resume affected jobs</option>}</select></label>
          <label>Reason<textarea name="reason" maxLength={2000} required /></label><label><input name="confirmed" type="checkbox" required /> I reviewed the file status, client scope and this decision.</label><button>Record attachment decision</button>
        </fieldset></form>}
      </article>)}
    </details>
  </section>;
}

type LedgerPage = { account_id: string; query?: string; offset: number; total: number; columns: string[]; rows: { location: string; row: { ColData?: { value?: string }[]; Header?: { ColData?: { value?: string }[] } } }[] };
export function LedgerSelection({ closeId, snapshotId, accountId, stale, suggestions }: { closeId: string; snapshotId: string; accountId: string; stale: boolean; suggestions: string[] }) {
  const [page, setPage] = useState<LedgerPage>();
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState("");
  const path = `/${closeId}/quickbooks/snapshots/${snapshotId}/ledger?account_id=${encodeURIComponent(accountId)}&offset=${page?.offset ?? 0}&query=${encodeURIComponent(page?.query ?? "")}`;
  async function inspect(next = 0, search = query) {
    setBusy(true); setError(""); setSaved("");
    try {
      setPage(await workspaceRequest(`/${closeId}/quickbooks/snapshots/${snapshotId}/ledger?account_id=${encodeURIComponent(accountId)}&offset=${next}&query=${encodeURIComponent(search)}`));
    } catch (e) { setError(e instanceof Error ? e.message : "Ledger rows unavailable."); }
    finally { setBusy(false); }
  }
  async function add() {
    setBusy(true); setError("");
    try {
      const result = await workspaceRequest(path, { confirmed: true });
      setSaved(`Evidence #${result.source_id} is available to the agent. Recheck the affected work proposal before confirming facts.`);
    } catch (e) { setError(e instanceof Error ? e.message : "Could not add ledger evidence."); }
    finally { setBusy(false); }
  }
  return <div>
    {!!suggestions.length && <p>Agent-found source references: {suggestions.map(term => <button type="button" key={term} disabled={busy || stale} onClick={() => { setQuery(term); void inspect(0, term); }}>{term}</button>)}. Search results are candidates, not proof of a complete ledger.</p>}
    <label>Find invoice, memo or transaction reference in this account's captured ledger<input type="search" value={query} maxLength={100} onChange={e => setQuery(e.target.value)} placeholder="Optional exact text; leave blank for first rows" /></label>
    <button type="button" disabled={busy || stale} onClick={() => void inspect(0)}>Find account-matched ledger rows</button>
    {error && <p role="alert">{error}</p>}{saved && <p role="status">{saved}</p>}
    {page && <><p>{page.total} account-matched report row{page.total === 1 ? "" : "s"}. Showing {page.rows.length ? `${page.offset + 1}–${page.offset + page.rows.length}` : "none"}. Report rows are context, not a verified balance or complete transaction export.</p>
      {page.rows.length === 0 ? <p>No exact account ID match in this report. Inspect the original snapshot before deciding coverage.</p> : <ul>{page.rows.map(row => <li key={row.location}>{row.location}: {(row.row.ColData ?? row.row.Header?.ColData ?? []).map((cell, index) => `${page.columns[index] || `Column ${index + 1}`}: ${cell.value ?? ""}`).join(" · ")}</li>)}</ul>}
      {page.rows.length > 0 && <button type="button" disabled={busy || stale} onClick={() => void add()}>Add this page to agent evidence</button>}
      {page.offset + page.rows.length < page.total && <button type="button" disabled={busy} onClick={() => void inspect(page.offset + page.rows.length, page.query ?? "")}>Next ledger page</button>}
      {page.offset > 0 && <button type="button" disabled={busy} onClick={() => void inspect(Math.max(0, page.offset - 20), page.query ?? "")}>Previous ledger page</button>}
    </>}
  </div>;
}
