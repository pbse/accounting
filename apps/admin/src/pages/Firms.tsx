import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useResource, when } from "../api";
import { PageHeader } from "@close/ui/components/PageHeader";

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
  const offset = Number(params.get("offset") ?? 0) || 0;
  const [draft, setDraft] = useState(query);
  useEffect(() => setDraft(query), [query]); // Back and Forward restore the search box too
  const search = new URLSearchParams();
  if (query) search.set("q", query);
  if (offset) search.set("offset", String(offset));
  const firms = useResource<{ firms: FirmRow[]; has_more: boolean; next_offset: number }>(`/v1/admin/firms${search.size ? `?${search}` : ""}`);
  const page = (next: number) => setParams({ ...(query ? { q: query } : {}), ...(next ? { offset: String(next) } : {}) });
  return (
    <>
      <PageHeader
        icon="firm"
        title="Firms"
        actions={
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
        }
      >
        Every firm, newest first.
      </PageHeader>
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
          {(offset > 0 || firms.data.has_more) && (
            <p className="title-actions">
              {offset > 0 && <button onClick={() => page(0)}>First page</button>}
              {firms.data.has_more && <button onClick={() => page(firms.data!.next_offset)}>More firms</button>}
            </p>
          )}
        </section>
      )}
    </>
  );
}
