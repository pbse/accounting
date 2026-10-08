import { useEffect, useState } from "react";
import { Link } from "react-router";
import { Avatar } from "@close/ui/components/Avatar";
import { EntityCell, RowList } from "@close/ui/components/RowList";
import { kindLabels } from "../closes/Work";
import { PlanChips, type Plan } from "../closes/Planning";
import { workspaceRequest } from "../closes/workspace";

export type QueueItem = Plan & { workpaper_id: string; close_id: string; entity: string; period: string; kind: string; created_at: string; digest: string; review_id: string; bulk_reason: string; approval_note?: string; approval_requested_by?: string; approval_agent?: string };

// The drafts ready for review across the reader's clients, each opening its own page, and up to twenty approved at
// once after a preview of exactly what that approves. Approval records a review decision; it never posts or signs off.
export function BulkReview({ items, onDone }: { items?: QueueItem[]; onDone: () => void }) {
  const [selected, setSelected] = useState<string[]>([]);
  const [confirming, setConfirming] = useState<QueueItem[]>();
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [results, setResults] = useState<{ workpaper_id: string; review_id?: string; error?: string }[]>([]);
  const [error, setError] = useState("");
  // A selection follows the list: a draft the list no longer shows (another filter, a change) leaves the selection,
  // and a preview being read stays open while the drafts it covers are unchanged (the list is read every 30 seconds).
  useEffect(() => {
    setSelected((current) => current.filter((id) => items?.some((item) => item.workpaper_id === id)));
    setConfirming((current) => (current && current.every((c) => items?.some((item) => item.workpaper_id === c.workpaper_id && item.digest === c.digest && item.review_id === c.review_id)) ? current : undefined));
  }, [items]);
  const chosen = confirming ?? [];
  const label = (item: QueueItem) => `${item.entity} · ${item.period} · ${kindLabels[item.kind] ?? item.kind} #${item.workpaper_id}`;
  async function approve() {
    setBusy(true);
    setError("");
    try {
      const data = await workspaceRequest("/review-batch", {
        note,
        items: chosen.map((item) => ({ close_id: item.close_id, workpaper_id: item.workpaper_id, digest: item.digest, expected_review_id: item.review_id })),
      });
      setResults(data.results);
      setSelected([]);
      setConfirming(undefined);
      setNote("");
      onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Bulk approval failed. Nothing was approved by this request.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <RowList>
        {!items && <p className="empty">Review queue unavailable. Open a close to review its drafts.</p>}
        {items && items.length === 0 && <p className="empty">Nothing is waiting for review.</p>}
        {items?.map((item) => (
          <div className="row" key={item.workpaper_id}>
            <EntityCell
              leading={
                <>
                  <input
                    type="checkbox"
                    aria-label={`Select ${label(item)} for bulk approval`}
                    disabled={!!item.bulk_reason || busy || (!selected.includes(item.workpaper_id) && selected.length >= 20)}
                    checked={selected.includes(item.workpaper_id)}
                    onChange={(e) => {
                      setConfirming(undefined);
                      setSelected((current) => (e.target.checked ? [...current, item.workpaper_id] : current.filter((id) => id !== item.workpaper_id)));
                    }}
                  />
                  <Avatar label={item.entity} />
                </>
              }
              title={`${item.entity} · ${item.period}`}
              detail={`${kindLabels[item.kind] ?? item.kind} · draft #${item.workpaper_id}${item.bulk_reason ? ` · review individually: ${item.bulk_reason.toLowerCase()}` : ""}`}
            >
              {item.approval_note && <small title={item.approval_note}>Approval requested{item.approval_requested_by ? ` by ${item.approval_requested_by}` : ""}{item.approval_agent ? ` via ${item.approval_agent}` : ""}: {item.approval_note}</small>}
            </EntityCell>
            <span className="status warn">{item.approval_note ? "Approval requested" : "Awaiting review"}</span>
            <PlanChips plan={item} />
            <Link className="button" to={`/dashboard/closes/${item.close_id}/workpapers/${item.workpaper_id}`}>
              Review
            </Link>
          </div>
        ))}
      </RowList>
      {selected.length > 0 && !confirming && (
        <div className="toolbar">
          <button type="button" className="primary" onClick={() => setConfirming((items ?? []).filter((item) => selected.includes(item.workpaper_id)))}>
            Approve {selected.length} selected
          </button>
          <button type="button" onClick={() => setSelected([])}>
            Clear selection
          </button>
        </div>
      )}
      {confirming && (
        <section aria-label="Confirm bulk approval">
          <h3>Approve {chosen.length === 1 ? "1 draft" : `${chosen.length} drafts`}</h3>
          <ul>
            {chosen.map((item) => (
              <li key={item.workpaper_id}>{label(item)}</li>
            ))}
          </ul>
          <p>
            No ledger changes. Approval records a review decision on each exact draft; it is not posting or sign-off. You approve as yourself and need the reviewer role. Each draft is rechecked individually, so some can succeed while others are refused.
          </p>
          <label>
            What did you check across these drafts?
            <textarea required maxLength={2000} value={note} onChange={(e) => setNote(e.target.value)} />
          </label>
          <div className="toolbar">
            <button type="button" className="primary" disabled={busy || !note.trim()} onClick={() => void approve()}>
              {busy ? "Approving…" : `Approve ${chosen.length === 1 ? "1 draft" : `${chosen.length} drafts`}`}
            </button>
            <button type="button" disabled={busy} onClick={() => setConfirming(undefined)}>
              Back
            </button>
          </div>
          {error && <p role="alert">{error}</p>}
        </section>
      )}
      {results.length > 0 && (
        <section role="status" aria-label="Bulk approval results">
          <h3>Approval results</h3>
          <ul>
            {results.map((result) => (
              <li key={result.workpaper_id}>
                Draft #{result.workpaper_id}: {result.review_id ? "approved" : result.error}
              </li>
            ))}
          </ul>
        </section>
      )}
    </>
  );
}
