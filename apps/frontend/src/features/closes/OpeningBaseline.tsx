import { workspaceQuery } from "../../data/queries";
import { useQuery } from "@tanstack/react-query";
import { useDates } from "../../data/time";
import { type FormEvent, useState } from "react";
import { workspaceRequest } from "./workspace";
import { SectionHeader } from "@close/ui/components/SectionHeader";

type Item = { key: string; label: string };
type Baseline = {
  id: string;
  digest: string;
  items: Item[];
  notes: Record<string, string>;
  reviewed_by: string;
  reviewed_at: string;
  resolutions: Record<string, { reference: string; resolved_by: string; resolved_at: string }>;
};
type View = { takeover: boolean; refusal?: string; digest?: string; items?: Item[]; baselines?: Baseline[] };

// A client's first close in the firm is its opening month: the reviewer notes each account that does not tie, which
// becomes inherited cleanup that later months wait for until it is resolved with a reference.
export function OpeningBaseline({ closeId }: { closeId: string }) {
  const dates = useDates();
  const read = useQuery(workspaceQuery<View>(`/${closeId}/opening-baseline`));
  const view = read.data;
  const [actionError, setError] = useState("");
  const error = actionError || (read.error ? read.error.message || "The opening baseline is unavailable." : "");
  const [saved, setSaved] = useState("");
  const [busy, setBusy] = useState(false);

  async function send(path: string, body: Record<string, unknown>, done: string) {
    setBusy(true);
    setError("");
    setSaved("");
    try {
      await workspaceRequest(`/${closeId}/opening-baseline${path}`, body);
      setSaved(done);
      void read.refetch();
    } catch (e) {
      setError(e instanceof Error ? e.message : "That could not be saved.");
    } finally {
      setBusy(false);
    }
  }
  if (!view) return error ? <p role="alert">{error}</p> : null;
  if (!view.takeover) return null;
  const baselines = view.baselines ?? [];
  const current = baselines[0]?.digest === view.digest;
  const review = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const notes = Object.fromEntries((view.items ?? []).map((item) => [item.key, String(form.get(item.key) ?? "").trim()]));
    void send("", { digest: view.digest, notes, confirmed: form.has("confirmed") }, "Reviewed the opening baseline.");
  };
  const open = baselines.flatMap((b) => Object.keys(b.notes).filter((key) => !b.resolutions[key]).map((key) => ({ baseline: b, key, label: b.items.find((i) => i.key === key)?.label ?? key })));
  return (
    <section aria-label="Opening baseline">
      <SectionHeader title="Opening baseline" />
      <p className="note">
        This is the client's first close in the firm, so its month-end balances are the starting point. Each account that does not tie needs a note; it becomes inherited
        cleanup. This month can then be signed off, and later months wait until each item is resolved with a reference.
      </p>
      {error && <p role="alert">{error}</p>}
      {saved && <p role="status">{saved}</p>}
      {view.refusal ? (
        <p className="empty">{view.refusal}</p>
      ) : current ? (
        <p role="status">Reviewed by {baselines[0].reviewed_by} on {dates.dateTime(baselines[0].reviewed_at)} for the current balances.</p>
      ) : (
        <form onSubmit={review}>
          <fieldset disabled={busy}>
            {baselines.length > 0 && <p role="status">The balances or their support changed since the last review; review them again.</p>}
            {(view.items ?? []).length === 0 && <p>Every balance-sheet account ties to its support.</p>}
            {(view.items ?? []).map((item) => (
              <label key={item.key}>
                {item.label}
                <textarea name={item.key} required minLength={5} maxLength={2000} placeholder="How it will be cleaned up" />
              </label>
            ))}
            <label>
              <input type="checkbox" name="confirmed" required /> These balances are the client's starting point.
            </label>
            <button className="primary">Review the opening baseline</button>
          </fieldset>
        </form>
      )}
      {open.length > 0 && (
        <>
          <h3>Inherited cleanup</h3>
          <ul>
            {open.map(({ baseline, key, label }) => (
              <li key={`${baseline.id}-${key}`}>
                {label}. Note: {baseline.notes[key]}
                <form onSubmit={(e) => (e.preventDefault(), void send("/resolve", { baseline_id: baseline.id, item: key, reference: String(new FormData(e.currentTarget).get("reference") ?? "").trim() }, "Resolved."))}>
                  <fieldset disabled={busy}>
                    <label>
                      How it was resolved
                      <input name="reference" required minLength={5} maxLength={2000} placeholder="Correcting entry JE-104 posted" />
                    </label>
                    <button>Resolve</button>
                  </fieldset>
                </form>
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}
