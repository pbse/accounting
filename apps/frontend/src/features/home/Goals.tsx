import { useDates } from "../../data/time";
import { useQuery } from "@tanstack/react-query";
import { workspaceQuery } from "../../data/queries";
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
  // The saved goals still running are the page's query. A goal previewed from a link shows until the saved list has
  // it; a goal run here keeps its last state on the page, finished or not, so its results can be read after the saved
  // list (which holds only running goals) drops it. Each goal shows once, in its newest known state.
  const saved = useQuery(workspaceQuery<{ operations: Goal[] }>("/portfolio")).data?.operations ?? [];
  const [previewed, setPreviewed] = useState<Goal[]>([]);
  const [ran, setRan] = useState<Goal[]>([]);
  const [error, setError] = useState("");
  const latest = new Map(ran.map((g) => [g.id, g]));
  const savedIds = new Set(saved.map((g) => g.id));
  const goals = [
    ...ran.filter((g) => !savedIds.has(g.id)),
    ...previewed.filter((g) => !g.id || (!savedIds.has(g.id) && !latest.has(g.id))),
    ...saved.map((g) => latest.get(g.id) ?? g),
  ];
  const record = (goal: Goal) => setRan((current) => [goal, ...current.filter((g) => g.id !== goal.id)]);
  useEffect(() => {
    if (!text) return;
    let live = true;
    setError("");
    // The link's goal is previewed once: the server keeps the preview, so the link leaves the address and a refresh
    // or a return to Home shows the saved goal rather than making another.
    preview(text, scopeView, scopeClient)
      .then((goal: Goal) => {
        if (!live) return;
        setPreviewed((current) => [goal, ...current.filter((g) => g.id !== goal.id)]);
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
        <PortfolioGoal key={goal.id || goal.goal} goal={goal} onRan={record} onChange={onChange} />
      ))}
    </>
  );
}

// A goal's exact server-selected closes. Each run acts on at most ten, each checked again against its preview.
function PortfolioGoal({ goal, onRan, onChange }: { goal: Goal; onRan: (goal: Goal) => void; onChange: () => void }) {
  const [current, setCurrent] = useState(goal);
  const dates = useDates();
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
      ? `${i.count} ${noun}${i.count === 1 ? "" : "s"}; hand over ${i.due_on ? `with the deadline ${dates.calendarDay(i.due_on)}` : "with no deadline"}`
      : `${i.count} ${noun}${i.count === 1 ? "" : "s"} to ${current.action}`;
  async function run() {
    setBusy(true);
    setError("");
    try {
      let left = pending;
      while (left > 0) {
        const next: Goal = await workspaceRequest("/portfolio/run", { id: current.id, confirmed: true, reason: reason.trim() });
        setCurrent(next);
        onRan(next);
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
