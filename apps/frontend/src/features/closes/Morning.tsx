import { useState } from "react";
import { useNavigate } from "react-router";
import { SectionHeader } from "@close/ui/components/SectionHeader";
import { workspaceRequest } from "./workspace";

export type Obligation = { close_id: string; entity: string; period: string; due_on: string; state: "open" | "reopen_required" | "unknown" };
// A month a roster client owes a close for that nobody opened: due when the firm's template would date it, and mine when
// the template would give it to the reader.
export type Unopened = { entity: string; period: string; due_on: string; mine: boolean; hand_over: boolean };

const count = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

// The months the firm's roster says are owed and nobody opened, including one a monthly hand-over has yet to open. Opening
// one gives it the firm's monthly template.
// Months owed and not opened, each with a button to open it. Work lists them all, newest first, and those due within
// the week again under its due closes (due set), soonest first.
export function NotOpened({ months, due }: { months: Unopened[]; due?: boolean }) {
  const [busy, setBusy] = useState("");
  const [error, setError] = useState("");
  const navigate = useNavigate();
  if (months.length === 0) return null;
  async function open(month: Unopened) {
    setBusy(`${month.entity}:${month.period}`);
    setError("");
    try {
      const saved: { id: string } = await workspaceRequest("", { entity: month.entity, period: month.period });
      navigate(`/dashboard/closes/${saved.id}`);
    } catch (e) {
      setError(e instanceof Error ? e.message : "The month could not be opened.");
      setBusy("");
    }
  }
  return (
    <section aria-label={due ? "Due and not opened" : "Months not opened"}>
      {due ? (
        <h3>Due and not opened</h3>
      ) : (
        <SectionHeader title="Months not opened" id="months-not-opened">
          <small>{count(months.length, "month", "months")} the firm's roster says are owed that nobody has opened, newest first.</small>
        </SectionHeader>
      )}
      <ul>
        {months.map((m) => (
          <li key={`${m.entity}:${m.period}`}>
            {m.entity} · {m.period}
            {m.due_on && ` · due ${m.due_on}`}
            {m.mine && " · yours"}
            {m.hand_over && " · the monthly hand-over is set to open it"}{" "}
            <button type="button" disabled={busy !== ""} onClick={() => void open(m)}>
              {busy === `${m.entity}:${m.period}` ? "Opening…" : `Open ${m.period}`}
            </button>
          </li>
        ))}
      </ul>
      {error && <p role="alert">{error}</p>}
    </section>
  );
}
