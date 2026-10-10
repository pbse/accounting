import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { PageHeader } from "@close/ui/components/PageHeader";
import { SectionHeader } from "@close/ui/components/SectionHeader";
import { workspaceQuery } from "../../data/queries";
import { useDates } from "../../data/time";
import { ClientFilter, Decisions, type HomeCounts, type View } from "../home/Home";
import { DailyBooks } from "../home/DailyBooks";
import { Dependencies } from "../home/Dependencies";
import { BulkReview, type QueueItem } from "../review/BulkReview";
import { DelegatedCloses } from "./Goal";
import { NotOpened } from "./Morning";

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
  // The review queue is a capped read; a client or month filter reaches the rest. Once a batch was approved here,
  // the section stays so its outcomes can be read after the queue empties.
  const [queueClient, setQueueClient] = useState("");
  const queueRead = useQuery({ ...workspaceQuery<{ items: QueueItem[]; count: number }>(`/review-queue?client=${encodeURIComponent(queueClient)}`), refetchInterval: 30_000 });
  const queue = queueRead.data;
  const queueError = queueRead.error ? queueRead.error.message || "The review queue is unavailable." : "";
  const [reviewed, setReviewed] = useState(false);
  const bump = () => void queryClient.invalidateQueries({ queryKey: ["workspace"] });
  const today = (counts?.as_of ?? new Date().toISOString()).slice(0, 10);
  const d = counts?.deadlines;
  const missed = d ? d.overdue + d.not_opened_overdue : 0;
  const soon = d ? d.due_soon + d.not_opened_upcoming : 0;
  const undated = counts ? counts.owed.filter((item) => item.state !== "signed_off" && !item.due_on).length + counts.not_opened.filter((m) => !m.due_on).length : 0;
  const reopened = [...(counts?.owed.filter((item) => item.state === "reopen_required" || item.state === "unknown") ?? []), ...(counts?.reopened_later ?? [])];
  const context = counts
    ? [
        `Books captured for ${counts.books.captured} of ${counts.books.of}${counts.books.exported ? ` · ${counts.books.exported} from exports` : ""}`,
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
      <Dependencies />
      <DailyBooks canReview={counts?.can_review ?? false} onReviewed={bump} />
      {(queueError || queueClient || reviewed || (queue && queue.count > 0)) && (
        <>
          <SectionHeader title="Ready for your review" id="review-queue">
            {(queueClient || (queue && queue.count > queue.items.length)) && <ClientFilter value={queueClient} onChange={(next) => next !== queueClient && setQueueClient(next)} shown={queue?.items.length ?? 0} of={queue?.count ?? 0} />}
          </SectionHeader>
          {queueError && (
            <p role="alert">
              {queueError}
              {queue ? " Showing the last queue read." : ""}
            </p>
          )}
          {queueRead.isPending ? (
            <p role="status" className="empty">
              Reading the queue…
            </p>
          ) : (
            <BulkReview
              items={queue?.items}
              onDone={() => {
                setReviewed(true);
                bump();
              }}
            />
          )}
        </>
      )}
      <DelegatedCloses client="" />
      {counts && <NotOpened months={counts.not_opened} />}
    </section>
  );
}
