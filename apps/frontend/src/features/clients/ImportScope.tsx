import { useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { DialogActions } from "@close/ui/components/Dialog";
import { workspaceQuery } from "../../data/queries";
import { useDates } from "../../data/time";
import { workspaceRequest } from "../closes/workspace";

// A file's scope and basis (docs/books-sources.md, How an upload becomes the month's books, steps 3 and 5): a reviewer
// attests what the confirmed reading covers and what basis the export is on. The client's approved basis procedure stays
// the authority; a file on another basis is said to hold what is built on it.

type Basis = "accrual" | "cash" | "modified_cash";
type Scope = {
  id: string;
  layout_id: string;
  book: string;
  filters: string;
  complete: boolean;
  missing: string;
  basis: Basis;
  recalculated: boolean;
  reason: string;
  actor: string;
  at: string;
};
type ScopeRead = {
  scope: Scope | null;
  confirmed: boolean;
  formulas: number;
  current: boolean;
  approved_basis: { method: Basis; scope: "client" | "firm" } | null;
  note: string;
  on: boolean;
  can_confirm: boolean;
};

const basisLabels: Record<Basis, string> = { accrual: "Accrual", cash: "Cash", modified_cash: "Modified cash" };
const scopePath = (client: string, id: string) => `/client/import/scope?name=${encodeURIComponent(client)}&id=${encodeURIComponent(id)}`;

export function ImportScope({ client, id, onClose }: { client: string; id: string; onClose: () => void }) {
  const read = useQuery(workspaceQuery<ScopeRead>(scopePath(client, id)));
  if (!read.data) return read.error ? <p role="alert">{read.error.message || "The file's scope could not be read."}</p> : <p role="status">Reading the file's scope…</p>;
  return <ScopeForm key={read.data.scope?.id ?? "none"} client={client} id={id} data={read.data} onClose={onClose} />;
}

function ScopeForm({ client, id, data, onClose }: { client: string; id: string; data: ScopeRead; onClose: () => void }) {
  const queryClient = useQueryClient();
  const dates = useDates();
  const was = data.scope;
  const [book, setBook] = useState(was?.book ?? "");
  const [filters, setFilters] = useState(was?.filters ?? "");
  const [complete, setComplete] = useState(was?.complete ?? true);
  const [missing, setMissing] = useState(was?.missing ?? "");
  const [basis, setBasis] = useState<Basis | "">(was?.basis ?? "");
  const [recalculated, setRecalculated] = useState(was?.recalculated ?? false);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(scopePath(client, id), { book, filters, complete, missing, basis, recalculated: data.formulas > 0 && recalculated, reason: reason.trim() });
      await queryClient.invalidateQueries({ queryKey: ["workspace"] });
    } catch (err) {
      setError(err instanceof Error ? err.message : "The scope could not be attested.");
    } finally {
      setBusy(false);
    }
  }
  const approved = data.approved_basis;
  return (
    <form className="ks-import" onSubmit={(e) => void submit(e)}>
      {!data.confirmed ? (
        <p className="note">Confirm how the file is read first: what it covers is attested of that reading.</p>
      ) : was ? (
        <p>
          Attested {dates.dayMonth(was.at)} by {was.actor}: {was.book || "the books"}
          {was.filters ? `, filtered to ${was.filters}` : ", unfiltered"}, {was.complete ? "the client's complete books for the month" : `leaving out ${was.missing}`}, {was.basis === "accrual" ? "on an accrual basis" : `on a ${basisLabels[was.basis].toLowerCase()} basis`}
          {was.recalculated ? ", a recalculated copy." : "."}{" "}
          {!data.current && <strong>That was of an earlier reading: attest it again for this one.</strong>}
        </p>
      ) : (
        <p>Nothing is attested of this file yet.</p>
      )}
      <p>
        {approved
          ? `The client's approved basis is ${basisLabels[approved.method].toLowerCase()} (${approved.scope === "client" ? "the client's own procedure" : "the firm's"}).`
          : "No basis is approved for the client yet."}
      </p>
      {data.note && <p role="alert">{data.note}</p>}
      {data.can_confirm && (
        <>
          <label>
            Book exported from
            <input value={book} maxLength={200} placeholder="General ledger" onChange={(e) => setBook(e.target.value)} />
          </label>
          <label>
            Filters the export applied
            <input value={filters} maxLength={500} placeholder="None" onChange={(e) => setFilters(e.target.value)} />
          </label>
          <label className="checkbox-label">
            <input type="checkbox" checked={complete} onChange={(e) => setComplete(e.target.checked)} /> This is the client's complete books for the month: every account, department and journal
          </label>
          {!complete && (
            <label>
              What it leaves out
              <input value={missing} maxLength={500} onChange={(e) => setMissing(e.target.value)} />
            </label>
          )}
          {data.formulas > 0 && (
            <label className="checkbox-label">
              <input type="checkbox" checked={recalculated} onChange={(e) => setRecalculated(e.target.checked)} /> {data.formulas} month-end amount{data.formulas === 1 ? " is a formula's" : "s are formulas'"}{" "}
              stored result. This is a copy recalculated in a spreadsheet application after every linked workbook was refreshed from its confirmed version, its input cells matching the original's
            </label>
          )}
          <label>
            Basis the export is on
            <select value={basis} onChange={(e) => setBasis(e.target.value as Basis)}>
              {!basis && <option value="">Choose</option>}
              {(Object.keys(basisLabels) as Basis[]).map((b) => (
                <option key={b} value={b}>
                  {basisLabels[b]}
                </option>
              ))}
            </select>
          </label>
          <label>
            Reason
            <input value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
          </label>
        </>
      )}
      {!data.on && <p className="note">File imports are switched off for your firm: what was attested can be read, not changed.</p>}
      {error && <p role="alert">{error}</p>}
      <DialogActions>
        <button type="button" onClick={onClose}>
          Close
        </button>
        {data.can_confirm && (
          <button className="primary" disabled={busy || !basis || !reason.trim() || (!complete && !missing.trim())}>
            {busy ? "Attesting…" : "Attest"}
          </button>
        )}
      </DialogActions>
    </form>
  );
}
