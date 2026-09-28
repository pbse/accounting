import { useEffect, useState, type FormEvent } from "react";
import { workspaceRequest } from "./workspace";

type Label = { exports: string[]; current: boolean; exported_on: string; complete: boolean; final_adjustments: boolean; note: string; actor: string; created_at: string };
type File = { id: string; name: string; purpose?: string; superseded_by: string | null };

// Uploaded exports stand in for a live ledger only as a person labels them: when exported, whether they cover the month,
// and whether they already hold the final close adjustments.
export function HistoricalExports({ closeId }: { closeId: string }) {
  const [label, setLabel] = useState<Label | null>(null);
  const [files, setFiles] = useState<File[]>([]);
  const [chosen, setChosen] = useState<string[]>([]);
  const [exportedOn, setExportedOn] = useState("");
  const [complete, setComplete] = useState(true);
  const [adjusted, setAdjusted] = useState(false);
  const [note, setNote] = useState("");
  const [message, setMessage] = useState("");
  // Read again whenever the panel opens, so a document uploaded or replaced since shows here.
  const load = () =>
    Promise.all([workspaceRequest(`/${closeId}/historical-exports`), workspaceRequest(`/${closeId}/evidence`)])
      .then(([data, evidence]) => {
        const current = (evidence as File[]).filter((f) => !f.superseded_by && f.purpose !== "procedure");
        setLabel(data.label);
        setFiles(current);
        setChosen((ids) => ids.filter((id) => current.some((f) => f.id === id)));
      })
      .catch((e) => setMessage(e instanceof Error ? e.message : "Historical exports are unavailable."));
  useEffect(() => {
    void load();
  }, [closeId]);
  async function save(event: FormEvent) {
    event.preventDefault();
    setMessage("");
    try {
      const data = await workspaceRequest(`/${closeId}/historical-exports`, { evidence_ids: chosen, exported_on: exportedOn, complete, final_adjustments: adjusted, note });
      setLabel(data.label);
      setMessage("Saved.");
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "The label could not be saved.");
    }
  }
  return (
    <details id="historical-exports" onToggle={(e) => e.currentTarget.open && void load()}>
      <summary>Historical exports</summary>
      <p>
        Without a live QuickBooks or Xero connection, label the uploaded ledger exports this close uses. The label is what you report; it is shown as reported, never as a
        verified ledger, and changing it after sign-off needs a new sign-off.
      </p>
      {label && (
        <p role="status">
          {label.actor} labeled {label.exports.join(", ")}: exported {label.exported_on}, {label.complete ? "complete for the month" : "partial"},{" "}
          {label.final_adjustments ? "already including" : "not including"} the final close adjustments. {label.note}
          {!label.current && " A labeled export was replaced; label the current exports again."}
        </p>
      )}
      <form onSubmit={save}>
        <fieldset>
          <legend>Exports</legend>
          {files.length === 0 && <p className="empty">Upload the exports first.</p>}
          {files.map((f) => (
            <label key={f.id}>
              <input type="checkbox" checked={chosen.includes(f.id)} onChange={(e) => setChosen(e.target.checked ? [...chosen, f.id] : chosen.filter((id) => id !== f.id))} /> {f.name}
            </label>
          ))}
        </fieldset>
        <label>
          Exported on
          <input type="date" required value={exportedOn} onChange={(e) => setExportedOn(e.target.value)} />
        </label>
        <label>
          <input type="checkbox" checked={complete} onChange={(e) => setComplete(e.target.checked)} /> They cover the whole month
        </label>
        <label>
          <input type="checkbox" checked={adjusted} onChange={(e) => setAdjusted(e.target.checked)} /> They already include the final close adjustments
        </label>
        <label>
          Note
          <input value={note} minLength={5} maxLength={500} onChange={(e) => setNote(e.target.value)} />
        </label>
        <button disabled={chosen.length === 0 || chosen.length > 20 || !exportedOn || note.trim().length < 5}>Save label</button>
        {message && <p role="status">{message}</p>}
      </form>
    </details>
  );
}
