import { useEffect, useState } from "react";
import { Link, useLocation } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Icon } from "@close/ui/components/Icon";
import { workspaceQuery } from "../../data/queries";
import { monthName, useDates, useHour } from "../../data/time";
import { eventTarget } from "../closes/Attention";
import { Setup } from "../closes/Setup";
import { Conversation } from "../dashboard/Assistant";
import type { HomeCounts } from "./Decisions";
import { Goals } from "./Goals";

const n = (count: number, one: string, many = `${one}s`) => `${count} ${count === 1 ? one : many}`;

// A draft's kind as the Workpapers tab names it, without loading that page with Home.
const draftWords: Record<string, string> = { prepaid: "Prepaid schedule", deferred_revenue: "Deferred revenue schedule", depreciation: "Depreciation schedule", accrual: "Accrued expense", reconciliation: "Statement reconciliation", settlement: "Stripe settlement", loan: "Loan schedule", owner: "Owner reclassification", payroll: "Payroll" };

// Home is a stage for the agent (docs/design-system.md, The shell): the greeting, one line on what needs the person
// and what the agent is doing (the counts Closes carries in full, and when they were read), the composer with the
// agent's three offers, the top of the decision list as Your next decisions with a link to all of them, and, folded
// beneath the stage after any portfolio goals, the drafts prepared in the past week. Send, and the greeting and the lists step aside while the thread fills the
// stage; New chat brings the stage back. A firm's first steps (Get started) and its first client follow beneath.
export default function Home() {
  const queryClient = useQueryClient();
  const dates = useDates();
  const homeRead = useQuery({ ...workspaceQuery<HomeCounts>("/home"), refetchInterval: 30_000 });
  const counts = homeRead.data;
  const error = homeRead.error ? homeRead.error.message || "Home is unavailable." : "";
  // The person's name comes with their profile, read with the page.
  const me = useQuery(workspaceQuery<{ email?: string; first_name?: string }>("/me/profile")).data;
  const [chatting, setChatting] = useState(false);
  // A portfolio goal the agent linked to (/dashboard?goal=…) previews beneath the stage: the page scrolls to it.
  const location = useLocation();
  const goal = new URLSearchParams(location.search).get("goal");
  useEffect(() => {
    if (goal) document.getElementById("goals")?.scrollIntoView({ block: "start" });
  }, [goal]);
  const bump = () => void queryClient.invalidateQueries({ queryKey: ["workspace"] });
  const hour = useHour();
  const daypart = hour < 12 ? "morning" : hour < 18 ? "afternoon" : "evening";
  const name = me?.first_name?.trim() || me?.email?.split("@")[0] || "";
  // Your next decisions: the most pressing closes with a decision of the reader's, as the decision list orders them, and
  // the closes whose sign-off no longer stands, which count as decisions without an attention event.
  const today = (counts?.as_of ?? "").slice(0, 10);
  // A sign-off that no longer stands is the reviewer's decision; without review authority it is a teammate's, as
  // Closes files it.
  const reopened = (counts?.can_review ? [...counts.owed.filter((item) => item.state === "reopen_required" || item.state === "unknown"), ...counts.reopened_later] : []).map((item) => ({
    close_id: item.close_id,
    entity: item.entity,
    period: item.period,
    due_on: item.due_on,
    text: item.state === "unknown" ? "Sign-off could not be rechecked" : "Changed since sign-off",
    to: `/dashboard/closes/${item.close_id}?tab=signoff`,
  }));
  // Reopened sign-offs lead, as the decision list orders them.
  const week = [
    ...reopened,
    ...(counts?.inbox.groups ?? [])
      .map((g) => ({ ...g, events: g.events.filter((e) => e.yours && !e.snoozed_until && !e.info) }))
      .filter((g) => g.events.length > 0)
      // The row opens the decision itself, as the decision list does.
      .map((g) => ({ close_id: g.close_id, entity: g.entity, period: g.period, due_on: g.due_on, text: g.events[0].text, to: eventTarget(g.close_id, g.events[0]) })),
  ]
    .filter((item, i, all) => all.findIndex((other) => other.close_id === item.close_id) === i)
    .slice(0, 3);
  // The line says what needs the person, then what the agent is doing and what waits on clients, so no decisions while
  // the agent works reads as that, not as done.
  // The snapshot's count already holds the closes whose sign-off no longer stands.
  const decisions = counts?.decisions.count ?? 0;
  const d = counts?.deadlines;
  const missed = d ? d.overdue + d.not_opened_overdue : 0;
  const needs = counts
    ? [
        decisions ? `${n(decisions, "decision")} ${decisions === 1 ? "needs" : "need"} you` + (counts.decisions.unread ? ` (${counts.decisions.unread} new)` : "") : "",
        missed ? n(missed, "close") + " overdue" : "",
        counts.uncaptured.length ? n(counts.uncaptured.length, "month") + " without month-end books" : "",
      ].filter(Boolean)
    : [];
  const agent = counts
    ? [
        counts.prepared.running ? `The agent is preparing ${n(counts.prepared.running, "document")}` : "",
        counts.prepared.waiting_client ? `${n(counts.prepared.waiting_client, "document")} ${counts.prepared.waiting_client === 1 ? "waits" : "wait"} on clients` : "",
      ].filter(Boolean)
    : [];
  const line = counts
    ? `${needs.length ? needs.join(" · ") : "Nothing needs you"}${agent.length ? `. ${agent.join("; ")}` : ""} · counted at ${dates.time(counts.as_of)}`
    : error
      ? "Your closes could not be read."
      : "Reading your closes…";
  const reviewWords: Record<string, string> = { pending: "ready for review", approved: "approved", changes_requested: "returned", source_changed: "source changed" };
  const empty = counts && counts.closes === 0 && counts.not_opened.length === 0;
  return (
    <>
      <section className={`ks-stage${chatting ? " chatting" : ""}`} aria-label="Home">
        <div className="ks-hello" aria-hidden={chatting}>
          <h2>{name ? `Good ${daypart}, ${name}.` : `Good ${daypart}.`}</h2>
          <p>{line}</p>
        </div>
        <Conversation mode="stage" closeId="" paperId="" executive={false} focus={0} onClose={() => undefined} onEmpty={(isEmpty) => setChatting(!isEmpty)} />
        <div className="ks-week" aria-hidden={chatting}>
          {error && <p role="alert">{error}</p>}
          {empty ? (
            counts.can_add ? (
              <Link className="ks-row" to="/dashboard/clients">
                <div>
                  <b>Add your first client</b> <span>· name it and open its month; the agent takes it from there</span>
                </div>
                <span className="ks-when" />
                <Icon name="arrow" className="ks-go" />
              </Link>
            ) : (
              <p className="ks-week-note">No month is open for your clients yet. A firm administrator assigns clients under Settings.</p>
            )
          ) : (
            <>
              <div className="ks-cat">Your next decisions</div>
              {week.map((g) => (
                <Link className="ks-row" key={g.close_id} to={g.to}>
                  <div>
                    <b>{g.entity}</b> <span>· {monthName(g.period)} · {g.text}</span>
                  </div>
                  <span className="ks-when">{g.due_on ? (g.due_on < today ? "Overdue" : dates.calendarDay(g.due_on)) : "No date"}</span>
                  <Icon name="arrow" className="ks-go" />
                </Link>
              ))}
              {counts && week.length === 0 && <p className="ks-week-note">{agent.length ? "No decision needs you yet; the agent's work and the clients' replies will bring them here." : "No decision needs you now."}</p>}
              <Link className="ks-more" to="/dashboard/closes">
                {decisions > week.length ? `All ${decisions} decisions` : "All closes"}
              </Link>
            </>
          )}
        </div>
      </section>
      <div className="ks-under" id="goals">
        <Goals onChange={bump} />
        {!!counts?.recent.length && (
          <details className="ks-done">
            <summary className="ks-done-line">
              <Icon name="check" />
              Prepared in the past week · {n(counts.recent.length, "draft")}
              {counts.recent.length === 5 ? ", the latest" : ""}
            </summary>
            {counts.recent.map((r) => (
              <Link className="ks-row" key={r.workpaper_id} to={`/dashboard/closes/${r.close_id}/workpapers/${r.workpaper_id}`}>
                <div>
                  <b>{r.entity}</b> <span>· {monthName(r.period)} · {draftWords[r.schedule || r.kind] ?? "Draft"} #{r.workpaper_id}</span>
                </div>
                <span className="ks-when">{reviewWords[r.state] ?? r.state.replaceAll("_", " ")}</span>
                <Icon name="arrow" className="ks-go" />
              </Link>
            ))}
          </details>
        )}
        {!empty && <Setup onEnrolled={bump} />}
      </div>
    </>
  );
}
