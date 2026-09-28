import { useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useResource, when } from "../api";

export type FirmRow = {
  id: string;
  name: string;
  enabled: boolean;
  created_at: string;
  members: number;
  clients: number;
  closes: number;
  active_runs: number;
  failed_runs_7d: number;
  last_activity: string | null;
};

export function Firms() {
  const [params, setParams] = useSearchParams();
  const query = params.get("q") ?? "";
  const [draft, setDraft] = useState(query);
  const firms = useResource<{ firms: FirmRow[] }>(`/v1/admin/firms${query ? `?q=${encodeURIComponent(query)}` : ""}`);
  return (
    <>
      <div className="page-title">
        <div>
          <div>
            <h1>Firms</h1>
            <p>Every firm, newest first.</p>
          </div>
        </div>
        <form
          role="search"
          className="title-actions"
          onSubmit={(event) => {
            event.preventDefault();
            setParams(draft.trim() ? { q: draft.trim() } : {});
          }}
        >
          <input aria-label="Search firms by name or ID" placeholder="Name or ID" value={draft} maxLength={200} onChange={(event) => setDraft(event.target.value)} />
          <button>Search</button>
        </form>
      </div>
      {firms.error && <p role="alert">{firms.error}</p>}
      {firms.data && (
        <section>
          {firms.data.firms.length === 0 ? (
            <p>No firm matches.</p>
          ) : (
            <table>
              <thead>
                <tr><th>Firm</th><th>Status</th><th>Members</th><th>Clients</th><th>Closes</th><th>Active runs</th><th>Failed runs (7 days)</th><th>Last activity</th></tr>
              </thead>
              <tbody>
                {firms.data.firms.map((f) => (
                  <tr key={f.id}>
                    <td><Link to={`/firms/${encodeURIComponent(f.id)}`}>{f.name}</Link><br /><small>{f.id}</small></td>
                    <td><span className={`status ${f.enabled ? "ok" : "danger"}`}>{f.enabled ? "Active" : "Suspended"}</span></td>
                    <td>{f.members}</td>
                    <td>{f.clients}</td>
                    <td>{f.closes}</td>
                    <td>{f.active_runs}</td>
                    <td>{f.failed_runs_7d}</td>
                    <td>{when(f.last_activity)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </section>
      )}
    </>
  );
}
