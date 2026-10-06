import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { AgentMark } from "@close/ui/components/AgentMark";
import { SectionHeader } from "@close/ui/components/SectionHeader";
import { workspaceRequest } from "./workspace";

export type Obligation = { close_id: string; entity: string; period: string; due_on: string; state: "open" | "reopen_required" | "unknown" };
// A month a roster client owes a close for that nobody opened: due when the firm's template would date it, and mine when
// the template would give it to the reader.
export type Unopened = { entity: string; period: string; due_on: string; mine: boolean; hand_over: boolean };

export type Morning = {
  overnight: { month_end_snapshots: number; drafts_prepared: number; standing_approvals: number };
  closes: { close_id: string; entity: string; period: string; needs: string[] }[];
  needing: number;
  quiet: number;
  model_check: { model: string; reasoning_effort?: string | null; close_id: string; kept: number; scored: number; checked_at: string } | null;
  as_of: string;
  due: Obligation[];
  // not_opened_*: months owed and not opened, dated overdue or within a week, listed under Months not opened.
  due_counts: { overdue: number; upcoming: number; no_due_date: number; not_opened_overdue?: number; not_opened_upcoming?: number };
  not_opened: Unopened[];
};

const count = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

// The brief comes from Work, which also counts its due closes from the same read.
export function MorningBrief({ brief, error }: { brief?: Morning; error: string }) {
  if (!brief) return error ? <p role="alert">{error}</p> : null;
  const done = brief.overnight;
  return (
    <section aria-label="Morning brief">
      <SectionHeader title={<AgentMark size="sm">Good morning</AgentMark>} />
      <p>
        In the past 24 hours the agent captured {count(done.month_end_snapshots, "month-end snapshot", "month-end snapshots")}, prepared{" "}
        {count(done.drafts_prepared, "draft", "drafts")} and approved {count(done.standing_approvals, "later-month draft", "later-month drafts")} under standing
        approvals.
      </p>
      {brief.model_check && (
        <p className={brief.model_check.kept === brief.model_check.scored ? "note" : undefined} role={brief.model_check.kept === brief.model_check.scored ? undefined : "status"}>
          Using {brief.model_check.model} ({brief.model_check.reasoning_effort == null ? "reasoning effort not recorded" : `${brief.model_check.reasoning_effort || "provider default"} reasoning effort`}), {brief.model_check.kept} of {brief.model_check.scored} past confirmed readings came out the same
          ({new Date(brief.model_check.checked_at).toLocaleDateString()}). <Link to={`/dashboard/closes/${brief.model_check.close_id}`}>See the readings</Link>.
        </p>
      )}
      {brief.needing === 0 ? (
        <p className="empty">No outstanding close has drafts, confirmations, failed documents, missing snapshots, new client questions, overdue dates or changes since its sign-off waiting. Each close still shows its own tie-out and changes.</p>
      ) : (
        <>
          <p>
            <strong>{count(brief.needing, "close needs", "closes need")} a person today</strong>
            {brief.closes.length < brief.needing ? ` (showing ${brief.closes.length})` : ""}:
          </p>
          <ul>
            {brief.closes.map((c) => (
              <li key={c.close_id}>
                <Link to={`/dashboard/closes/${c.close_id}`}>
                  {c.entity} · {c.period}
                </Link>
                : {c.needs.join(", ")}
              </li>
            ))}
          </ul>
          {brief.quiet > 0 && (
            <p className="note">
              {count(brief.quiet, "other outstanding close has", "other outstanding closes have")} none of these waiting; its tie-out and changes are on the close page.
            </p>
          )}
        </>
      )}
    </section>
  );
}

// The months the firm's roster says are owed and nobody opened, including one a monthly hand-over has yet to open. Opening
// one gives it the firm's monthly template.
export function NotOpened({ months }: { months: Unopened[] }) {
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
    <section aria-label="Months not opened">
      <SectionHeader title="Months not opened" id="months-not-opened">
        <small>{count(months.length, "month", "months")} the firm's roster says are owed that nobody has opened, newest first.</small>
      </SectionHeader>
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
