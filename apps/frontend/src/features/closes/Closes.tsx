import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { PageHeader } from "@close/ui/components/PageHeader";
import { workspaceQuery } from "../../data/queries";
import { useDates } from "../../data/time";
import { Decisions, type HomeCounts, type View } from "../home/Home";

const n = (count: number, one: string, many = `${one}s`) => `${count} ${count === 1 ? one : many}`;

// Closes, the records page of the firm's work (docs/design-system.md, The shell): the four outcome counts as stats,
// how current the books are as the context line, and the decisions waiting, grouped by client with the most pressing
// close first. It reads what Home reads, in one snapshot.
export default function Closes() {
  const queryClient = useQueryClient();
  const dates = useDates();
  const homeRead = useQuery({ ...workspaceQuery<HomeCounts>("/home"), refetchInterval: 30_000 });
  const counts = homeRead.data;
  const error = homeRead.error ? homeRead.error.message || "Closes are unavailable." : "";
  const tick = Math.max(homeRead.dataUpdatedAt, homeRead.errorUpdatedAt);
  const [view, setView] = useState<View>("all");
  const bump = () => void queryClient.invalidateQueries({ queryKey: ["workspace"] });
  const today = (counts?.as_of ?? new Date().toISOString()).slice(0, 10);
  const d = counts?.deadlines;
  const missed = d ? d.overdue + d.not_opened_overdue : 0;
  const soon = d ? d.due_soon + d.not_opened_upcoming : 0;
  const undated = counts ? counts.owed.filter((item) => item.state !== "signed_off" && !item.due_on).length + counts.not_opened.filter((m) => !m.due_on).length : 0;
  const reopened = [...(counts?.owed.filter((item) => item.state === "reopen_required" || item.state === "unknown") ?? []), ...(counts?.reopened_later ?? [])];
  const context = counts
    ? [
        `Books captured for ${counts.books.captured} of ${counts.books.of}`,
        counts.books.exceptions ? n(counts.books.exceptions, "new exception") + " in the books" : "",
        counts.books.unchecked ? n(counts.books.unchecked, "capture") + " not fully checked" : "",
        `counted at ${dates.time(counts.as_of)}`,
      ]
        .filter(Boolean)
        .join(" · ")
    : "Your clients' closes, and what they wait on.";
  return (
    <section className="bare" aria-label="Closes">
      <PageHeader title="Closes">{context}</PageHeader>
      {error && <p role="alert">{error}</p>}
      <div className="ks-stats">
        <div className="ks-stat">
          <span className="ks-l">Signed off</span>
          <span className="ks-v">
            <b>
              {counts?.signed_off.done ?? "–"}
              <small> of {counts?.signed_off.total ?? "–"}</small>
            </b>
          </span>
          <span className="ks-c">closes owed through this month</span>
        </div>
        <div className="ks-stat">
          <span className="ks-l">Need your decision</span>
          <span className="ks-v">
            <b>{counts?.decisions.count ?? "–"}</b>
            {counts && counts.decisions.unread > 0 && <span className="pill muted">{counts.decisions.unread} new</span>}
          </span>
          <span className="ks-c">{counts?.decisions.team ? `${counts.decisions.team} for teammates` : "across the closes you can open"}</span>
        </div>
        <div className="ks-stat">
          <span className="ks-l">Deadline exceptions</span>
          <span className="ks-v">
            <b>{counts ? missed + soon + undated : "–"}</b>
            {missed > 0 && <span className="pill danger">{missed} overdue</span>}
          </span>
          <span className="ks-c">{d ? `${n(soon, "close")} due this week · ${undated} without a date` : "overdue, due this week, or undated"}</span>
        </div>
        <div className="ks-stat">
          <span className="ks-l">Prepared by the agent</span>
          <span className="ks-v">
            <b>
              {counts?.prepared.done ?? "–"}
              <small> of {counts?.prepared.total ?? "–"}</small>
            </b>
            {counts && counts.prepared.failed > 0 && <span className="pill danger">{counts.prepared.failed} failed</span>}
          </span>
          <span className="ks-c">{counts ? `${counts.prepared.waiting_person} waiting on you · ${counts.prepared.waiting_client} on clients` : "this month"}</span>
        </div>
      </div>
      <Decisions today={today} tick={tick} snapshot={counts?.inbox} reopened={reopened} canReview={counts?.can_review ?? false} teamTotal={counts?.decisions.team ?? 0} view={view} onView={setView} onChange={bump} />
    </section>
  );
}
