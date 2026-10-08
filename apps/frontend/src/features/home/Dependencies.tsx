import { useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import { Icon } from "@close/ui/components/Icon";
import { EntityCell, RowList } from "@close/ui/components/RowList";
import { SectionHeader } from "@close/ui/components/SectionHeader";
import { kindLabels } from "../closes/Work";
import { workspaceRequest } from "../closes/workspace";
import { ClientFilter } from "./Home";

type Dependency = { workpaper_id: string; close_id: string; entity: string; period: string; kind: string; upstream_workpaper_id: string };
type Predicted = { workpaper_id: string; close_id: string; entity: string; period: string; kind: string; transaction: string; account: string; updated_at: string; approved_at: string; count: number };
type Untied = { workpaper_id: string; close_id: string; entity: string; period: string; kind: string; account: string; balance: string; expected: string; difference: string; captured_at: string };
type Read = { items: Dependency[]; has_more: boolean; predicted?: Predicted[]; predicted_more: boolean; no_longer_ties?: Untied[]; no_longer_ties_more: boolean };

// Approved or current drafts whose support moved after the fact: a changed source or inherited schedule, an account
// that no longer ties in a later Trial Balance, or a transaction updated after approval. Shown only while there is one.
export function Dependencies({ refresh }: { refresh: number }) {
  const [read, setRead] = useState<Read>();
  const [error, setError] = useState("");
  // The read checks the ten open closes with the newest Trial Balances and the 100 newest affected drafts; a client
  // or month filter reaches the others.
  const [client, setClient] = useState("");
  // A new filter clears the previous filter's rows; a refresh of the same filter keeps the last read beside any error.
  const shownFor = useRef(client);
  useEffect(() => {
    let live = true;
    if (shownFor.current !== client) {
      shownFor.current = client;
      setRead(undefined);
    }
    workspaceRequest(`/dependencies?client=${encodeURIComponent(client)}`)
      .then((data: Read) => {
        if (live) {
          setRead(data);
          setError("");
        }
      })
      .catch((e) => live && setError(e instanceof Error ? e.message : "Dependency review is unavailable."));
    return () => {
      live = false;
    };
  }, [refresh, client]);
  const untied = read?.no_longer_ties ?? [];
  const predicted = (read?.predicted ?? []).filter((item) => !untied.some((u) => u.workpaper_id === item.workpaper_id));
  const changed = read?.items ?? [];
  const capped = !!read && (read.has_more || read.no_longer_ties_more || read.predicted_more);
  // A capped read that left closes unchecked keeps the section and its filter, so a later close can still be reached.
  if (!error && !client && !capped && changed.length === 0 && untied.length === 0 && predicted.length === 0) return null;
  const draft = (item: { kind: string; workpaper_id: string }) => `${kindLabels[item.kind] ?? item.kind} · draft #${item.workpaper_id}`;
  return (
    <>
      <SectionHeader title="Needs re-review" id="dependency-review">
        {capped || client ? <ClientFilter value={client} onChange={setClient} shown={changed.length + untied.length + predicted.length} of={0} /> : <small>An earlier approval is not reused once what it rested on changes.</small>}
      </SectionHeader>
      {client && changed.length + untied.length + predicted.length === 0 && <p className="empty">Nothing matching “{client}” needs re-review.</p>}
      {!client && capped && changed.length + untied.length + predicted.length === 0 && <p className="empty">Nothing among the closes checked first; filter by client or month to check others.</p>}
      {error && <p role="alert">{error}{read ? " Showing the last read." : ""}</p>}
      {changed.length > 0 && (
        <RowList label="Changed sources">
          {changed.map((item) => (
            <Link className="row" key={item.workpaper_id} to={`/dashboard/closes/${item.close_id}/workpapers/${item.workpaper_id}`}>
              <EntityCell name={item.entity} title={`${item.entity} · ${item.period}`} detail={draft(item)} />
              <span className="status danger">Evidence changed</span>
              <small>{item.upstream_workpaper_id ? `Linked to prior draft #${item.upstream_workpaper_id}` : "Linked source changed"}</small>
              <Icon name="arrow" />
            </Link>
          ))}
          {read?.has_more && <p className="note">Showing 100 recent affected drafts; filter by client or month for the rest.</p>}
        </RowList>
      )}
      {untied.length > 0 && (
        <RowList label="No longer ties">
          {untied.map((item) => (
            <Link className="row" key={`${item.workpaper_id}-${item.account}`} to={`/dashboard/closes/${item.close_id}/workpapers/${item.workpaper_id}`}>
              <EntityCell name={item.entity} title={`${item.entity} · ${item.period}`} detail={draft(item)} />
              <span className="status danger">No longer ties</span>
              <small>
                {item.account} is {item.balance} in the Trial Balance captured {new Date(item.captured_at).toLocaleDateString()} but {item.expected} in its support, a difference of {item.difference}. Sign-off waits until it ties.
              </small>
              <Icon name="arrow" />
            </Link>
          ))}
          {read?.no_longer_ties_more && <p className="note">Checked the 10 open closes with the newest Trial Balances; filter by client or month to check others.</p>}
        </RowList>
      )}
      {predicted.length > 0 && (
        <RowList label="May need re-review">
          {predicted.map((item) => (
            <Link className="row" key={item.workpaper_id} to={`/dashboard/closes/${item.close_id}/workpapers/${item.workpaper_id}`}>
              <EntityCell name={item.entity} title={`${item.entity} · ${item.period}`} detail={draft(item)} />
              <span className="status warn">May be affected</span>
              <small>
                {item.transaction} on {item.account} updated {new Date(item.updated_at).toLocaleDateString()}, after approval on {new Date(item.approved_at).toLocaleDateString()}
                {item.count > 1 ? ` (${item.count} such transactions)` : ""}. The approval stands until you review again.
              </small>
              <Icon name="arrow" />
            </Link>
          ))}
          {read?.predicted_more && <p className="note">Showing 100 drafts; filter by client or month for the rest.</p>}
        </RowList>
      )}
    </>
  );
}
