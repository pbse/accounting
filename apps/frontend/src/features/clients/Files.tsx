import { useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Card } from "@close/ui/components/Card";
import { Dialog, DialogActions } from "@close/ui/components/Dialog";
import { workspaceQuery } from "../../data/queries";
import { monthName, useDates } from "../../data/time";
import { workspaceRequest } from "../closes/workspace";

// A client's books as files (docs/books-sources.md, Reading files): a file is sent for a month, the reader proposes how
// to read it, a person previews the reading, and a reviewer confirms the layout, which is proposed again for the next
// month's file. Nothing here is a month's books yet: accepting an import arrives with a later step.

type Meaning = "account_number" | "account_name" | "account_type" | "debit" | "credit" | "balance" | "opening" | "movement" | "year_to_date";
export type Layout = {
  table: string;
  header_row: number;
  columns: Record<string, Meaning>;
  style: { decimal_comma: boolean; scale: number };
  balance_sign?: string;
  as_of: string;
  currency: string;
  headings?: number[];
  totals?: number[];
  postings?: number[];
  ignored?: number[];
  ignored_columns?: number[];
};
type Cell = { column: number; ref: string; kind: string; value: string; formula?: string };
type Row = { number: number; hidden?: boolean; cells: Cell[] };
type Table = { name: string; hidden?: boolean; rows: Row[]; more: number };
type Line = { row: number; ref: string; kind: "posting" | "total" | "heading"; number?: string; name: string; balance: string; cents: boolean };
type Reading = { lines: Line[]; debits: string; credits: string; balanced: boolean; problems: string[]; notes: string[] };
type Confirmed = { id: string; layout: Layout; reading: Reading; reason: string; actor: string; at: string; balanced: boolean };
type ImportRow = { id: string; period: string; name: string; format: string; actor: string; at: string; confirmed?: Confirmed };
type ImportRead = {
  import: ImportRow;
  tables: Table[];
  encoding?: string;
  unsure?: string[];
  linked_workbooks?: string[];
  recalculate_on_open?: boolean;
  formulas?: number;
  can_confirm: boolean;
  proposal?: { layout: Layout; questions: string[]; reused: boolean };
};

const meanings: [Meaning | "", string][] = [
  ["", "Not used"],
  ["account_number", "Account number"],
  ["account_name", "Account name"],
  ["account_type", "Account type"],
  ["debit", "Debit"],
  ["credit", "Credit"],
  ["balance", "Balance (signed)"],
  ["opening", "Opening balance"],
  ["movement", "Movement"],
  ["year_to_date", "Year to date"],
];
const filesPath = (name: string) => `/client/imports?name=${encodeURIComponent(name)}`;
const importPath = (name: string, id: string) => `/client/import?name=${encodeURIComponent(name)}&id=${encodeURIComponent(id)}`;

// FilesCard is the client's files: what was sent for which month, whether its layout is confirmed and balanced, a way to
// send another, and each one's reading.
export function FilesCard({ client }: { client: string }) {
  const queryClient = useQueryClient();
  const dates = useDates();
  const files = useQuery(workspaceQuery<{ imports: ImportRow[]; can_confirm: boolean }>(filesPath(client)));
  const [sending, setSending] = useState(false);
  const [open, setOpen] = useState("");
  const reread = () => void queryClient.invalidateQueries({ queryKey: ["workspace"] });
  const imports = files.data?.imports ?? [];
  const latest = imports[0];
  return (
    <>
      <Card
        icon="file"
        tone={latest && !latest.confirmed?.balanced ? "wait" : "done"}
        title="Files"
        chip={<span className="pill neutral">{imports.length ? `${imports.length} sent` : "None sent"}</span>}
        action={
          <button type="button" onClick={() => setSending(true)}>
            Send a file
          </button>
        }
      >
        {files.error
          ? "The client's files could not be read."
          : !latest
            ? "Send the client's Trial Balance as a CSV or Excel workbook for a month. Its layout is confirmed once and read the same way each month after."
            : `${monthName(latest.period)}: ${latest.name}, sent ${dates.dayMonth(latest.at)}${latest.confirmed ? (latest.confirmed.balanced ? ", read and balanced." : ", read, with problems to resolve.") : ", its layout not confirmed yet."}`}
        {imports.length > 0 && (
          <ul className="ks-filelist">
            {imports.slice(0, 6).map((i) => (
              <li key={i.id}>
                <button type="button" className="link" onClick={() => setOpen(i.id)}>
                  {monthName(i.period)} · {i.name}
                </button>{" "}
                {i.confirmed ? (i.confirmed.balanced ? <span className="pill">Balanced</span> : <span className="pill danger">Problems</span>) : <span className="pill muted">Layout to confirm</span>}
              </li>
            ))}
          </ul>
        )}
      </Card>
      <Dialog open={sending} onClose={() => setSending(false)} title="Send a file" description="The client's Trial Balance for one month, as a CSV or Excel workbook (.xlsx), up to 50 MiB.">
        <SendForm client={client} onSent={(id) => (setSending(false), reread(), setOpen(id))} onCancel={() => setSending(false)} />
      </Dialog>
      {open && (
        <Dialog open onClose={() => (setOpen(""), reread())} title="Read the file" description="How the file is read: which row heads it, what each column holds, and how amounts are written.">
          <ImportReader client={client} id={open} onDone={() => (setOpen(""), reread())} />
        </Dialog>
      )}
    </>
  );
}

function SendForm({ client, onSent, onCancel }: { client: string; onSent: (id: string) => void; onCancel: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function send(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const saved = await workspaceRequest(filesPath(client), new FormData(event.currentTarget));
      onSent(saved.id);
    } catch (e) {
      setError(e instanceof Error ? e.message : "The file could not be sent.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <form onSubmit={send}>
      <fieldset disabled={busy}>
        <label>
          Month
          <input type="month" name="period" required />
        </label>
        <label>
          File
          <input type="file" name="file" accept=".csv,.xlsx,text/csv,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" required />
        </label>
        {error && <p role="alert">{error}</p>}
        <DialogActions>
          <button type="button" onClick={onCancel}>
            Cancel
          </button>
          <button className="primary">{busy ? "Sending…" : "Send"}</button>
        </DialogActions>
      </fieldset>
    </form>
  );
}

// ImportReader shows a file's first rows, the layout to read it with (proposed, or the one confirmed), a preview of the
// reading, and, for a reviewer, confirming the layout with a reason.
function ImportReader({ client, id, onDone }: { client: string; id: string; onDone: () => void }) {
  const read = useQuery(workspaceQuery<ImportRead>(importPath(client, id)));
  if (read.error) return <p role="alert">{read.error.message || "The file could not be read."}</p>;
  if (!read.data) return <p role="status">Reading the file…</p>;
  const start = read.data.import.confirmed?.layout ?? read.data.proposal?.layout;
  return <LayoutForm key={read.data.import.id} client={client} id={id} data={read.data} start={start} onDone={onDone} />;
}

function LayoutForm({ client, id, data, start, onDone }: { client: string; id: string; data: ImportRead; start?: Layout; onDone: () => void }) {
  const [layout, setLayout] = useState<Layout>(
    start ?? { table: data.tables[0]?.name ?? "", header_row: 1, columns: {}, style: { decimal_comma: false, scale: 1 }, as_of: "", currency: "" },
  );
  const [reading, setReading] = useState<Reading | undefined>(data.import.confirmed?.reading);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const table = data.tables.find((t) => t.name === layout.table) ?? data.tables[0];
  const header = table?.rows.find((r) => r.number === layout.header_row);
  const width = Math.max(0, ...(table?.rows.slice(0, 40).flatMap((r) => r.cells.map((c) => c.column + 1)) ?? [0]));
  const columns = Array.from({ length: width }, (_, i) => i);
  async function act(action: "preview" | "confirm") {
    setBusy(true);
    setError("");
    try {
      const out = await workspaceRequest(importPath(client, id), { action, layout, reason: reason.trim() });
      setReading(out.reading);
      if (action === "confirm") onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The file could not be read with this layout.");
    } finally {
      setBusy(false);
    }
  }
  const set = (change: Partial<Layout>) => (setLayout({ ...layout, ...change }), setReading(undefined));
  return (
    <div className="ks-import">
      {data.proposal?.questions.length ? (
        <ul className="note">
          {data.proposal.reused && <li>Proposed the way the client’s last confirmed file was read: its headers are the same.</li>}
          {data.proposal.questions.map((q) => (
            <li key={q}>{q}</li>
          ))}
        </ul>
      ) : null}
      {data.unsure?.length ? <p className="note">The file's {data.unsure.join(" and ")} could be read another way; check the rows below.</p> : null}
      {data.recalculate_on_open && <p className="note">The workbook asks to be recalculated when opened, so its stored results may be out of date.</p>}
      {data.linked_workbooks?.length ? <p className="note">It links to other workbooks: {data.linked_workbooks.join(", ")}.</p> : null}
      <fieldset disabled={busy}>
        {data.tables.length > 1 && (
          <label>
            Sheet
            <select value={layout.table} onChange={(e) => set({ table: e.target.value })}>
              {data.tables.map((t) => (
                <option key={t.name} value={t.name}>
                  {t.name}
                  {t.hidden ? " (hidden)" : ""}
                </option>
              ))}
            </select>
          </label>
        )}
        <label>
          Header row
          <input type="number" min={1} value={layout.header_row} onChange={(e) => set({ header_row: Number(e.target.value) })} />
        </label>
        <div className="ks-columns">
          {columns.map((c) => (
            <label key={c}>
              {header?.cells.find((cell) => cell.column === c)?.value || `Column ${c + 1}`}
              <select
                value={layout.columns[String(c)] ?? ""}
                onChange={(e) => {
                  const next = { ...layout.columns };
                  if (e.target.value) next[String(c)] = e.target.value as Meaning;
                  else delete next[String(c)];
                  set({ columns: next });
                }}
              >
                {meanings.map(([value, label]) => (
                  <option key={value} value={value}>
                    {label}
                  </option>
                ))}
              </select>
            </label>
          ))}
        </div>
        {Object.values(layout.columns).includes("balance") && (
          <label>
            A positive balance is
            <select value={layout.balance_sign || "debit"} onChange={(e) => set({ balance_sign: e.target.value })}>
              <option value="debit">a debit</option>
              <option value="credit">a credit</option>
            </select>
          </label>
        )}
        <label>
          Amounts are in
          <select value={layout.style.scale} onChange={(e) => set({ style: { ...layout.style, scale: Number(e.target.value) } })}>
            <option value={1}>units</option>
            <option value={1000}>thousands</option>
            <option value={1000000}>millions</option>
          </select>
        </label>
        <label className="checkbox-label">
          <input type="checkbox" checked={layout.style.decimal_comma} onChange={(e) => set({ style: { ...layout.style, decimal_comma: e.target.checked } })} /> Decimals follow a comma (1.234,56)
        </label>
        <label>
          As of
          <input type="date" value={layout.as_of} onChange={(e) => set({ as_of: e.target.value })} />
        </label>
        <label>
          Currency
          <input value={layout.currency} maxLength={3} onChange={(e) => set({ currency: e.target.value.toUpperCase() })} />
        </label>
      </fieldset>
      <table className="ks-preview">
        <tbody>
          {table?.rows.slice(0, 25).map((r) => (
            <tr key={r.number} className={r.number === layout.header_row ? "header" : undefined}>
              <th>{r.number}</th>
              {columns.map((c) => (
                <td key={c}>{r.cells.find((cell) => cell.column === c)?.value}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      {reading && (
        <div className="ks-reading">
          <p>
            <span className={reading.balanced ? "pill" : "pill danger"}>{reading.balanced ? "Balanced" : "Not balanced"}</span> Debits {reading.debits}, credits {reading.credits},{" "}
            {reading.lines.filter((l) => l.kind === "posting").length} accounts.
          </p>
          {reading.problems.length > 0 && (
            <ul role="alert">
              {reading.problems.map((p) => (
                <li key={p}>{p}</li>
              ))}
            </ul>
          )}
          {reading.notes.length > 0 && (
            <ul className="note">
              {reading.notes.map((n) => (
                <li key={n}>{n}</li>
              ))}
            </ul>
          )}
        </div>
      )}
      {error && <p role="alert">{error}</p>}
      {data.can_confirm && (
        <label>
          Reason
          <input value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
        </label>
      )}
      <DialogActions>
        <button type="button" disabled={busy} onClick={() => void act("preview")}>
          {busy ? "Reading…" : "Preview the reading"}
        </button>
        {data.can_confirm && (
          <button type="button" className="primary" disabled={busy || !reading || !reason.trim()} onClick={() => void act("confirm")}>
            Confirm the layout
          </button>
        )}
      </DialogActions>
    </div>
  );
}
