import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query";
import { Card } from "@close/ui/components/Card";
import { Dialog, DialogActions } from "@close/ui/components/Dialog";
import { workspaceQuery } from "../../data/queries";
import { monthName, useDates } from "../../data/time";
import { workspaceRequest } from "../closes/workspace";
import { AcceptFile } from "./AcceptFile";
import { AskClient } from "./AskClient";
import { AccountMap } from "./AccountMap";
import { ImportScope } from "./ImportScope";

// A client's books as files (docs/books-sources.md, Reading files): a file is sent for a month, the reader proposes how
// to read it, a person previews the reading, and a reviewer confirms the layout, which is proposed again for the next
// month's file. Nothing here is a month's books yet: accepting an import arrives with a later step.

type Meaning = "account_number" | "account_name" | "account_type" | "debit" | "credit" | "balance" | "opening" | "movement" | "year_to_date" | "dimension" | "date" | "reference" | "memo" | "party";
type Layout = {
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
  // date_order is how a ledger's dates in numbers read: year, month, day (ymd), month first (mdy) or day first (dmy).
  date_order?: string;
};
type Cell = { column: number; ref: string; kind: string; value: string; formula?: string };
type Row = { number: number; hidden?: boolean; cells: Cell[] };
type Table = { name: string; hidden?: boolean; rows: Row[]; more: number };
type Line = {
  row: number;
  ref: string;
  kind: "posting" | "total" | "heading";
  number?: string;
  name: string;
  balance: string;
  cents: boolean;
  dimension?: string;
  // A ledger line's date, transaction number, description and payee.
  date?: string;
  reference?: string;
  memo?: string;
  party?: string;
};
type Reading = { lines: Line[]; debits: string; credits: string; balanced: boolean; problems: string[]; notes: string[] };
// Status is a confirmed layout's outcome, as the list shows it; Confirmed adds the layout and what it read.
type Status = { id: string; reason: string; actor: string; at: string; balanced: boolean; problems: number };
// CSV is how a person chose to read a CSV file; empty, as the reader read it.
type CSV = { encoding: string; delimiter: string };
type Confirmed = Status & { layout: Layout; reading: Reading; csv?: CSV; line_count?: number; accounts?: number };
type ImportRow = { id: string; period: string; name: string; format: string; actor: string; at: string; replaces?: string; replaces_reason?: string; confirmed?: Status; accepted?: boolean };
type ImportRead = {
  import: Omit<ImportRow, "confirmed"> & { confirmed?: Confirmed };
  tables: Table[];
  encoding?: string;
  delimiter?: string;
  unsure?: string[];
  linked_workbooks?: string[];
  recalculate_on_open?: boolean;
  formulas?: number;
  // on says the firm's file imports are on: off, a file is read and downloaded but not confirmed again.
  on?: boolean;
  can_confirm: boolean;
  can_delete?: boolean;
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
  ["dimension", "Department, location or class"],
  // A column of dates makes the file a General Ledger: each row a line of the month's transactions.
  ["date", "Date (a ledger's line)"],
  ["reference", "Transaction number"],
  ["memo", "Description"],
  ["party", "Payee or customer"],
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
  const files = useQuery(workspaceQuery<{ imports: ImportRow[]; more: boolean; on?: boolean; can_confirm: boolean }>(filesPath(client)));
  // Older files come a hundred at a time after the newest hundred.
  const [older, setOlder] = useState<{ imports: ImportRow[]; more: boolean } | undefined>(undefined);
  const [sending, setSending] = useState(false);
  const [asking, setAsking] = useState(false);
  const [open, setOpen] = useState("");
  const [mapping, setMapping] = useState("");
  const [accepting, setAccepting] = useState("");
  const [scoping, setScoping] = useState("");
  const [all, setAll] = useState(false);
  const reread = () => (setOlder(undefined), void queryClient.invalidateQueries({ queryKey: ["workspace"] }));
  const imports = [...(files.data?.imports ?? []), ...(older?.imports ?? [])];
  const more = older ? older.more : !!files.data?.more;
  async function loadOlder() {
    const page = await workspaceRequest(`${filesPath(client)}&offset=${imports.length}`);
    setOlder({ imports: [...(older?.imports ?? []), ...page.imports], more: page.more });
  }
  const latest = imports[0];
  return (
    <>
      <Card
        icon="file"
        tone={latest && !(latest.confirmed && clean(latest.confirmed)) ? "wait" : "done"}
        title="Files"
        chip={<span className="pill neutral">{imports.length ? `${imports.length}${more ? "+" : ""} sent` : "None sent"}</span>}
        action={
          files.data?.on === false ? undefined : (
            <>
              {files.data?.can_confirm && (
                <button type="button" onClick={() => setAsking(true)}>
                  Ask the client
                </button>
              )}
              <button type="button" onClick={() => setSending(true)}>
                Send a file
              </button>
            </>
          )
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
                {i.accepted ? <span className="pill">Accepted</span> : i.confirmed ? clean(i.confirmed) ? <span className="pill">Balanced</span> : <span className="pill danger">Problems</span> : <span className="pill muted">Layout to confirm</span>}
              </li>
            ))}
            {imports.length > 6 && (
              <li>
                <button type="button" className="link" onClick={() => setAll(!all)}>
                  {all ? "Show the latest six" : `Show all ${imports.length}${more ? "+" : ""} files`}
                </button>
              </li>
            )}
            {(all || imports.length <= 6) && more && (
              <li>
                <button type="button" className="link" onClick={() => void loadOlder().then(() => setAll(true))}>
                  Show older files
                </button>
              </li>
            )}
          </ul>
        )}
      </Card>
      {asking && (
        <Dialog open onClose={() => setAsking(false)} title="Ask the client for a month's books" description="A private link through which the client sends the month's Trial Balance or General Ledger, as a CSV or Excel workbook of up to 5 MiB.">
          <AskForm client={client} onClose={() => setAsking(false)} />
        </Dialog>
      )}
      <Dialog open={sending} onClose={() => setSending(false)} title="Send a file" description="The client's Trial Balance for one month, as a CSV or Excel workbook (.xlsx), up to 50 MiB.">
        <SendForm client={client} imports={imports} onSent={(id) => (setSending(false), reread(), setOpen(id))} onCancel={() => setSending(false)} />
      </Dialog>
      {open && (
        <Dialog open onClose={() => (setOpen(""), reread())} title="Read the file" description="How the file is read: which row heads it, what each column holds, and how amounts are written.">
          <ImportReader
            client={client}
            id={open}
            onDone={() => (setOpen(""), reread())}
            onMap={() => (setOpen(""), setMapping(open))}
            onScope={() => (setOpen(""), setScoping(open))}
            onAccept={() => (setOpen(""), setAccepting(open))}
          />
        </Dialog>
      )}
      {mapping && (
        <Dialog open onClose={() => setMapping("")} title="Map the accounts" description="Each account's class and type, as the statements and checks read them. Next month's file finds its accounts mapped already.">
          <AccountMap client={client} id={mapping} onClose={() => setMapping("")} />
        </Dialog>
      )}
      {scoping && (
        <Dialog open onClose={() => setScoping("")} title="Scope and basis" description="What the file covers and what basis the export is on, attested of its confirmed reading.">
          <ImportScope client={client} id={scoping} onClose={() => setScoping("")} />
        </Dialog>
      )}
      {accepting && (
        <Dialog open onClose={() => (setAccepting(""), reread())} title="Checks and acceptance" description="What the checks find in the file's reading, and its acceptance as the client's books for the month.">
          <AcceptFile client={client} id={accepting} onClose={() => (setAccepting(""), reread())} />
        </Dialog>
      )}
    </>
  );
}

function SendForm({ client, imports, onSent, onCancel }: { client: string; imports: ImportRow[]; onSent: (id: string) => void; onCancel: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // A file sent for a month that has one already either replaces it, saying why, or is another file of the month.
  const [period, setPeriod] = useState("");
  const [replaces, setReplaces] = useState("");
  const earlier = imports.filter((i) => i.period === period);
  async function send(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const saved = await workspaceRequest(filesPath(client), new FormData(event.currentTarget));
      // The same file sent again for the month is the one already kept: it opens.
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
          <input type="month" name="period" required value={period} onChange={(e) => (setPeriod(e.target.value), setReplaces(""))} />
        </label>
        <label>
          File
          <input type="file" name="file" accept=".csv,.xlsx,text/csv,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" required />
        </label>
        {earlier.length > 0 && (
          <label>
            It replaces
            <select name="replaces" value={replaces} onChange={(e) => setReplaces(e.target.value)}>
              <option value="">Nothing: it is another file for the month</option>
              {earlier.map((i) => (
                <option key={i.id} value={i.id}>
                  {i.name}
                </option>
              ))}
            </select>
          </label>
        )}
        {replaces && (
          <label>
            Why it replaces it
            <input name="replaces_reason" maxLength={500} required />
          </label>
        )}
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

// AskForm asks the client for a month's books (docs/books-sources.md, Client-provided books): a request on the month's
// close. The file the client sends is released from the month's client attachments, and then appears among the files here.
function AskForm({ client, onClose }: { client: string; onClose: () => void }) {
  return (
    <AskClient
      path={`/client/books-requests?name=${encodeURIComponent(client)}`}
      fields={
        <label>
          Month
          <input type="month" name="period" required />
        </label>
      }
      values={(form) => ({ period: String(form.get("period")) })}
      after={(asked) => (
        <p className="note">
          Once the client sends the file and it passes scanning, release it under Client attachments on{" "}
          <Link to={`/dashboard/closes/${asked.close_id}`}>{monthName(asked.values.period)}</Link>'s Overview (The agent's plan, documents and accounts). It then appears among the files here.
        </p>
      )}
      onClose={onClose}
    />
  );
}

// ImportReader shows a file's first rows, the layout to read it with (proposed, or the one confirmed), a preview of the
// reading, and, for a reviewer, confirming the layout with a reason.
function ImportReader({
  client,
  id,
  onDone,
  onMap,
  onScope,
  onAccept,
}: {
  client: string;
  id: string;
  onDone: () => void;
  onMap: () => void;
  onScope: () => void;
  onAccept: () => void;
}) {
  // A CSV is read as the reader reads it until a person chooses another encoding or delimiter.
  const [csv, setCsv] = useState<CSV | undefined>(undefined);
  const read = useQuery({ ...workspaceQuery<ImportRead>(importPath(client, id, csv)), placeholderData: keepPreviousData });
  if (!read.data) return read.error ? <p role="alert">{read.error.message || "The file could not be read."}</p> : <p role="status">Reading the file…</p>;
  const start = read.data.import.confirmed?.layout ?? read.data.proposal?.layout;
  return (
    <>
      {read.error && <p role="alert">{read.error.message || "The file could not be read that way."}</p>}
      <LayoutForm
        key={`${read.data.import.id}:${read.data.encoding ?? ""}:${read.data.delimiter ?? ""}`}
        client={client}
        id={id}
        data={read.data}
        start={start}
        csv={csv}
        onCsv={setCsv}
        onDone={onDone}
        onMap={onMap}
        onScope={onScope}
        onAccept={onAccept}
      />
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
  onMap,
  onScope,
  onAccept,
}: {
  client: string;
  id: string;
  data: ImportRead;
  start?: Layout;
  csv?: CSV;
  onCsv: (csv: CSV) => void;
  onDone: () => void;
  onMap: () => void;
  onScope: () => void;
  onAccept: () => void;
}) {
  const [layout, setLayout] = useState<Layout>(
    start ?? { table: data.tables[0]?.name ?? "", header_row: 1, columns: {}, style: { decimal_comma: false, scale: 1 }, as_of: "", currency: "" },
  );
  // The confirmed reading shows only beside the parse it was confirmed with: a file read another way is previewed afresh.
  const confirmedCsv = data.import.confirmed?.csv;
  const sameParse = !csv || (csv.encoding === confirmedCsv?.encoding && csv.delimiter === confirmedCsv?.delimiter);
  const [reading, setReading] = useState<Reading | undefined>(sameParse ? data.import.confirmed?.reading : undefined);
  // The confirmed reading's lines are fetched in pages; a preview's come with it.
  const [previewed, setPreviewed] = useState(false);
  // A preview's counts: its lines come two hundred at a time, as the server sends them.
  const [counts, setCounts] = useState<{ lines: number; accounts: number }>({ lines: 0, accounts: 0 });
  const [deleting, setDeleting] = useState(false);
  const [deleteReason, setDeleteReason] = useState("");
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
  // The columns are every column the rows shown use, the header row's included, however far down it is.
  const width = Math.max(0, ...[...(first?.rows ?? []), ...(table?.rows ?? [])].flatMap((r) => r.cells.map((c) => c.column + 1)));
  const columns = Array.from({ length: width }, (_, i) => i);
  async function act(action: "preview" | "confirm") {
    setBusy(true);
    setError("");
    try {
      const out = await workspaceRequest(importPath(client, id), { action, layout, reason: reason.trim(), csv: csv ?? { encoding: "", delimiter: "" } });
      setReading(out.reading);
      setCounts({ lines: out.line_count ?? out.reading.lines.length, accounts: out.accounts ?? 0 });
      setPreviewed(true);
      if (action === "confirm") onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The file could not be read with this layout.");
    } finally {
      setBusy(false);
    }
  }
  const set = (change: Partial<Layout>) => (setLayout({ ...layout, ...change }), setReading(undefined));
  async function deleteFile() {
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(`/client/import/delete?name=${encodeURIComponent(client)}&id=${encodeURIComponent(id)}`, { reason: deleteReason.trim() });
      onDone();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The file could not be deleted.");
    } finally {
      setBusy(false);
    }
  }
  async function downloadFile() {
    try {
      const blob = await workspaceRequest(`/client/import/file?name=${encodeURIComponent(client)}&id=${encodeURIComponent(id)}`, undefined, true);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = data.import.name;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      setError(e instanceof Error ? e.message : "The file could not be downloaded.");
    }
  }
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
        {Object.values(layout.columns).includes("date") && (
          <label>
            Dates are written
            <select value={layout.date_order ?? ""} onChange={(e) => set({ date_order: e.target.value })}>
              <option value="">as they show (asked when they could read two ways)</option>
              <option value="mdy">month first (01/31/2026)</option>
              <option value="dmy">day first (31/01/2026)</option>
              <option value="ymd">year first (2026-01-31)</option>
            </select>
          </label>
        )}
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
            {previewed ? counts.accounts : data.import.confirmed?.line_count ? (data.import.confirmed.accounts ?? 0) : reading.lines.filter((l) => l.kind === "posting").length} accounts.
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
          {previewed || !data.import.confirmed?.line_count ? (
            <ReadingLines
              key={JSON.stringify(layout)}
              lines={[]}
              total={previewed ? counts.lines : reading.lines.length}
              page={async (offset) =>
                previewed && offset >= reading.lines.length
                  ? (await workspaceRequest(importPath(client, id), { action: "preview", layout, reason: "", csv: csv ?? { encoding: "", delimiter: "" }, lines_from: offset })).reading
                  : { lines: reading.lines.slice(offset, offset + 200) }
              }
            />
          ) : (
            <ReadingLines lines={[]} total={data.import.confirmed.line_count} page={(offset) => workspaceRequest(`/client/import/lines?name=${encodeURIComponent(client)}&id=${encodeURIComponent(id)}&offset=${offset}`)} />
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
      {data.on === false && <p className="note">File imports are switched off for your firm: this file can be read and downloaded, not read again with another layout.</p>}
      {deleting && (
        <label>
          Why delete it
          <input value={deleteReason} maxLength={500} onChange={(e) => setDeleteReason(e.target.value)} />
        </label>
      )}
      <DialogActions>
        {data.can_delete &&
          (deleting ? (
            <button type="button" className="danger" disabled={busy || !deleteReason.trim()} onClick={() => void deleteFile()}>
              Delete it
            </button>
          ) : (
            <button type="button" className="danger" onClick={() => setDeleting(true)}>
              Delete the file
            </button>
          ))}
        <button type="button" onClick={() => void downloadFile()}>
          Download the file
        </button>
        {data.import.confirmed && (
          <button type="button" onClick={onMap}>
            Map the accounts
          </button>
        )}
        {data.import.confirmed && (
          <button type="button" onClick={onScope}>
            Scope and basis
          </button>
        )}
        {data.import.confirmed && (
          <button type="button" onClick={onAccept}>
            Checks and acceptance
          </button>
        )}
        {data.on !== false && (
          <button type="button" disabled={busy} onClick={() => void act("preview")}>
            {busy ? "Reading…" : "Preview the reading"}
          </button>
        )}
        {data.can_confirm && (
          <button type="button" className="primary" disabled={busy || !reading || !reason.trim() || !layout.as_of || !layout.currency} onClick={() => void act("confirm")}>
            Confirm the layout
          </button>
        )}
      </DialogActions>
    </div>
  );
}

// ReadingLines shows how each row was read, once opened, two hundred rows at a time: a file can hold thousands. The
// lines are given, or fetched a page at a time from page, total in all.
function ReadingLines({ lines, total, page }: { lines: Line[]; total?: number; page?: (offset: number) => Promise<{ lines: Line[] }> }) {
  const [shown, setShown] = useState(0);
  const [fetched, setFetched] = useState<Line[]>([]);
  const [error, setError] = useState("");
  const all = page ? fetched : lines;
  const count = total ?? lines.length;
  async function more() {
    const next = shown + 200;
    if (page && fetched.length < Math.min(next, count)) {
      try {
        const got = await page(fetched.length);
        setFetched([...fetched, ...got.lines]);
      } catch (e) {
        setError(e instanceof Error ? e.message : "The rows could not be read.");
        return;
      }
    }
    setShown(next);
  }
  return (
    <details onToggle={(e) => (e.currentTarget as HTMLDetailsElement).open && shown === 0 && void more()}>
      <summary>How each of the {count} rows was read</summary>
      {error && <p role="alert">{error}</p>}
      {shown > 0 && (
        <>
          <table className="ks-preview">
            <tbody>
              {all.slice(0, shown).map((l) => (
                <tr key={l.row}>
                  <th>{l.row}</th>
                  <td>{lineKinds[l.kind]}</td>
                  <td>
                    {[l.number, l.name].filter(Boolean).join(" ")}
                    {l.dimension ? ` (${l.dimension})` : ""}
                    {(l.date || l.reference || l.party || l.memo) && (
                      <>
                        <br />
                        {[l.date, l.reference && `#${l.reference}`, l.party, l.memo].filter(Boolean).join(" · ")}
                      </>
                    )}
                  </td>
                  <td>{l.kind === "heading" ? "" : l.balance}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {shown < count && (
            <button type="button" className="link" onClick={() => void more()}>
              Show {Math.min(200, count - shown)} more
            </button>
          )}
        </>
      )}
    </details>
  );
}
