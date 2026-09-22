import { useEffect, useState, type FormEvent } from "react";
import { EmailHistory, SendRequestEmail, type EmailRecord } from "./Email";
import { workspaceRequest } from "./workspace";

type Requirement = { account_id: string; name: string; kind: string; question: string; binding_id: string; job_ids: string[]; excluded: boolean; reason: string; state: string };
type Status = {
  coverage: { snapshot_id: string; stale: boolean; digest: string; items: Requirement[]; jobs: { id: string; name: string; state: string }[] };
  requests: { email?: EmailRecord; emails: EmailRecord[]; id: string; body: string; recipient: string; link?: { used_at?: string; revoked_at?: string; expires_at: string } }[];
  attachments: { id: string; request_id: string; name: string; state: string; error: string; scan_version: string; scanned_at?: string; source_id?: string; decision_note?: string }[];
};
export function Coverage({ closeId }: { closeId: string }) {
  const [status, setStatus] = useState<Status>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [selected, setSelected] = useState<string[]>([]);
  const [body, setBody] = useState("");
  const [link, setLink] = useState("");
  const [version, setVersion] = useState(0);
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
      if (result.link_token) setLink(`${window.location.origin}/request#${result.link_token}`);
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
  return <section id="coverage-work" aria-labelledby="coverage-title">
    <h3 id="coverage-title">Coverage and client evidence</h3>
    {error && <p role="alert">{error}</p>}
    <button disabled={busy} onClick={() => { setError(""); setVersion(v => v + 1); }}>Refresh coverage</button>
    {!coverage ? <p>Loading coverage…</p> : !coverage.snapshot_id ? <p>Capture period reports in the QuickBooks panel above to define account coverage.</p> : <>
      <p>Ledger snapshot #{coverage.snapshot_id}. Confirm the complete set of required jobs for each account. Resolution then follows their current, approved drafts automatically. Uploads, matching names and client replies cannot approve accounting decisions.</p>
      {coverage.stale && <p role="alert">Ledger source or connection changed. Recapture before changing coverage.</p>}
      <p>{coverage.items.filter(i => i.state === "resolved").length} resolved · {coverage.items.filter(i => i.state === "excluded").length} explicitly excluded · {coverage.items.filter(i => !["resolved", "excluded"].includes(i.state)).length} unresolved</p>
      {coverage.items.map(item => <details key={`${item.account_id}-${item.binding_id}`}>
        <summary>{item.name || item.account_id} · {item.state.replaceAll("_", " ")}</summary>
        <p>{item.question}</p>
        <p>{item.reason}</p>
        <form onSubmit={e => bind(e, item.account_id)}><fieldset disabled={busy || coverage.stale}>
          <legend>Required work for {item.name}</legend>
          {coverage.jobs.map(job => <label key={job.id}><input type="checkbox" name="jobs" value={job.id} defaultChecked={item.job_ids.includes(job.id)} /> {job.name} · #{job.id} · {job.state.replaceAll("_", " ")}</label>)}
          {coverage.jobs.length === 0 && <p>No intake jobs yet. Request the missing documents below.</p>}
          <label><input type="checkbox" name="excluded" defaultChecked={item.excluded} /> Exclude from supported preparation scope; deselect all jobs and document the reason or external-work reference.</label>
          <label>Scope rationale<textarea name="reason" defaultValue={item.reason} maxLength={2000} required /></label>
          <label><input type="checkbox" name="confirmed" required /> I reviewed the account and ledger evidence. This is the complete required work set, or a justified exclusion.</label>
          <button>Confirm coverage scope</button>
        </fieldset></form>
      </details>)}
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
        <p>Share the link manually or approve email delivery below. Creating a request does not send an email. Anyone with the link can reply; their identity is not verified.</p>
      </details>
    </>}
    {link && <label>Copy this private reply link now<input readOnly value={link} onFocus={e => e.currentTarget.select()} /></label>}
    {!!status?.requests.length && <details><summary>Approved consolidated requests ({status.requests.length})</summary>{status.requests.map(q => <article key={q.id}>
      <h4>Request #{q.id} · {q.recipient}</h4><pre style={{ whiteSpace: "pre-wrap" }}>{q.body}</pre>
      <p>{q.link?.used_at ? "Reply received" : q.link?.revoked_at ? "Link revoked" : q.link ? `Expires ${new Date(q.link.expires_at).toLocaleString()}` : "No current link"}</p>
      {!!q.emails?.length && <EmailHistory emails={q.emails} />}
      {!q.link?.used_at && !q.link?.revoked_at && (!q.email || (q.email.state==="accepted" && q.email.delivery==="delivered")) && <SendRequestEmail key={q.email?.id ?? "initial"} closeId={closeId} requestId={q.id} recipient={q.recipient} previousId={q.email?.id} onQueued={()=>{setLink("");setVersion(v=>v+1);}} />}
      {!q.link?.used_at && <><button disabled={busy} onClick={() => void act({ action: "link", request_id: q.id, confirmed: true })}>Replace private link</button><button disabled={busy} onClick={() => void act({ action: "revoke", request_id: q.id, confirmed: true })}>Withdraw request</button></>}
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
