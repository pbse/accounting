import { workspaceQuery } from "../../data/queries";
import { useQuery } from "@tanstack/react-query";
import { useDates } from "../../data/time";
import { useState } from "react";
import { money, workspaceRequest } from "./workspace";

type Party = { id: string; name: string; currency: string; buckets: string[]; unaged: string; credits: string; open: string; unresolved: number };
type Report =
  | { state: "waiting"; reason: string }
  | { state: "ready"; captured_at: string; month_end: string; complete: boolean; note: string; buckets: string[]; receivables: Party[] | null; payables: Party[] | null };

// An unknown currency is shown as a plain amount rather than guessed.
const show = (amount: string, currency: string) => (currency ? money(amount, currency) : `${amount} (currency unknown)`);

// Receivables and payables aged at month-end from the latest capture's open items. Statements are downloads only.
export function Aging({ closeId, open }: { closeId: string; open?: boolean }) {
  const dates = useDates();
  const read = useQuery(workspaceQuery<Report>(`/${closeId}/aging`));
  const report = read.data;
  const [actionError, setError] = useState("");
  const error = actionError || (read.error ? read.error.message || "Aging unavailable." : "");
  async function statement(party: Party) {
    try {
      const blob = await workspaceRequest(`/${closeId}/aging/statement?customer=${encodeURIComponent(party.id)}&currency=${encodeURIComponent(party.currency)}`, undefined, true);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `statement-${party.name.replace(/[^\w-]+/g, "-")}-${party.currency}.csv`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Statement unavailable.");
    }
  }
  if (!report) return error ? <p role="alert">{error}</p> : null;
  const table = (title: string, parties: Party[] | null, customers: boolean) =>
    (parties?.length ?? 0) > 0 && (
      <table>
        <caption>{title}</caption>
        <thead>
          <tr>
            <th>{customers ? "Customer" : "Vendor"}</th>
            {report.state === "ready" && report.buckets.map((b) => <th key={b}>{b}</th>)}
            <th>No due date</th>
            <th>Open</th>
            <th>Unused payments and credits</th>
            {customers && <th />}
          </tr>
        </thead>
        <tbody>
          {parties!.map((p, i) => (
            <tr key={`${p.id}-${p.currency}-${i}`}>
              <td>
                {p.name}
                {p.unresolved > 0 && <small> · {p.unresolved} unreadable, left out</small>}
              </td>
              {p.buckets.map((b, bucket) => (
                <td key={bucket}>{show(b, p.currency)}</td>
              ))}
              <td>{show(p.unaged, p.currency)}</td>
              <td>{show(p.open, p.currency)}</td>
              <td>{show(p.credits, p.currency)}</td>
              {customers && (
                <td>
                  <button type="button" disabled={!p.id} onClick={() => void statement(p)}>
                    Download statement
                  </button>
                </td>
              )}
            </tr>
          ))}
        </tbody>
      </table>
    );
  return (
    <details open={open}>
      <summary>Receivables and payables aging</summary>
      {error && <p role="alert">{error}</p>}
      {report.state === "waiting" ? (
        <p className="empty">{report.reason}</p>
      ) : (
        <>
          <p className="note">
            {report.note} Captured {dates.dateTime(report.captured_at)}; aged against {report.month_end}.
          </p>
          {!report.complete && <p role="status">Incomplete: some open items could not be read in this capture, so totals may be missing documents.</p>}
          {table("Receivables", report.receivables, true)}
          {table("Payables", report.payables, false)}
          <p className="note">Statements are downloads for the firm to use through its own channel. Nothing is sent, collected, paid or written off.</p>
        </>
      )}
    </details>
  );
}
