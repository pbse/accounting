import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { SectionHeader } from "@close/ui/components/SectionHeader";
import { workspaceRequest } from "../closes/workspace";

// One preview request per linked goal at a time, shared by every mount of the effect while it is in flight (React's
// development double mount among them); once it settles, the same link asked again selects the closes afresh.
// A goal acting on "visible" closes selects them by the view and client filter it was asked from: a link carries them
// as goal_view and goal_client, and without them the selection is the Preparation page's default (active work, every
// client), the one the firm-wide Work page used for a linked goal.
const previews = new Map<string, Promise<Goal>>();
const preview = (text: string, view: string, client: string) => {
  const key = `${text}\u0000${view}\u0000${client}`;
  let p = previews.get(key);
  if (!p) {
    p = workspaceRequest("/portfolio", { text, view, client, today: new Date().toLocaleDateString("en-CA") });
    previews.set(key, p);
    p.finally(() => previews.delete(key)).catch(() => undefined);
  }
  return p;
};

type Goal = { id: string; action: "prepare" | "recheck" | "stop" | "delegate" | "show"; finish?: "review" | "signoff"; goal: string; scope: string; has_more: boolean; reason: string; items: { close_id: string; label: string; count: number; state: string; result: string; due_on?: string }[] };

// The portfolio goals on Home: one the agent panel linked to (/dashboard?goal=…), previewed by the server as the
// exact closes it selects, and the reader's saved goals still running. Nothing runs until the reader confirms a preview.
export function Goals({ onChange }: { onChange: () => void }) {
  const [params, setParams] = useSearchParams();
  const text = params.get("goal")?.trim() ?? "";
  const scopeView = params.get("goal_view") || "active";
  const scopeClient = params.get("goal_client") ?? "";
  const [goals, setGoals] = useState<Goal[]>([]);
  const [error, setError] = useState("");
  useEffect(() => {
    let live = true;
    // The saved list replaces its own entries only: a goal previewed while it was read (one with an id the list does not
    // have yet) stays, and a listing, which is never saved, stays too.
    workspaceRequest("/portfolio")
      .then((data: { operations: Goal[] }) => {
        if (!live) return;
        const listed = new Set(data.operations.map((g) => g.id));
        setGoals((current) => [...current.filter((g) => !listed.has(g.id)), ...data.operations]);
      })
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, []);
  useEffect(() => {
    if (!text) return;
    let live = true;
    setError("");
    // The link's goal is previewed once: the server keeps the preview, so the link leaves the address and a refresh
    // or a return to Home shows the saved goal rather than making another.
    preview(text, scopeView, scopeClient)
      .then((goal: Goal) => {
        if (!live) return;
        setGoals((current) => [goal, ...current.filter((g) => g.id !== goal.id)]);
        const next = new URLSearchParams(params);
        next.delete("goal");
        next.delete("goal_view");
        next.delete("goal_client");
        setParams(next, { replace: true });
      })
      .catch((e) => live && setError(e instanceof Error ? e.message : "The goal could not be previewed."));
    return () => {
      live = false;
    };
  }, [text, scopeView, scopeClient]);
  if (goals.length === 0 && !error) return null;
  return (
    <>
      <SectionHeader title="Goals" id="goals">
        <small>Each goal lists exactly which closes it selects before anything runs.</small>
      </SectionHeader>
      {error && <p role="alert">{error}</p>}
      {goals.map((goal) => (
        <PortfolioGoal key={goal.id || goal.goal} goal={goal} onChange={onChange} />
      ))}
    </>
  );
}

// A goal's exact server-selected closes. Each run acts on at most ten, each checked again against its preview.
function PortfolioGoal({ goal, onChange }: { goal: Goal; onChange: () => void }) {
  const [current, setCurrent] = useState(goal);
  // A listing asked again comes back as a new goal with the same key.
  useEffect(() => setCurrent(goal), [goal]);
  const [reason, setReason] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const pending = current.items.filter((i) => i.state === "pending").length;
  const needsReason = current.action === "stop" && !current.reason;
  const noun = current.action === "prepare" || current.action === "delegate" ? "source document" : "job";
  const handing = current.action === "delegate";
  // A hand-over shows each close's deadline for the agent: its own due date when the close page would accept it.
  const pendingText = (i: Goal["items"][number]) =>
    handing
      ? `${i.count} ${noun}${i.count === 1 ? "" : "s"}; hand over ${i.due_on ? `with the deadline ${new Date(`${i.due_on}T00:00:00`).toLocaleDateString(undefined, { weekday: "short", day: "numeric", month: "short" })}` : "with no deadline"}`
      : `${i.count} ${noun}${i.count === 1 ? "" : "s"} to ${current.action}`;
  async function run() {
    setBusy(true);
    setError("");
    try {
      let left = pending;
      while (left > 0) {
        const next: Goal = await workspaceRequest("/portfolio/run", { id: current.id, confirmed: true, reason: reason.trim() });
        setCurrent(next);
        const now = next.items.filter((i) => i.state === "pending").length;
        if (now >= left) break;
        left = now;
      }
      onChange();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The goal could not run.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section aria-label={`Goal: ${current.goal}`}>
      <h3>{current.goal}</h3>
      <p>
        {current.scope}.{current.has_more && " More than 100 closes matched; this goal covers the first 100 by due date."}
      </p>
      {current.items.length === 0 ? (
        <p>No closes match this goal.</p>
      ) : (
        <ul>
          {current.items.map((i, n) => (
            <li key={i.close_id || n}>
              {i.close_id ? <Link to={`/dashboard/closes/${i.close_id}`}>{i.label}</Link> : i.label}: {i.state === "pending" ? pendingText(i) : i.result || i.state}
            </li>
          ))}
        </ul>
      )}
      {pending > 0 && (
        <>
          {needsReason && (
            <label>
              Reason for stopping
              <textarea value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
            </label>
          )}
          <label>
            <input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} />{" "}
            {handing
              ? `Hand the ${pending} pending close${pending === 1 ? "" : "s"} to the agent to get each ${current.finish === "signoff" ? "signed off" : "ready for review"}. A prepaid invoice whose facts the invoice and the QuickBooks capture both prove is drafted under the firm's procedure without field-by-field confirmation; everything else waits for a person. The agent never approves, sends, posts or signs off.`
              : `${current.action[0].toUpperCase() + current.action.slice(1)} the ${pending} pending close${pending === 1 ? "" : "s"}; facts and methods still need separate review.`}
          </label>
          <button type="button" className="primary" disabled={busy || !confirmed || (needsReason && !reason.trim())} onClick={() => void run()}>
            {busy ? "Running…" : `Run for ${pending} close${pending === 1 ? "" : "s"}`}
          </button>
        </>
      )}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}
