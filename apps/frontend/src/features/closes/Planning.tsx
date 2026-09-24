import { useState } from "react";
import { workspaceRequest } from "./workspace";

export type Plan = { due_on?: string; priority?: string; owned_by_me?: boolean; assigned?: boolean };

function days(due: string) {
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  return Math.round((new Date(`${due}T00:00:00`).getTime() - today.getTime()) / 86_400_000);
}

export function PlanChips({ plan }: { plan: Plan }) {
  const chips = [];
  if (plan.due_on) {
    const left = days(plan.due_on);
    const date = new Date(`${plan.due_on}T00:00:00`).toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" });
    const [tone, label] = left < 0 ? ["status danger", `Overdue · ${date}`] : left <= 7 ? ["status warn", `Due ${left === 0 ? "today" : `in ${left}d`} · ${date}`] : ["status", `Due ${date}`];
    chips.push(
      <span key="due" className={tone}>
        {label}
      </span>,
    );
  }
  if (plan.priority && plan.priority !== "normal") chips.push(<span key="priority" className="chip">{plan.priority === "high" ? "High priority" : "Low priority"}</span>);
  if (plan.owned_by_me) chips.push(<span key="mine" className="chip">Mine</span>);
  else if (plan.assigned) chips.push(<span key="held" className="chip">Held by a colleague</span>);
  return chips.length ? <span className="plan-chips">{chips}</span> : null;
}

export function PlanningControls({ closeId, plan, onSaved }: { closeId: string; plan: Plan; onSaved: () => void }) {
  const [dueOn, setDueOn] = useState(plan.due_on ?? "");
  const [priority, setPriority] = useState(plan.priority ?? "normal");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  async function save(assignment: "keep" | "take" | "release") {
    setBusy(true);
    setMessage("");
    try {
      await workspaceRequest(`/${closeId}/planning`, { due_on: dueOn, priority, assignment });
      setMessage("Saved.");
      onSaved();
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "Could not save the plan.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <form
      className="planning"
      aria-label="Close plan"
      onSubmit={(event) => {
        event.preventDefault();
        void save("keep");
      }}
    >
      <label>
        Due date
        <input type="date" value={dueOn} onChange={(e) => setDueOn(e.target.value)} />
      </label>
      <label>
        Priority
        <select value={priority} onChange={(e) => setPriority(e.target.value)}>
          <option value="high">High</option>
          <option value="normal">Normal</option>
          <option value="low">Low</option>
        </select>
      </label>
      <button disabled={busy}>Save plan</button>
      {plan.owned_by_me ? (
        <button type="button" disabled={busy} onClick={() => void save("release")}>
          Release
        </button>
      ) : (
        <button type="button" disabled={busy || plan.assigned} onClick={() => void save("take")}>
          {plan.assigned ? "Held by a colleague" : "Assign to me"}
        </button>
      )}
      <span role="status">{message}</span>
    </form>
  );
}
