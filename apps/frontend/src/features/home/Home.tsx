import { useState } from "react";
import { Link } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Icon } from "@close/ui/components/Icon";
import { workspaceQuery } from "../../data/queries";
import { useDates, useHour } from "../../data/time";
import { Setup } from "../closes/Setup";
import { Conversation } from "../dashboard/Assistant";
import type { HomeCounts } from "./Decisions";
import { Goals } from "./Goals";

const n = (count: number, one: string, many = `${one}s`) => `${count} ${count === 1 ? one : many}`;

// Home is a stage for the agent (docs/design-system.md, The shell): the greeting, one line on how the week stands
// (the counts Closes carries in full, and when they were read), the composer with the agent's three offers, and the
// top of the decision list as This week. Send, and the greeting and the list step aside while the thread fills the
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
  const bump = () => void queryClient.invalidateQueries({ queryKey: ["workspace"] });
  const hour = useHour();
  const daypart = hour < 12 ? "morning" : hour < 18 ? "afternoon" : "evening";
  const name = me?.first_name?.trim() || me?.email?.split("@")[0] || "";
  const d = counts?.deadlines;
  const missed = d ? d.overdue + d.not_opened_overdue : 0;
  const waiting = counts
    ? [
        counts.decisions.count ? n(counts.decisions.count, "decision") + (counts.decisions.unread ? ` (${counts.decisions.unread} new)` : "") : "",
        missed ? n(missed, "close") + " overdue" : "",
        counts.uncaptured.length ? n(counts.uncaptured.length, "month") + " without month-end books" : "",
        counts.books.exceptions ? n(counts.books.exceptions, "new exception") + " in the books" : "",
        counts.books.unchecked ? n(counts.books.unchecked, "capture") + " not fully checked" : "",
      ].filter(Boolean)
    : [];
  const line = counts ? `${waiting.length ? waiting.join(" · ") : "Nothing is waiting on you"} · counted at ${dates.time(counts.as_of)}` : error ? "Your closes could not be read." : "Reading your closes…";
  // This week: the most pressing closes with a decision of the reader's, as the decision list orders them, and the
  // closes whose sign-off no longer stands, which count as decisions without an attention event.
  const today = (counts?.as_of ?? "").slice(0, 10);
  // A sign-off that no longer stands is the reviewer's decision; without review authority it is a teammate's, as
  // Closes files it.
  const reopened = (counts?.can_review ? [...counts.owed.filter((item) => item.state === "reopen_required" || item.state === "unknown"), ...counts.reopened_later] : []).map((item) => ({
    close_id: item.close_id,
    entity: item.entity,
    due_on: item.due_on,
    text: item.state === "unknown" ? "Sign-off could not be rechecked" : "Changed since sign-off",
  }));
  // Reopened sign-offs lead, as the decision list orders them.
  const week = [
    ...reopened,
    ...(counts?.inbox.groups ?? [])
      .map((g) => ({ ...g, events: g.events.filter((e) => e.yours && !e.snoozed_until) }))
      .filter((g) => g.events.length > 0)
      .map((g) => ({ close_id: g.close_id, entity: g.entity, due_on: g.due_on, text: g.events[0].text })),
  ]
    .filter((item, i, all) => all.findIndex((other) => other.close_id === item.close_id) === i)
    .slice(0, 3);
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
              <div className="ks-cat">This week</div>
              {week.map((g) => (
                <Link className="ks-row" key={g.close_id} to={`/dashboard/closes/${g.close_id}`}>
                  <div>
                    <b>{g.entity}</b> <span>· {g.text}</span>
                  </div>
                  <span className="ks-when">{g.due_on ? (g.due_on < today ? "Overdue" : dates.calendarDay(g.due_on)) : "No date"}</span>
                  <Icon name="arrow" className="ks-go" />
                </Link>
              ))}
              {counts && week.length === 0 && <p className="ks-week-note">Nothing needs a decision this week.</p>}
              <Link className="ks-more" to="/dashboard/closes">
                All closes
              </Link>
            </>
          )}
        </div>
      </section>
      <div className="ks-under">
        <Goals onChange={bump} />
        {!empty && <Setup onEnrolled={bump} />}
      </div>
    </>
  );
}
