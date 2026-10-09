import { useDates } from "../../data/time";
import { useEffect, useState } from "react";
import { workspaceRequest } from "./workspace";

export type Activity = "prepare" | "review" | "evidence" | "requests" | "signoff" | "configuration";
const labels: Record<Activity, string> = {
  prepare: "Preparation",
  review: "Review",
  evidence: "Evidence",
  requests: "Client requests",
  signoff: "Sign-off",
  configuration: "Configuration",
};
// Recorded time may also be our own team's setup or support help, which is reported apart.
type EntryActivity = Activity | "assistance";
const entryLabels: Record<EntryActivity, string> = { ...labels, assistance: "Our team's help (setup or support)" };

// Credits 30 seconds only when the tab is visible and someone interacted in the last two minutes.
export function EffortTracker({ closeId, activity }: { closeId: string; activity: Activity }) {
  useEffect(() => {
    let last = 0;
    const seen = () => {
      last = Date.now();
    };
    const events = ["keydown", "pointerdown", "wheel", "scroll"] as const;
    events.forEach((name) => window.addEventListener(name, seen, { passive: true }));
    const timer = setInterval(() => {
      if (document.visibilityState === "visible" && Date.now() - last < 120_000)
        void workspaceRequest(`/${closeId}/effort`, { activity, seconds: 30 }, true)
          .then(() => window.dispatchEvent(new Event("effort-recorded")))
          .catch(() => undefined);
    }, 30_000);
    return () => {
      clearInterval(timer);
      events.forEach((name) => window.removeEventListener(name, seen));
    };
  }, [closeId, activity]);
  return null;
}

type Summary = {
  tracked_seconds: number;
  tracked_by_activity: Partial<Record<Activity, number>>;
  recorded_minutes: number;
  entries: { activity: EntryActivity; minutes: number; note: string; created_at: string; mine: boolean }[];
  answered_wait_seconds: number;
  open_requests: number;
  tracking_since?: string;
  oldest_open_since?: string;
};

export const minutes = (value: number) => (value < 1 ? "< 1 min" : `${Math.round(value)} min`);
const hours = (seconds: number) => (seconds < 3600 ? minutes(seconds / 60) : `${(seconds / 3600).toFixed(1)} h`);

export function EffortCard({ closeId }: { closeId: string }) {
  const dates = useDates();
  const [summary, setSummary] = useState<Summary>();
  const [error, setError] = useState("");
  const [form, setForm] = useState({ activity: "prepare" as EntryActivity, minutes: "", note: "" });
  const [busy, setBusy] = useState(false);
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    const recorded = () => setRefresh((n) => n + 1);
    window.addEventListener("effort-recorded", recorded);
    return () => window.removeEventListener("effort-recorded", recorded);
  }, []);
  useEffect(() => {
    let live = true;
    workspaceRequest(`/${closeId}/effort`)
      .then((data) => live && setSummary(data))
      .catch((e) => live && setError(e instanceof Error ? e.message : "Effort is unavailable."));
    return () => {
      live = false;
    };
  }, [closeId, refresh]);
  async function record() {
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(`/${closeId}/effort/entries`, { activity: form.activity, minutes: Number(form.minutes), note: form.note });
      setForm({ ...form, minutes: "", note: "" });
      setRefresh((n) => n + 1);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Time could not be recorded.");
    } finally {
      setBusy(false);
    }
  }
  if (!summary) return error ? <p role="alert">{error}</p> : null;
  const total = summary.tracked_seconds / 60 + summary.recorded_minutes;
  return (
    <section aria-label="Human effort for this close">
      <h2>Human effort</h2>
      <p>
        Human minutes on this close. Tracked activity counts visible, interactive time in this workspace; add off-screen preparation,
        corrections or configuration below. Waiting on clients is separate from labor.
      </p>
      <dl>
        <dt>Human minutes</dt>
        <dd>
          <strong>{summary.tracked_seconds || summary.recorded_minutes ? minutes(total) : "Not measured yet"}</strong>
        </dd>
        <dt>Tracked workspace activity</dt>
        <dd>
          {minutes(summary.tracked_seconds / 60)}
          {summary.tracking_since ? ` since ${dates.date(summary.tracking_since)}` : ""}
          {Object.keys(summary.tracked_by_activity).length > 0 &&
            ` · approximately ${Object.entries(summary.tracked_by_activity)
              .map(([key, seconds]) => `${labels[key as Activity]} ${minutes((seconds ?? 0) / 60)}`)
              .join(", ")}`}
        </dd>
        <dt>Recorded off-screen time</dt>
        <dd>{minutes(summary.recorded_minutes)}</dd>
        <dt>Waiting on clients (answered requests)</dt>
        <dd>{summary.answered_wait_seconds ? `${hours(summary.answered_wait_seconds)} from delivery to first reply` : "No answered requests yet"}</dd>
        <dt>Delivered requests without a reply</dt>
        <dd>
          {summary.open_requests}
          {summary.oldest_open_since ? ` · oldest since ${dates.date(summary.oldest_open_since)}` : ""}
        </dd>
      </dl>
      <details>
        <summary>Record off-screen time</summary>
        <div className="form-grid">
          <label>
            Activity
            <select value={form.activity} onChange={(e) => setForm({ ...form, activity: e.target.value as EntryActivity })}>
              {Object.entries(entryLabels).map(([key, label]) => (
                <option key={key} value={key}>
                  {label}
                </option>
              ))}
            </select>
          </label>
          <label>
            Minutes
            <input type="number" min={1} max={480} value={form.minutes} onChange={(e) => setForm({ ...form, minutes: e.target.value })} />
          </label>
        </div>
        <label>
          What was done outside the workspace?
          <textarea maxLength={500} value={form.note} onChange={(e) => setForm({ ...form, note: e.target.value })} />
        </label>
        <button type="button" disabled={busy || !form.minutes || form.note.trim().length < 5} onClick={() => void record()}>
          Record time
        </button>
        {summary.entries.length > 0 && (
          <ul>
            {summary.entries.map((entry, index) => (
              <li key={index}>
                {entryLabels[entry.activity]} · {entry.minutes} min · {entry.note} · {dates.date(entry.created_at)}
                {entry.mine ? " · you" : ""}
              </li>
            ))}
          </ul>
        )}
      </details>
      {error && <p role="alert">{error}</p>}
    </section>
  );
}
