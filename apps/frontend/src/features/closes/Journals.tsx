import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { workspaceQuery } from "../../data/queries";
import { useDates } from "../../data/time";
import { workspaceRequest } from "./workspace";

// Writing back by file (docs/books-sources.md, Writing back): a month whose books are its files has no ledger to post
// to, so its approved entries download as a journal batch for the client's ledger, each journal's reference in its memo;
// a person records each as entered with the ledger's own reference.

type Line = { date: string; account: string; debit?: string; credit?: string; memo: string };
type Pending = { paper_id: string; kind: string; source: string; lines: Line[]; held?: string };
type Journal = { id: string; batch: number; reference: string; paper_id: string; state: "exported" | "entered" | "verified" | "uncertain"; ledger_reference: string; lines: Line[]; exported_by: string; exported_at: string; entered_by?: string; entered_at?: string; note?: string };
type JournalsRead = { pending: Pending[]; journals: Journal[]; ledger: string; journal_import: string; can_export: boolean; can_enter: boolean };

const states = { exported: ["pill muted", "Exported"], entered: ["pill neutral", "Entered"], verified: ["pill", "Verified"], uncertain: ["pill danger", "Uncertain"] } as const;

export function Journals({ closeId }: { closeId: string }) {
  const queryClient = useQueryClient();
  const dates = useDates();
  const path = `/${closeId}/journals`;
  const read = useQuery(workspaceQuery<JournalsRead>(path));
  const [references, setReferences] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  async function act(key: string, body: object, then?: (out: { csv?: string; name?: string }) => void) {
    setBusy(key);
    setError("");
    try {
      const out = await workspaceRequest(path, { ...body, confirmed: true });
      then?.(out);
      await queryClient.invalidateQueries({ queryKey: ["workspace"] });
    } catch (e) {
      setError(e instanceof Error ? e.message : "The journals could not be saved.");
    } finally {
      setBusy("");
    }
  }
  // The batch is downloaded as the server wrote it: the file the ledger's journal import reads.
  function save(blob: Blob, name: string) {
    const link = document.createElement("a");
    link.href = URL.createObjectURL(blob);
    link.download = name;
    link.click();
    URL.revokeObjectURL(link.href);
  }
  function download(out: { csv?: string; name?: string }) {
    if (out.csv) save(new Blob([out.csv], { type: "text/csv" }), out.name ?? "journals.csv");
  }
  // A batch already exported, again, if its file was lost.
  async function again(batch: number) {
    setError("");
    try {
      save(await workspaceRequest(`${path}/batches/${batch}`, undefined, true), `journals, batch ${batch}.csv`);
    } catch (e) {
      setError(e instanceof Error ? e.message : "The batch is unavailable.");
    }
  }
  if (!read.data) return read.error ? <p className="note">{read.error.message}</p> : <p role="status">Reading the journals…</p>;
  const { pending, journals, ledger, journal_import, can_export, can_enter } = read.data;
  const ready = pending.filter((p) => !p.held);
  const batches = [...new Set(journals.map((j) => j.batch))];
  return (
    <section aria-label="Journals to enter in the ledger">
      <p>
        This month's books are its files, so its approved entries are entered in the client's ledger by a journal import
        {ledger ? ` into ${ledger}${journal_import ? `, through its ${journal_import}` : ""}` : ""}. Each journal's reference is in its memo, so a duplicate or
        partial import can be seen. An entered journal is verified once the month's General Ledger, exported after it was
        entered, is accepted and holds it; until then the month is not signed off.
      </p>
      {error && <p role="alert">{error}</p>}
      <h3>Approved, not yet exported</h3>
      {pending.length === 0 ? (
        <p className="empty">No approved entry is waiting to be exported.</p>
      ) : (
        <ul>
          {pending.map((p) => (
            <li key={p.paper_id}>
              Draft #{p.paper_id} ({p.kind}
              {p.source ? `, ${p.source}` : ""}): {p.lines.map((l) => `${l.account} ${l.debit ? `Dr ${l.debit}` : `Cr ${l.credit}`}`).join("; ")}
              {p.held && <strong> Held: {p.held}</strong>}
            </li>
          ))}
        </ul>
      )}
      {can_export && ready.length > 0 && (
        <button type="button" className="primary" disabled={!!busy} onClick={() => void act("export", { action: "export" }, download)}>
          {busy === "export" ? "Exporting…" : `Download a batch of ${ready.length} journal${ready.length === 1 ? "" : "s"}`}
        </button>
      )}
      <h3>Exported</h3>
      {journals.length === 0 ? (
        <p className="empty">No journal is exported yet.</p>
      ) : (
        <ul>
          {journals.map((j) => (
            <li key={j.id}>
              <span className={states[j.state][0]}>{states[j.state][1]}</span> {j.reference} (batch {j.batch}, draft #{j.paper_id}), exported {dates.dayMonth(j.exported_at)} by {j.exported_by}
              {j.state !== "exported" && j.ledger_reference ? `; entered as ${j.ledger_reference}${j.entered_at ? ` ${dates.dayMonth(j.entered_at)}` : ""}${j.entered_by ? ` by ${j.entered_by}` : ""}` : ""}
              {j.note && <small> {j.note}</small>}
              {can_enter && j.state === "exported" && (
                <span>
                  {" "}
                  <input
                    aria-label={`The ledger's reference for ${j.reference}`}
                    placeholder="Ledger's journal reference"
                    maxLength={120}
                    value={references[j.id] ?? ""}
                    onChange={(e) => setReferences({ ...references, [j.id]: e.target.value })}
                  />
                  <button type="button" disabled={!!busy || !(references[j.id] ?? "").trim()} onClick={() => void act(j.id, { action: "entered", journal: j.id, ledger_reference: references[j.id] })}>
                    Recorded as entered
                  </button>
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
      {batches.map((batch) => (
        <button key={batch} type="button" onClick={() => void again(batch)}>
          Download batch {batch} again
        </button>
      ))}
    </section>
  );
}
