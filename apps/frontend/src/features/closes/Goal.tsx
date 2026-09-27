import { type ReactNode, useEffect, useState } from "react";
import { Link } from "react-router";
import { askAssistant } from "../dashboard/assistantStore";
import { workspaceRequest } from "./workspace";

type Item = { job_id: string; name: string; status: "prepared" | "needs_you" | "client" | "working" | "unsupported" | "stopped"; text: string; drafts: string[] };
type Goal = { id: string; author: string; due_on: string; state: "active" | "reached" | "stopped"; created_at: string; reached_at: string | null; finished_at: string | null; events: { actor: string; text: string; at: string }[] };
type Status = {
  goal: Goal | null;
  progress: { items: Item[]; needs: string[]; notes: string[]; reached: boolean };
  activity: { at: string; actor: string; text: string }[];
  activity_total: number;
  signed_off: boolean;
};

const sections: [Item["status"], string][] = [
  ["needs_you", "Needs you"],
  ["unsupported", "I can't prepare"],
  ["client", "Waiting on the client"],
  ["working", "I'm working on"],
  ["prepared", "Ready for review"],
  ["stopped", "Left out"],
];
const handOver = "Get this close ready for review";

export function localDate(d = new Date()) {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

function due(on: string) {
  const days = Math.round((Date.parse(on) - Date.parse(localDate())) / 86_400_000);
  const day = new Date(`${on}T00:00:00`).toLocaleDateString(undefined, { weekday: "short", day: "numeric", month: "short" });
  if (days < 0) return `Due ${day}, ${-days} day${days === -1 ? "" : "s"} overdue`;
  return days === 0 ? "Due today" : days === 1 ? "Due tomorrow" : `Due ${day}, in ${days} days`;
}

function counts(items: Item[]) {
  if (!items.length) return "No documents yet.";
  const by = (s: Item["status"]) => items.filter((i) => i.status === s).length;
  const parts = [`${by("prepared")} ready for review`, `${by("needs_you") + by("unsupported")} need you`, `${by("client")} waiting on the client`, `${by("working")} in progress`].filter((p) => !p.startsWith("0 "));
  return `${items.length} document${items.length === 1 ? "" : "s"}: ${parts.join(", ")}.`;
}

// useGoal reads the close's goal and live progress, polling while shown.
function useGoal(closeId: string, live: boolean, every: number) {
  const [status, setStatus] = useState<Status>();
  const [error, setError] = useState("");
  const [asked, setAsked] = useState(0);
  useEffect(() => {
    if (!live) return;
    let on = true;
    let timer: ReturnType<typeof setTimeout>;
    const poll = () =>
      workspaceRequest(`/${closeId}/goal`)
        .then((data: Status) => {
          if (!on) return;
          setStatus(data);
          setError("");
        })
        .catch((e) => on && setError(e instanceof Error ? e.message : "The agent's progress is unavailable."))
        .finally(() => on && (timer = setTimeout(poll, every)));
    void poll();
    return () => {
      on = false;
      clearTimeout(timer);
    };
  }, [closeId, live, asked]);
  return { status, error, refresh: () => setAsked((n) => n + 1) };
}

function Progress({ closeId, status, refresh }: { closeId: string; status: Status; refresh: () => void }) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [all, setAll] = useState<{ goal: string; activity: Status["activity"] }>();
  const { goal, progress, activity, activity_total: total } = status;
  if (!goal) return null;
  // The whole history is read on request and shown only while it is still the whole, so the newest steps always show.
  const full = all?.goal === goal.id && all.activity.length === total ? all.activity : undefined;
  const stopped = goal.state === "stopped";
  const needs = progress.items.filter((i) => i.status === "needs_you" || i.status === "unsupported").length + progress.needs.length;
  async function stop() {
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(`/${closeId}/goal/stop`, { reason: reason.trim() });
      setReason("");
      refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The agent could not be stopped.");
    } finally {
      setBusy(false);
    }
  }
  const link = (item: Item) => (item.drafts[0] ? `/dashboard/closes/${closeId}/workpapers/${item.drafts[0]}` : `/dashboard/closes/${closeId}`);
  return (
    <>
      <p>
        <strong>
          {stopped
            ? goal.events[goal.events.length - 1]?.text
            : goal.state === "reached"
              ? "Every uploaded document has a draft ready for your review."
              : needs
                ? `${needs} thing${needs === 1 ? " needs" : "s need"} you before this is ready for review.`
                : "I'm getting this close ready for review."}
        </strong>
      </p>
      <p className="assistant-note">
        Handed over by {goal.author} · {new Date(goal.created_at).toLocaleDateString()}
        {goal.due_on && !stopped ? ` · ${due(goal.due_on)}` : ""}
      </p>
      {!stopped && (
        <>
          {progress.needs.length > 0 && (
            <ul aria-label="Needs you">
              {progress.needs.map((n) => (
                <li key={n}>{n}</li>
              ))}
            </ul>
          )}
          {sections.map(([key, title]) => {
            const items = progress.items.filter((i) => i.status === key);
            return (
              items.length > 0 && (
                <div key={key}>
                  <strong>
                    {title} · {items.length}
                  </strong>
                  <ul>
                    {items.map((i) => (
                      <li key={i.job_id}>
                        <Link to={link(i)}>{i.name}</Link>: {i.text}
                      </li>
                    ))}
                  </ul>
                </div>
              )
            );
          })}
          {progress.notes.map((n) => (
            <p key={n} className="assistant-note">
              {n}
            </p>
          ))}
        </>
      )}
      {total > 0 && (
        <details>
          <summary>What happened · {total}</summary>
          <ol>
            {(full ?? activity).map((a, n) => (
              <li key={n}>
                {new Date(a.at).toLocaleString()} · {a.text}
              </li>
            ))}
          </ol>
          {!full && total > activity.length && (
            <button type="button" onClick={() => void workspaceRequest(`/${closeId}/goal?activity=all`).then((d: Status) => d.goal && setAll({ goal: d.goal.id, activity: d.activity }), () => setError("The full history could not be read."))}>
              Show all {total}
            </button>
          )}
        </details>
      )}
      {stopped ? (
        !status.signed_off && (
          <button type="button" onClick={() => askAssistant(handOver, closeId)}>
            Hand it over again
          </button>
        )
      ) : (
        <details>
          <summary>Stop the agent</summary>
          <label>
            Reason
            <textarea value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
          </label>
          <button type="button" disabled={busy || !reason.trim()} onClick={() => void stop()}>
            {busy ? "Stopping…" : "Stop"}
          </button>
          <p className="assistant-note">Work already started keeps running; stopping only takes the close back from the agent.</p>
        </details>
      )}
      {error && <p role="alert">{error}</p>}
    </>
  );
}

function Proposal({ status, dueOn, onConfirm }: { status: Status; dueOn: string; onConfirm: (body: { due_on: string }) => Promise<void> }) {
  const [date, setDate] = useState(dueOn);
  const [agreed, setAgreed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function confirm() {
    setBusy(true);
    setError("");
    try {
      await onConfirm({ due_on: date });
    } catch (e) {
      setError(e instanceof Error ? e.message : "The close could not be handed over.");
      setAgreed(false);
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <p>I'll keep this close moving until every uploaded document has a draft ready for your review:</p>
      <ul>
        <li>I bring new documents and client replies into preparation as they arrive.</li>
        <li>I tell you exactly what I need from you, and why.</li>
        <li>I let you know as soon as it's ready for review.</li>
      </ul>
      <p>I never approve, send, post or sign off anything, and you can stop me at any time.</p>
      <p>Now: {counts(status.progress.items)}</p>
      <label>
        Ready for review by <input type="date" value={date} min={localDate()} onChange={(e) => setDate(e.target.value)} />
      </label>
      <label>
        <input type="checkbox" checked={agreed} onChange={(e) => setAgreed(e.target.checked)} /> Let the agent work on this close until it's ready for review.
      </label>
      <button type="button" className="primary" disabled={busy || !agreed} onClick={() => void confirm()}>
        {busy ? "Handing over…" : "Hand it over"}
      </button>
      <p className="assistant-note">Handing over needs the workpapers:review permission.</p>
      {error && <p role="alert">{error}</p>}
    </>
  );
}

// GoalCard is the assistant's card for handing its close to the agent: the proposal, then the goal's live progress.
export function GoalCard({ closeId, dueOn, outcome, live, onConfirm }: { closeId: string; dueOn: string; outcome?: { at: string } | null; live: boolean; onConfirm: (body: { due_on: string }) => Promise<void> }) {
  const { status, error, refresh } = useGoal(closeId, live, 5000);
  const card = (body: ReactNode) => (
    <div className="assistant-card">
      <strong>Hand this close to the agent</strong>
      {body}
      {error && <p role="alert">{error}</p>}
    </div>
  );
  if (!live) return card(<p>{outcome ? `Handed over ${new Date(outcome.at).toLocaleString()}. The newest card shows its progress.` : "A newer card replaced this one."}</p>);
  if (!status) return card(!error && <p role="status">Loading the close…</p>);
  if (outcome || (status.goal && status.goal.state !== "stopped")) return card(<Progress closeId={closeId} status={status} refresh={refresh} />);
  if (status.signed_off) return card(<p>This close is signed off, so there is nothing left for me to prepare.</p>);
  return card(<Proposal status={status} dueOn={dueOn} onConfirm={onConfirm} />);
}

// GoalPanel shows the close's goal at the top of its Overview, or offers to hand the close to the agent.
export function GoalPanel({ closeId }: { closeId: string }) {
  const { status, error, refresh } = useGoal(closeId, true, 10000);
  if (!status) return error ? <p role="alert">{error}</p> : null;
  if (!status.goal && status.signed_off) return null;
  return (
    <section className="panel" aria-labelledby="goal-title">
      <h2 id="goal-title">{status.goal && status.goal.state !== "stopped" ? "Handed to the agent" : "Hand this close to the agent"}</h2>
      {status.goal ? (
        <Progress closeId={closeId} status={status} refresh={refresh} />
      ) : (
        <p>
          The agent keeps preparation moving and tells you only what needs you.{" "}
          <button type="button" onClick={() => askAssistant(handOver, closeId)}>
            Hand it over
          </button>
        </p>
      )}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}
