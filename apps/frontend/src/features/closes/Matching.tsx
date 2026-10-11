import { useDates } from "../../data/time";
import { useEffect, useRef, useState } from "react";
import { money, workspaceRequest } from "./workspace";

type Item = { id: string; kind: string; name: string; number?: string; date?: string; currency: string; amount: string };
type Group = { currency: string; amount: string; documents: Item[]; transactions: Item[] };
type Report =
  | { state: "waiting"; reason: string }
  | {
      state: "ready";
      captured_at: string;
      note: string;
      pairs: Group[];
      ambiguous: Group[];
      documents_not_in_capture: Item[];
      documents_without_total: Item[];
      transactions_without_document: Item[];
      transactions_without_document_total: number;
      has_more: boolean;
      unreadable_transactions: number;
    };

const show = (amount: string, currency: string) => (currency ? money(amount, currency) : `${amount} (currency unknown)`);
const label = (item: Item) => [item.kind === "document" ? "" : item.kind, item.name || "No name", item.number, item.date].filter(Boolean).join(" · ");

// Uploaded prepaid invoices beside the latest capture's bills and purchases, paired only on a unique currency and exact amount.
export function Matching({ closeId, open }: { closeId: string; open?: boolean }) {
  const dates = useDates();
  const [report, setReport] = useState<Report>();
  const [error, setError] = useState("");
  const latest = useRef(0);
  // Read each time the panel opens, so a newer capture or document shows; an older answer is dropped.
  function load() {
    const n = ++latest.current;
    setError("");
    setReport(undefined);
    workspaceRequest(`/${closeId}/matching`)
      .then((r) => n === latest.current && setReport(r))
      .catch((e) => n === latest.current && setError(e instanceof Error ? e.message : "Matching unavailable."));
  }
  // Shown open (as Books' view), the panel reads at once rather than on a toggle.
  useEffect(() => {
    if (open) load();
  }, [open, closeId]);
  const list = (title: string, items: Item[], more = "") =>
    items.length > 0 && (
      <>
        <h4>{title}</h4>
        <ul>
          {items.map((item) => (
            <li key={`${item.kind}-${item.id}`}>
              {label(item)} {item.amount && `· ${show(item.amount, item.currency)}`}
            </li>
          ))}
        </ul>
        {more && <p className="note">{more}</p>}
      </>
    );
  const groups = (title: string, items: Group[]) =>
    items.length > 0 && (
      <>
        <h4>{title}</h4>
        <ul>
          {items.map((g) => (
            <li key={`${g.currency}-${g.amount}`}>
              {show(g.amount, g.currency)}: {g.documents.map(label).join(", ")} ↔ {g.transactions.map(label).join(", ")}
            </li>
          ))}
        </ul>
      </>
    );
  return (
    <details open={open} onToggle={(e) => e.currentTarget.open && load()}>
      <summary>Documents and ledger transactions</summary>
      {error && <p role="alert">{error}</p>}
      {!report ? null : report.state === "waiting" ? (
        <p className="empty">{report.reason}</p>
      ) : (
        <>
          <p className="note">
            {report.note} Captured {dates.dateTime(report.captured_at)}.
          </p>
          {groups("Possible matches", report.pairs)}
          {groups("Same amount, more than one candidate", report.ambiguous)}
          {list("Documents not found in this month's captured bills and purchases", report.documents_not_in_capture)}
          {list("Documents without a total quoted from the invoice", report.documents_without_total)}
          {list(
            "Bills and purchases without an uploaded document in this close",
            report.transactions_without_document,
            report.has_more ? `Showing the largest ${report.transactions_without_document.length} of ${report.transactions_without_document_total}.` : "",
          )}
          {report.unreadable_transactions > 0 && <p role="status">{report.unreadable_transactions} captured bills or purchases have an unreadable total and are left out.</p>}
        </>
      )}
    </details>
  );
}
