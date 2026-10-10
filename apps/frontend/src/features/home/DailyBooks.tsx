import { useDates } from "../../data/time";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { workspaceQuery } from "../../data/queries";
import { Link } from "react-router";
import { EntityCell, RowList } from "@close/ui/components/RowList";
import { SectionHeader } from "@close/ui/components/SectionHeader";
import { workspaceRequest } from "../closes/workspace";
import { ClientFilter } from "./Decisions";

type Daily = { close_id: string; entity: string; period: string; source: "quickbooks" | "xero"; sync_id: string; captured_at: string; requested_by: string; total: number; new: { type: string; question: string }[]; unchecked?: string };

// New exceptions from this and last month's captures since each client was last reviewed, and captures whose
// transaction check did not finish. Shown only while there is one; marking a capture reviewed clears its count.
// Mark reviewed shows to a reviewer alone, as the server requires.
export function DailyBooks({ canReview, onReviewed }: { canReview: boolean; onReviewed: () => void }) {
  const [busy, setBusy] = useState("");
  const dates = useDates();
  const [actionError, setActionError] = useState("");
  // The read covers 100 closes; a client or month filter reaches the rest. Each filter is its own read, every 30
  // seconds, so a new filter shows none of the previous rows (nor their Mark reviewed) before it answers, and a later
  // read of the same filter keeps the last answer beside any error.
  const [client, setClient] = useState("");
  const query = useQuery({ ...workspaceQuery<{ items: Daily[]; has_more?: boolean }>(`/daily?client=${encodeURIComponent(client)}`), refetchInterval: 30_000 });
  const items = query.data?.items;
  const more = !!query.data?.has_more;
  const error = actionError || (query.error ? query.error.message || "Daily books are unavailable." : "");
  async function reviewed(item: Daily) {
    setBusy(item.close_id);
    setActionError("");
    try {
      await workspaceRequest(`/${item.close_id}/daily-review`, { sync_id: item.sync_id, source: item.source });
      onReviewed();
    } catch (e) {
      setActionError(e instanceof Error ? e.message : "The review could not be saved.");
    } finally {
      setBusy("");
    }
  }
  const open = items?.filter((item) => item.new.length > 0) ?? [];
  const unchecked = items?.filter((item) => item.unchecked) ?? [];
  // A capped read may hold an unchecked capture beyond its 100 rows, so the section and its filter stay while it is.
  if (!error && !client && !more && open.length === 0 && unchecked.length === 0) return null;
  return (
    <>
      <SectionHeader title="New in the books" id="daily-books">
        {more || client ? <ClientFilter value={client} onChange={setClient} shown={open.length + unchecked.length} of={0} /> : <small>Since each client's last review.</small>}
      </SectionHeader>
      <RowList label="Daily books">
        {error && <p role="alert" className="empty">{error}{items ? " Showing the last read." : ""}</p>}
        {client && !error && open.length + unchecked.length === 0 && <p className="empty">Nothing new matching “{client}”.</p>}
        {!client && !error && more && open.length + unchecked.length === 0 && <p className="empty">Nothing new among the first 100 closes; filter by client or month for the rest.</p>}
        {unchecked.map((item) => (
          <div className="row" key={`unchecked-${item.close_id}`}>
            <EntityCell name={item.entity} title={`${item.entity} · ${item.period}`} detail={`Transaction check incomplete in the ${item.source === "xero" ? "Xero" : "QuickBooks"} capture of ${dates.dateTime(item.captured_at)}: ${item.unchecked}`} />
            <Link to={`/dashboard/closes/${item.close_id}?tab=books`}>Open the close</Link>
          </div>
        ))}
        {open.map((item) => (
          <div className="row" key={item.close_id}>
            <EntityCell name={item.entity} title={`${item.entity} · ${item.period}`} detail={`${item.new.length} new of ${item.total} · captured ${dates.dateTime(item.captured_at)}`} />
            <small>{item.new.slice(0, 2).map((e) => e.question).join(" ")}</small>
            <Link to={`/dashboard/closes/${item.close_id}?tab=books`}>Open exceptions</Link>
            {canReview && (
              <button type="button" disabled={busy === item.close_id} onClick={() => void reviewed(item)}>
                Mark reviewed
              </button>
            )}
          </div>
        ))}
      </RowList>
    </>
  );
}
