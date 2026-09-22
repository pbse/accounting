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
export function CarryForward({ closeId }: { closeId: string }) {
  const [preview, setPreview] = useState<{
    period: string;
    close_id: string;
    items: Item[];
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
    setBusy(true);
    setError("");
    try {
      const result = await workspaceRequest(`/${closeId}/carry-forward`, {
        confirmed: data.has("confirmed"),
        items: preview!.items
          .filter((i) => ids.includes(i.id))
          .map(({ id, digest, review_id }) => ({ id, digest, review_id })),
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
    <details>
      <summary>Prepare next month from reviewed schedules</summary>
      <p>
        Carry current approved prepaid schedules and their evidence into the
        following month. New drafts need fresh review. Statements, approvals,
        client requests and journal entries are not rolled forward.
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
                    defaultChecked={item.eligible}
                  />{" "}
                  Draft #{item.id} · {item.source} · opening scheduled balance $
                  {item.balance}
                </label>
                {!item.eligible && <p>{item.reason}</p>}
              </div>
            ))}
            {preview.items.some((i) => i.eligible) && (
              <>
                <label>
                  <input name="confirmed" type="checkbox" required /> I reviewed
                  this selection. Prepare schedules only, without assuming
                  ledger balances, prior postings or approval for the new month.
                </label>
                <p>
                  Select up to 20 schedules. Existing next-month work is
                  preserved. Repeating the same selection reuses its jobs.
                </p>
                <button>
                  {busy
                    ? "Queuing schedules…"
                    : "Prepare selected schedules for next month"}
                </button>
              </>
            )}
          </fieldset>
        </form>
      )}
    </details>
  );
}
