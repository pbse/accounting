import { useState } from "react";
import { Link, useSearchParams } from "react-router";
import { download, when } from "../api";
import { Reveal, shown, useRevealable } from "../components/Reveal";

type Entry = { id: number; organization: string | null; staff_user: string; action: string; target_type: string; target_id: string; reason: string; request: Record<string, unknown>; result: string; error: string; created_at: string };

// The request's details, text masked until revealed.
export const details = (request: Record<string, unknown>) =>
  Object.keys(request ?? {}).length === 0 ? "—" : <small>{Object.entries(request).map(([k, v]) => `${k}: ${typeof v === "string" ? v : JSON.stringify(v)}`).join(", ")}</small>;
const filters = ["firm", "staff", "action", "result"];

// Every staff action and reveal, across firms, newest first, searchable and exported as CSV.
export function Audit() {
  const [params, setParams] = useSearchParams();
  // The page cursor belongs to its search: another search (by form or by history) starts at the newest.
  const [page, setPage] = useState<{ search: string; before: number }>();
  const [exported, setExported] = useState("");
  // Where the next export batch starts, for the search it came from.
  const [nextBatch, setNextBatch] = useState<{ search: string; before: string }>();
  const query = new URLSearchParams();
  for (const name of filters) if (params.get(name)) query.set(name, params.get(name)!);
  const search = query.toString();
  const before = page?.search === search ? page.before : 0;
  const setBefore = (value: number) => setPage({ search, before: value });
  if (before) query.set("before", String(before));
  // A reveal covers this search: paging keeps it, other filters are masked until revealed anew.
  const audit = useRevealable<{ entries: Entry[]; next_before: number | null; revealed: boolean }>(`/v1/admin/audit${query.size ? `?${query}` : ""}`, search);
  const reason = audit.reveal.shown ? audit.reveal.reason : undefined;
  // Exports this search from the newest entry, or from before, as CSV.
  const exportBatch = async (before?: string) => {
    setExported("");
    try {
      const exportQuery = new URLSearchParams(search);
      if (before) exportQuery.set("before", before);
      // A revealed export is its own audited read; an empty reason stands on the staff member's grant.
      if (reason !== undefined) exportQuery.set("reveal", "1");
      const headers = await download(`/v1/admin/audit/export${exportQuery.size ? `?${exportQuery}` : ""}`, "staff-audit.csv", reason !== undefined ? { "X-Access-Reason": reason } : {});
      const next = headers.get("X-Next-Before");
      setNextBatch(next ? { search, before: next } : undefined);
      setExported(next ? `Exported the newest ${Number(headers.get("X-Entries")).toLocaleString()} entries; export the next batch for older ones.` : "Exported.");
    } catch (e) {
      setExported((e as Error).message);
    }
  };
  return (
    <>
      <div className="page-title">
        <div><div><h1>Staff audit</h1><p>Every staff action and every reveal of client content, refusals included. Reasons, details and errors are hidden until revealed.</p></div></div>
        <div className="title-actions">
          <button onClick={() => exportBatch()}>Export CSV{reason !== undefined ? " (revealed)" : ""}</button>
          {nextBatch?.search === search && <button onClick={() => exportBatch(nextBatch.before)}>Export the next batch{reason !== undefined ? " (revealed)" : ""}</button>}
        </div>
      </div>
      <form className="filters" onSubmit={(event) => {
        event.preventDefault();
        setBefore(0);
        const next = new URLSearchParams();
        for (const [k, v] of new FormData(event.currentTarget)) if (String(v).trim()) next.set(k, String(v).trim());
        setParams(next);
      }}>
        <label>Firm ID<input name="firm" key={`f${params.get("firm")}`} defaultValue={params.get("firm") ?? ""} placeholder="Any, or platform" /></label>
        <label>Staff<input name="staff" key={`s${params.get("staff")}`} defaultValue={params.get("staff") ?? ""} placeholder="Any" /></label>
        <label>Action<input name="action" key={`a${params.get("action")}`} defaultValue={params.get("action") ?? ""} placeholder="plan. or content.reveal" /></label>
        <label>Result
          <select name="result" key={`r${params.get("result")}`} defaultValue={params.get("result") ?? ""}>
            <option value="">Any</option>
            <option value="ok">ok</option>
            <option value="refused">refused</option>
            <option value="started">started (outcome follows)</option>
          </select>
        </label>
        <button>Search</button>
      </form>
      {exported && <p className="note" role="status">{exported}</p>}
      <Reveal reveal={audit.reveal} firm={params.get("firm") && params.get("firm") !== "platform" ? params.get("firm") : undefined} />
      {audit.error && <p role="alert">{audit.error}</p>}
      {!audit.data && !audit.error && <p role="status">Loading the audit…</p>}
      {audit.data && (
        <section>
          {audit.data.entries.length === 0 ? <p>No entry matches.</p> : (
            <table>
              <thead><tr><th>When</th><th>Firm</th><th>Staff</th><th>Action</th><th>Target</th><th>Details</th><th>Reason</th><th>Result</th></tr></thead>
              <tbody>
                {audit.data.entries.map((e) => (
                  <tr key={e.id}>
                    <td>{when(e.created_at)}</td>
                    <td>{e.organization ? <Link to={`/firms/${encodeURIComponent(e.organization)}`}>{e.organization}</Link> : "platform"}</td>
                    <td>{e.staff_user}</td>
                    <td>{e.action}</td>
                    <td>{e.target_type} {e.target_id}</td>
                    <td>{details(e.request)}</td>
                    <td>{shown(e.reason)}</td>
                    <td><span className={`status ${e.result === "ok" ? "ok" : e.result === "started" ? "info" : "danger"}`}>{e.result}</span>{e.error && <small> {shown(e.error)}</small>}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          {(before > 0 || audit.data.next_before) && (
            <p className="title-actions">
              {before > 0 && <button onClick={() => setBefore(0)}>Newest</button>}
              {audit.data.next_before && <button onClick={() => setBefore(audit.data!.next_before!)}>Older</button>}
            </p>
          )}
        </section>
      )}
    </>
  );
}
