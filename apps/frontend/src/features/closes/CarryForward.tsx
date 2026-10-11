import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router";
import { workspaceRequest } from "./workspace";

type Item = {
  id: string;
  digest: string;
  review_id: string;
  source: string;
  balance: string;
  eligible: boolean;
  reason: string;
  job_id: string;
};
export function CarryForward({ closeId, open }: { closeId: string; open?: boolean }) {
  const [preview, setPreview] = useState<{
    period: string;
    close_id: string;
    items: Item[];
    procedures: { id:string; kind:string; enabled:boolean; reason:string; actor:string }[];
    unresolved: { kind:string; id:string; state:string }[];
    unresolved_more: boolean;
    unresolved_digest: string;
  }>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const navigate = useNavigate();
  async function load() {
    setBusy(true);
    setError("");
    setPreview(undefined);
    try {
      setPreview(await workspaceRequest(`/${closeId}/carry-forward`));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Could not inspect schedules.");
    } finally {
      setBusy(false);
    }
  }
  async function prepare(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const ids = data.getAll("schedule").map(String);
    const procedureIds = data.getAll("procedure").map(String);
    const carryUnresolved = data.has("carry_unresolved");
    if ((ids.length === 0 && procedureIds.length === 0 && !carryUnresolved) || (ids.length > 0 && procedureIds.length > 0)) {
      setError("Choose schedules, procedures or unresolved follow-up. Select schedules and procedures in separate actions.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const result = await workspaceRequest(`/${closeId}/carry-forward`, {
        confirmed: data.has("confirmed"),
        items: preview!.items
          .filter((i) => ids.includes(i.id))
          .map(({ id, digest, review_id }) => ({ id, digest, review_id })),
        procedure_ids: procedureIds,
        carry_unresolved: carryUnresolved,
        unresolved_digest: carryUnresolved ? preview!.unresolved_digest : "",
      });
      navigate(`/dashboard/closes/${result.close_id}`);
    } catch (e) {
      setError(
        e instanceof Error ? e.message : "Could not carry schedules forward.",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <details open={open}>
      <summary>Carry work into the next month</summary>
      <p>
        Carry current approved prepaid schedules or explicitly selected current
        firm procedures into the following month, one set at a time. You can also
        carry unresolved items as incomplete follow-up. New drafts need fresh review.
        Statements, approvals, client requests and journal entries are not rolled forward.
      </p>
      <button disabled={busy} onClick={() => void load()}>
        Preview next month
      </button>
      {error && <p role="alert">{error}</p>}
      {preview && (
        <form onSubmit={prepare}>
          <fieldset disabled={busy}>
            <legend>Prepaid schedules for {preview.period}</legend>
            {preview.close_id && (
              <p>
                <Link to={`/dashboard/closes/${preview.close_id}`}>
                  Open the existing {preview.period} close
                </Link>
              </p>
            )}
            {preview.items.length === 0 && (
              <p>No prepaid schedules in this close.</p>
            )}
            {preview.items.map((item) => (
              <div key={item.id}>
                <label>
                  <input
                    type="checkbox"
                    name="schedule"
                    value={item.id}
                    disabled={!item.eligible}
                  />{" "}
                  Draft #{item.id} · {item.source} · opening scheduled balance $
                  {item.balance}
                </label>
                {!item.eligible && <p>{item.reason}</p>}
              </div>
            ))}
            {preview.procedures?.length>0 && <><h3>Current approved procedures</h3><p>Applying a newer procedure to an existing next-month close can invalidate work that used its prior version. Review that close before selecting it.</p>{preview.procedures.map(p=><label key={p.id}><input name="procedure" type="checkbox" value={p.id} disabled={!p.enabled} /> {p.kind} · version #{p.id} · {p.reason}{!p.enabled?" (disabled)":""}</label>)}</>}
            {preview.unresolved?.length>0 && <><h3>Unresolved prior work</h3><ul>{preview.unresolved.map(item=><li key={`${item.kind}-${item.id}`}>{item.kind} #{item.id} · {item.state.replaceAll("_"," ")}</li>)}</ul>{preview.unresolved_more ? <p role="alert">More than 20 items are unresolved. Resolve or narrow the prior scope before carrying all follow-up work.</p> : <label><input name="carry_unresolved" type="checkbox" /> Add these as incomplete follow-up steps in the next close. They need fresh evidence and do not resume prior-period jobs automatically.</label>}</>}
            {(preview.items.some((i) => i.eligible) || preview.procedures?.some(p=>p.enabled) || (preview.unresolved?.length>0 && !preview.unresolved_more)) && (
              <>
                <label>
                  <input name="confirmed" type="checkbox" required /> I reviewed
                  this selection. Prepare schedules, apply selected procedures, or carry unresolved follow-up
                  without assuming ledger balances, prior postings or approval for the new month.
                </label>
                <p>
                  Select up to 20 schedules or three procedures, one set per action. Existing next-month work is
                  preserved. Repeating the same selection reuses its jobs.
                </p>
                <button>
                  {busy
                    ? "Carrying selected work…"
                    : "Carry selected work into next month"}
                </button>
              </>
            )}
          </fieldset>
        </form>
      )}
    </details>
  );
}
