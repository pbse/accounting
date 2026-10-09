import { workspaceQuery } from "../../data/queries";
import { useQuery } from "@tanstack/react-query";
import { useDates } from "../../data/time";
import { useState } from "react";
import { workspaceRequest } from "./workspace";
import { RowList } from "@close/ui/components/RowList";
import { SectionHeader } from "@close/ui/components/SectionHeader";

type Item = { job_id: string; source_name: string; confirmed_kind: string; replay_kind?: string; kept: boolean; changed_fields: string[]; error?: string };

export function Replay({ closeId }: { closeId: string }) {
  const dates = useDates();
  // The last saved run, read with the page; a run made here replaces it on screen.
  const saved = useQuery(workspaceQuery<{ run: { model: string; reasoning_effort?: string | null; items: Item[]; actor?: string; created_at?: string } | null }>(`/${closeId}/replay`)).data?.run ?? undefined;
  const [ran, setResult] = useState<{ model: string; reasoning_effort?: string | null; items: Item[]; actor?: string; created_at?: string }>();
  const result = ran ?? saved;
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function run() {
    setBusy(true);
    setError("");
    try {
      setResult(await workspaceRequest(`/${closeId}/replay`, {}));
    } catch (e) {
      setError(e instanceof Error ? e.message : "Replay failed. Nothing was saved.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section aria-label="Check the current agent">
      <SectionHeader title="Check the current agent">
        <button type="button" disabled={busy} onClick={() => void run()}>
          {busy ? "Replaying…" : "Replay confirmed readings"}
        </button>
      </SectionHeader>
      <p className="note">
        Reads up to 3 of this close's most recently confirmed documents again with the current model (up to 3 model calls), on the evidence the
        agent originally selected, and compares each reading with what was confirmed. Only the comparison is saved; no reading or workpaper changes.
        The check runs again once for each new model setting.
      </p>
      {error && <p role="alert">{error}</p>}
      {result?.created_at && (
        <p className="note">
          Saved {dates.dateTime(result.created_at)} · {result.actor === "scheduled-replay" ? "checked automatically for this model and effort" : `run by ${result.actor}`}
        </p>
      )}
      {result && (
        <RowList
          columns={[
            "Document",
            <>Agent ({result.model}{result.reasoning_effort ? `, ${result.reasoning_effort}` : ""})</>,
            "",
            "",
          ]}
        >
          {result.items.length === 0 && <p className="empty">No model readings have been confirmed in this close yet.</p>}
          {result.items.map((item) => (
            <div className="row" key={item.job_id}>
              <strong>{item.source_name}</strong>
              <span>
                {item.error ? (
                  <span className="status info">{item.error}</span>
                ) : item.kept ? (
                  <span className="status ok">Same as confirmed</span>
                ) : (
                  <span className="status warn">
                    Differs: {item.changed_fields.map((f) => f.replaceAll("_", " ")).join(", ")}
                    {item.replay_kind && item.replay_kind !== item.confirmed_kind ? ` (read as ${item.replay_kind}, confirmed as ${item.confirmed_kind})` : ""}
                  </span>
                )}
              </span>
              <span />
              <span />
            </div>
          ))}
        </RowList>
      )}
    </section>
  );
}
