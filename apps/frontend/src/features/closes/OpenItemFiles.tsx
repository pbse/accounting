import { useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { workspaceQuery } from "../../data/queries";
import { useDates } from "../../data/time";
import { workspaceRequest } from "./workspace";

// Open items by file (docs/books-sources.md, Open items and cash dates): a month whose books are its files has its open
// receivables and payables sent as exports, each true at its own date. A reviewer chooses the file, sees how its columns
// are proposed to read and the documents they give, changes a column's role if need be, and keeps it; the newest file of
// each side is the month's open items.

type Role = "party" | "document" | "date" | "due" | "open" | "currency";
type Layout = { table: string; header_row: number; columns: Record<string, Role>; date_order?: string; style: { decimal_comma: boolean; scale: number } };
type Reading = {
  layout: Layout;
  headings: { column: number; value: string }[];
  documents: number;
  total: string;
  preview: { row: number; party: string; document: string; date?: string; due?: string; open: string }[];
  problems: string[];
};
type Kept = { id: string; side: "receivable" | "payable"; as_of: string; name: string; documents: number; problems: string[]; actor: string; created_at: string };

const roleNames: Record<Role, string> = { party: "Customer or vendor", document: "Document number", date: "Date", due: "Due date", open: "Open amount", currency: "Currency" };

export function OpenItemFiles({ closeId }: { closeId: string }) {
  const queryClient = useQueryClient();
  const dates = useDates();
  const path = `/${closeId}/open-item-files`;
  const read = useQuery(workspaceQuery<{ files: Kept[]; on: boolean; can_send: boolean }>(path));
  const [chosen, setChosen] = useState<{ side: string; as_of: string; file: File }>();
  const [reading, setReading] = useState<Reading>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function post(choice: { side: string; as_of: string; file: File }, layout?: Layout, confirmed = false) {
    const form = new FormData();
    form.set("side", choice.side);
    form.set("as_of", choice.as_of);
    form.set("file", choice.file);
    if (layout) form.set("layout", JSON.stringify(layout));
    if (confirmed) form.set("confirmed", "true");
    setBusy(true);
    setError("");
    try {
      const out = await workspaceRequest(path, form);
      if (confirmed) {
        setChosen(undefined);
        setReading(undefined);
        await queryClient.invalidateQueries({ queryKey: ["workspace"] });
      } else setReading(out);
    } catch (e) {
      setError(e instanceof Error ? e.message : "The file could not be read.");
    } finally {
      setBusy(false);
    }
  }
  function choose(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const file = form.get("file");
    if (!(file instanceof File) || file.size === 0) return;
    const choice = { side: String(form.get("side")), as_of: String(form.get("as_of")), file };
    setChosen(choice);
    void post(choice);
  }
  // The order dates written in numbers are in, chosen when the file does not say: the file is read again with it.
  function order(dateOrder: string) {
    if (chosen && reading) void post(chosen, { ...reading.layout, date_order: dateOrder || undefined });
  }
  // A column's role changed: the file is read again with it.
  function assign(column: number, role: Role | "") {
    if (!chosen || !reading) return;
    const columns = Object.fromEntries(Object.entries(reading.layout.columns).filter(([c, r]) => Number(c) !== column && r !== role));
    if (role) columns[String(column)] = role;
    void post(chosen, { ...reading.layout, columns });
  }
  if (!read.data?.on) return null;
  const { files, can_send } = read.data;
  return (
    <section aria-label="Open items from files">
      <h3>Open items from files</h3>
      {files.length === 0 ? (
        <p className="empty">No open receivables or payables were sent for this month yet.</p>
      ) : (
        <ul>
          {files.map((f) => (
            <li key={f.id}>
              {f.side === "receivable" ? "Receivables" : "Payables"} as of {dates.calendarDate(f.as_of)}: {f.name}, {f.documents} document{f.documents === 1 ? "" : "s"}, sent{" "}
              {dates.dayMonth(f.created_at)} by {f.actor}
              {f.problems.length > 0 && <small> {f.problems.join(" ")}</small>}
            </li>
          ))}
        </ul>
      )}
      {error && <p role="alert">{error}</p>}
      {can_send && !reading && (
        <form onSubmit={choose}>
          <fieldset disabled={busy}>
            <label>
              The file holds
              <select name="side" required>
                <option value="receivable">Open receivables (unpaid invoices)</option>
                <option value="payable">Open payables (unpaid bills)</option>
              </select>
            </label>
            <label>
              True as of
              <input type="date" name="as_of" required />
            </label>
            <label>
              File
              <input type="file" name="file" accept=".csv,.xlsx" required />
            </label>
            <button type="submit">{busy ? "Reading…" : "Read the file"}</button>
          </fieldset>
        </form>
      )}
      {can_send && reading && chosen && (
        <div className="stack">
          <p>
            {reading.documents} open document{reading.documents === 1 ? "" : "s"}, {reading.total} in all, read from row {reading.layout.header_row}'s headings.
          </p>
          {reading.headings.map((h) => (
            <label key={h.column}>
              {h.value || `Column ${h.column + 1}`}
              <select value={reading.layout.columns[String(h.column)] ?? ""} disabled={busy} onChange={(e) => assign(h.column, e.target.value as Role | "")}>
                <option value="">Not used</option>
                {(Object.keys(roleNames) as Role[]).map((r) => (
                  <option key={r} value={r}>
                    {roleNames[r]}
                  </option>
                ))}
              </select>
            </label>
          ))}
          <label>
            Dates written in numbers read
            <select value={reading.layout.date_order ?? ""} disabled={busy} onChange={(e) => order(e.target.value)}>
              <option value="">Only where they read one way</option>
              <option value="mdy">Month first (01/31/2027)</option>
              <option value="dmy">Day first (31/01/2027)</option>
              <option value="ymd">Year first (2027-01-31)</option>
            </select>
          </label>
          {reading.problems.length > 0 && <p role="status">{reading.problems.join(" ")}</p>}
          <ul>
            {reading.preview.map((p) => (
              <li key={p.row}>
                {p.party} {p.document} {p.date ?? ""} due {p.due ?? "—"}: {p.open}
              </li>
            ))}
          </ul>
          <p>
            <button type="button" className="primary" disabled={busy} onClick={() => void post(chosen, reading.layout, true)}>
              {reading.documents === 0
                ? `Keep: no ${chosen.side === "receivable" ? "receivable" : "payable"} is open`
                : `Keep as the month's open ${chosen.side === "receivable" ? "receivables" : "payables"}`}
            </button>{" "}
            <button type="button" disabled={busy} onClick={() => (setChosen(undefined), setReading(undefined))}>
              Cancel
            </button>
          </p>
        </div>
      )}
    </section>
  );
}
