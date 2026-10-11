import { useState, type FormEvent } from "react";
import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query";
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
// Status is a confirmed layout's outcome, as the list shows it; Confirmed adds the layout and what it read.
type Status = { id: string; reason: string; actor: string; at: string; balanced: boolean; problems: number };
// CSV is how a person chose to read a CSV file; empty, as the reader read it.
type CSV = { encoding: string; delimiter: string };
type Confirmed = Status & { layout: Layout; reading: Reading; csv?: CSV };
type ImportRow = { id: string; period: string; name: string; format: string; actor: string; at: string; confirmed?: Status };
type ImportRead = {
  import: Omit<ImportRow, "confirmed"> & { confirmed?: Confirmed };
  tables: Table[];
  encoding?: string;
  delimiter?: string;
  unsure?: string[];
  linked_workbooks?: string[];
  recalculate_on_open?: boolean;
  formulas?: number;
  can_confirm: boolean;
  proposal?: { layout: Layout; questions: string[]; reused: boolean };
};

// A column is read for a meaning, left out on purpose (a memo, a note), or not chosen yet: values in a column not chosen
// are problems, so nothing in the file is passed over unseen.
const meanings: [Meaning | "" | "ignored", string][] = [
  ["", "Not chosen"],
  ["ignored", "Left out"],
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
// A row the reader cannot place is marked by a person: an account it took for a total, a total or heading it cannot name,
// or a footer to leave out.
type RowKind = "" | "postings" | "totals" | "headings" | "ignored";
const rowKinds: [RowKind, string][] = [
  ["", "As read"],
  ["postings", "Account"],
  ["totals", "Total"],
  ["headings", "Heading"],
  ["ignored", "Left out"],
];
// clean says a confirmed reading balances with no problem left.
const clean = (c: Status) => c.balanced && !c.problems;
const lineKinds = { posting: "Account", total: "Total", heading: "Heading" };
const filesPath = (name: string) => `/client/imports?name=${encodeURIComponent(name)}`;
const importPath = (name: string, id: string, csv?: CSV) =>
  `/client/import?name=${encodeURIComponent(name)}&id=${encodeURIComponent(id)}${csv?.encoding ? `&encoding=${csv.encoding}&delimiter=${csv.delimiter}` : ""}`;
const encodings = [
  ["utf-8", "UTF-8"],
  ["utf-16le", "UTF-16 (little-endian)"],
  ["utf-16be", "UTF-16 (big-endian)"],
  ["windows-1252", "Windows-1252"],
];
const delimiters = [
  ["comma", "Commas"],
  ["semicolon", "Semicolons"],
  ["tab", "Tabs"],
  ["pipe", "Pipes"],
];

// FilesCard is the client's files: what was sent for which month, whether its layout is confirmed and balanced, a way to
// send another, and each one's reading.
export function FilesCard({ client }: { client: string }) {
  const queryClient = useQueryClient();
  const dates = useDates();
  const files = useQuery(workspaceQuery<{ imports: ImportRow[]; can_confirm: boolean }>(filesPath(client)));
  const [sending, setSending] = useState(false);
  const [open, setOpen] = useState("");
  const [all, setAll] = useState(false);
  const reread = () => void queryClient.invalidateQueries({ queryKey: ["workspace"] });
  const imports = files.data?.imports ?? [];
  const latest = imports[0];
  return (
    <>
      <Card
        icon="file"
        tone={latest && !(latest.confirmed && clean(latest.confirmed)) ? "wait" : "done"}
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
            : `${monthName(latest.period)}: ${latest.name}, sent ${dates.dayMonth(latest.at)}${latest.confirmed ? (clean(latest.confirmed) ? ", read and balanced." : ", read, with problems to resolve.") : ", its layout not confirmed yet."}`}
        {imports.length > 0 && (
          <ul className="ks-filelist">
            {(all ? imports : imports.slice(0, 6)).map((i) => (
              <li key={i.id}>
                <button type="button" className="link" onClick={() => setOpen(i.id)}>
                  {monthName(i.period)} · {i.name}
                </button>{" "}
                {i.confirmed ? clean(i.confirmed) ? <span className="pill">Balanced</span> : <span className="pill danger">Problems</span> : <span className="pill muted">Layout to confirm</span>}
              </li>
            ))}
            {imports.length > 6 && (
              <li>
                <button type="button" className="link" onClick={() => setAll(!all)}>
                  {all ? "Show the latest six" : `Show all ${imports.length} files`}
                </button>
              </li>
            )}
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
        <label className="checkbox-label">
          <input type="checkbox" name="one_client" value="true" required /> It holds only {client}’s books
        </label>
        <p className="note">A file holding several clients’ books cannot be sent yet: whoever opens one client would see them all.</p>
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
  // A CSV is read as the reader reads it until a person chooses another encoding or delimiter.
  const [csv, setCsv] = useState<CSV | undefined>(undefined);
  const read = useQuery({ ...workspaceQuery<ImportRead>(importPath(client, id, csv)), placeholderData: keepPreviousData });
  if (!read.data) return read.error ? <p role="alert">{read.error.message || "The file could not be read."}</p> : <p role="status">Reading the file…</p>;
  const start = read.data.import.confirmed?.layout ?? read.data.proposal?.layout;
  return (
    <>
      {read.error && <p role="alert">{read.error.message || "The file could not be read that way."}</p>}
      <LayoutForm key={read.data.import.id} client={client} id={id} data={read.data} start={start} csv={csv} onCsv={setCsv} onDone={onDone} />
    </>
  );
}

function LayoutForm({
  client,
  id,
  data,
  start,
  csv,
  onCsv,
  onDone,
}: {
  client: string;
  id: string;
  data: ImportRead;
  start?: Layout;
  csv?: CSV;
  onCsv: (csv: CSV) => void;
  onDone: () => void;
}) {
  const [layout, setLayout] = useState<Layout>(
    start ?? { table: data.tables[0]?.name ?? "", header_row: 1, columns: {}, style: { decimal_comma: false, scale: 1 }, as_of: "", currency: "" },
  );
  const [reading, setReading] = useState<Reading | undefined>(data.import.confirmed?.reading);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // Rows come in pages: the first, or from a row a person asks for; the header keeps its names from the first page.
  const [from, setFrom] = useState(1);
  const [goTo, setGoTo] = useState("");
  const later = useQuery({ ...workspaceQuery<ImportRead>(`${importPath(client, id, csv)}&from=${from}`), enabled: from > 1 });
  const first = data.tables.find((t) => t.name === layout.table) ?? data.tables[0];
  const table = from > 1 ? (later.data?.tables.find((t) => t.name === first?.name) ?? undefined) : first;
  const header = first?.rows.find((r) => r.number === layout.header_row);
  const width = Math.max(0, ...(first?.rows.slice(0, 40).flatMap((r) => r.cells.map((c) => c.column + 1)) ?? [0]));
  const columns = Array.from({ length: width }, (_, i) => i);
  async function act(action: "preview" | "confirm") {
    setBusy(true);
    setError("");
    try {
      const out = await workspaceRequest(importPath(client, id), { action, layout, reason: reason.trim(), csv: csv ?? { encoding: "", delimiter: "" } });
      setReading(out.reading);
      if (action === "confirm") onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The file could not be read with this layout.");
    } finally {
      setBusy(false);
    }
  }
  const set = (change: Partial<Layout>) => (setLayout({ ...layout, ...change }), setReading(undefined));
  const kindOf = (row: number): RowKind => (["postings", "totals", "headings", "ignored"] as const).find((k) => layout[k]?.includes(row)) ?? "";
  const mark = (row: number, kind: RowKind) => {
    const next: Partial<Layout> = {};
    for (const k of ["postings", "totals", "headings", "ignored"] as const) next[k] = (layout[k] ?? []).filter((r) => r !== row);
    if (kind) next[kind] = [...(next[kind] ?? []), row].sort((a, b) => a - b);
    set(next);
  };
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
      {data.unsure?.length ? <p className="note">The file's {data.unsure.join(" and ")} could be read another way: check the rows below, and choose another below if they read wrongly.</p> : null}
      {data.recalculate_on_open && <p className="note">The workbook asks to be recalculated when opened, so its stored results may be out of date.</p>}
      {data.linked_workbooks?.length ? <p className="note">It links to other workbooks: {data.linked_workbooks.join(", ")}.</p> : null}
      <fieldset disabled={busy}>
        {data.encoding && (
          <>
            <label>
              Text encoding
              <select value={data.encoding} onChange={(e) => (onCsv({ encoding: e.target.value, delimiter: data.delimiter ?? "comma" }), setReading(undefined))}>
                {encodings.map(([value, label]) => (
                  <option key={value} value={value}>
                    {label}
                  </option>
                ))}
              </select>
            </label>
            <label>
              Values separated by
              <select value={data.delimiter} onChange={(e) => (onCsv({ encoding: data.encoding ?? "utf-8", delimiter: e.target.value }), setReading(undefined))}>
                {delimiters.map(([value, label]) => (
                  <option key={value} value={value}>
                    {label}
                  </option>
                ))}
              </select>
            </label>
          </>
        )}
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
                value={layout.columns[String(c)] ?? (layout.ignored_columns?.includes(c) ? "ignored" : "")}
                onChange={(e) => {
                  const next = { ...layout.columns };
                  delete next[String(c)];
                  const ignored = (layout.ignored_columns ?? []).filter((x) => x !== c);
                  if (e.target.value === "ignored") ignored.push(c);
                  else if (e.target.value) next[String(c)] = e.target.value as Meaning;
                  set({ columns: next, ignored_columns: ignored.sort((a, b) => a - b) });
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
      <p className="note">Mark a row only when the reading asks: an account it took for a total, a total or heading it cannot name, or a footer to leave out.</p>
      <table className="ks-preview">
        <tbody>
          {table?.rows.map((r) => (
            <tr key={r.number} className={r.number === layout.header_row ? "header" : undefined}>
              <th>{r.number}</th>
              <td>
                {r.number > layout.header_row && (
                  <select aria-label={`How row ${r.number} is read`} value={kindOf(r.number)} disabled={busy} onChange={(e) => mark(r.number, e.target.value as RowKind)}>
                    {rowKinds.map(([value, label]) => (
                      <option key={value} value={value}>
                        {label}
                      </option>
                    ))}
                  </select>
                )}
              </td>
              {columns.map((c) => (
                <td key={c}>{r.cells.find((cell) => cell.column === c)?.value}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      {from > 1 && !table && <p role="status">{later.error ? "Those rows could not be read." : "Reading those rows…"}</p>}
      <div className="setting-actions">
        {from > 1 && (
          <button type="button" onClick={() => setFrom(1)}>
            First rows
          </button>
        )}
        {table && table.more > 0 && table.rows.length > 0 && (
          <button type="button" onClick={() => setFrom(table.rows[table.rows.length - 1].number + 1)}>
            Next rows ({table.more} more)
          </button>
        )}
        <label>
          Rows from
          <input type="number" min={1} value={goTo} onChange={(e) => setGoTo(e.target.value)} />
        </label>
        <button type="button" disabled={!(Number(goTo) >= 1)} onClick={() => setFrom(Number(goTo))}>
          Show
        </button>
      </div>
      {reading && (
        <div className="ks-reading">
          <p>
            <span className={reading.balanced && !reading.problems.length ? "pill" : "pill danger"}>
              {!reading.balanced ? "Not balanced" : reading.problems.length ? "Balanced, with problems" : "Balanced"}
            </span>{" "}
            Debits {reading.debits}, credits {reading.credits},{" "}
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
          <details>
            <summary>How each row was read</summary>
            <table className="ks-preview">
              <tbody>
                {reading.lines.map((l) => (
                  <tr key={l.row}>
                    <th>{l.row}</th>
                    <td>{lineKinds[l.kind]}</td>
                    <td>{[l.number, l.name].filter(Boolean).join(" ")}</td>
                    <td>{l.kind === "heading" ? "" : l.balance}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </details>
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
          <button type="button" className="primary" disabled={busy || !reading || !reason.trim() || !layout.as_of || !layout.currency} onClick={() => void act("confirm")}>
            Confirm the layout
          </button>
        )}
      </DialogActions>
    </div>
  );
}
