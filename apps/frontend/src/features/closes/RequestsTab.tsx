import { useEffect, useRef, useState } from "react";
import { Link, useLocation, useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Icon } from "@close/ui/components/Icon";
import { Segmented } from "@close/ui/components/Segmented";
import { clientPage } from "../../data/clients";
import { decisionRefreshMs, workspaceQuery } from "../../data/queries";
import { useDates } from "../../data/time";
import { EmailReplies } from "./EmailReplies";
import type { Status } from "./Goal";
import { AgentPanel, Glance, MoreLinks, NeedsYou, type Figure, type Need } from "./Overview";
import { Requests } from "./Requests";
import type { Brief } from "./Stands";
import type { Close, Paper } from "./WorkpaperView";

// A close's Requests (docs/design-system.md, Close page): where the requests to the client stand in one sentence, then
// the decisions on this tab and the requests as one table, each with its recipient, when it reached the client and where
// it stands; the rail counts what is held and waiting and opens the tab's other views (drafting a request, email replies).

// An approved request and where it stands (GET /{id}/requests/approved).
type Approved = {
  id: string;
  body: string;
  recipient: string;
  approved_at: string;
  sent_at: string | null;
  email: "" | "failed" | "sending";
  answer: "" | "answered" | "answered_in_part";
  held: boolean;
  link: { used_at: string | null; revoked_at: string | null; expires_at: string } | null;
  job: string;
};
type Proposed = { proposal: { id: string; recipient: string; body: string; questions: { question: string }[] } | null };
// Read on arriving at the tab and on returning from one of its views (a draft approved there is a request now), then
// within the decision visibility target.
export const useApproved = (closeId: string) => useQuery({ ...workspaceQuery<Approved[]>(`/${closeId}/requests/approved`), refetchOnMount: "always", refetchInterval: decisionRefreshMs, refetchOnWindowFocus: true });
const useProposed = (closeId: string) => useQuery({ ...workspaceQuery<Proposed>(`/${closeId}/requests/proposed`), refetchInterval: decisionRefreshMs, refetchOnWindowFocus: true });

const words = ["no", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten"];
const capital = (text: string) => text[0].toUpperCase() + text.slice(1);
const w = (n: number) => words[n] ?? String(n);
const day = 86_400_000;

// A request's title: its question, or how many it asks and the first, from its numbered lines.
function titleOf(body: string) {
  const lines = body.split("\n").map((line) => line.trim()).filter(Boolean);
  const numbered = lines.filter((line) => /^\d+[.)]\s+/.test(line)).map((line) => line.replace(/^\d+[.)]\s+/, ""));
  const first = numbered[0] ?? lines[0] ?? "Request";
  const text = numbered.length > 1 ? `${numbered.length} questions · ${first}` : first;
  return text.length > 90 ? `${text.slice(0, 89)}…` : text;
}

// Where a request stands, in the close's words, its tone and the filter group it belongs to. A request that reached the
// client stays sent whatever became of a later reminder.
function requestStatus(q: Approved, now: number): [string, string, string] {
  if (q.link?.revoked_at) return ["Withdrawn", "neutral", "closed"];
  // A reply held for review needs the person even after the client answered through the link.
  if (q.held) return ["Reply held for review", "muted", "held"];
  if (q.answer === "answered") return ["Answered", "", "answered"];
  // Delivery first: a request that never reached the client says so, whatever became of its link. Sending has not
  // reached it either, so it is not yet a wait on the client.
  // An expired link ends the client's way to answer, partly answered or not; on a request not yet with the client it
  // means a new link is needed before it is shared.
  const lapsed = !!q.link && new Date(q.link.expires_at).getTime() <= now;
  if (!q.sent_at && q.email === "failed") return [`Not delivered${lapsed ? " · link expired" : ""}`, "danger", "attention"];
  if (!q.sent_at && q.email === "sending") return [`Sending${lapsed ? " · link expired" : ""}`, lapsed ? "danger" : "live", "attention"];
  if (!q.sent_at) return [`Approved · not sent${lapsed ? " · link expired" : ""}`, lapsed ? "danger" : "muted", "attention"];
  if (lapsed) return [q.answer === "answered_in_part" ? "Partly answered · link expired" : "Link expired", "danger", "expired"];
  if (q.answer === "answered_in_part") return ["Partly answered", "muted", "waiting"];
  const days = Math.floor((now - new Date(q.sent_at).getTime()) / day);
  return [`Waiting for client · ${days} day${days === 1 ? "" : "s"}`, "muted", "waiting"];
}
const groups: [string, string][] = [
  ["held", "Reply held"],
  ["attention", "Not with the client"],
  ["expired", "Link expired"],
  ["waiting", "Waiting for client"],
  ["answered", "Answered"],
  ["closed", "Withdrawn"],
];

// RequestsLede says where the requests stand: a reply held for review, requests that have not reached the client, how
// many wait on it, a draft waiting for approval, or that every one is answered; under it to whom, and the longest wait.
export function RequestsLede({ closeId }: { closeId: string }) {
  const read = useApproved(closeId);
  const proposed = useProposed(closeId).data?.proposal;
  const now = Date.now();
  const list = read.data ?? [];
  const by = (group: string) => list.filter((q) => requestStatus(q, now)[2] === group);
  const held = by("held").length;
  const attention = by("attention").length;
  const expired = by("expired").length;
  const waiting = by("waiting");
  const sentence = !read.data
    ? read.isError
      ? "The requests could not be read."
      : "Requests to the client."
    : held
      ? held === 1
        ? `The reply to ${by("held")[0].recipient} is held until you check who sent it.`
        : `${capital(w(held))} replies are held until you check who sent them.`
      : attention
        ? `${capital(w(attention))} request${attention === 1 ? " has" : "s have"} not reached the client.`
        : expired
          ? `${capital(w(expired))} request${expired === 1 ? "'s link has" : "s' links have"} expired.`
          : waiting.length
          ? `${capital(w(waiting.length))} request${waiting.length === 1 ? " waits" : "s wait"} on the client.`
          : proposed
            ? "A drafted request waits for your approval."
            : !list.length
              ? "No requests yet."
              : !by("closed").length
                ? "Every request is answered."
                : !by("answered").length
                  ? `${capital(w(by("closed").length))} request${by("closed").length === 1 ? " was" : "s were"} withdrawn; nothing waits on the client.`
                  : "Every request is answered or withdrawn.";
  const recipients = [...new Set(list.filter((q) => !q.link?.revoked_at).map((q) => q.recipient))];
  const oldest = waiting.filter((q) => q.sent_at).map((q) => Math.floor((now - new Date(q.sent_at!).getTime()) / day));
  return (
    <div className="ks-lede">
      <h1>{sentence}</h1>
      <p>{[recipients.length ? `To ${recipients.join(", ")}` : "No recipient yet", oldest.length ? `the oldest has waited ${Math.max(...oldest)} day${Math.max(...oldest) === 1 ? "" : "s"}` : ""].filter(Boolean).join(" · ")}</p>
    </div>
  );
}

// RequestsTable is the tab's one table: the drafted request waiting for approval, then every approved request, newest
// first, with its recipient, when it reached the client and where it stands. A row opens the work it is handled in.
function RequestsTable({ approved, proposed, base }: { approved: Approved[]; proposed: Proposed["proposal"] | undefined; base: string }) {
  const dates = useDates();
  const [filter, setFilter] = useState("all");
  const now = Date.now();
  const rows = approved.map((q) => ({ q, status: requestStatus(q, now) }));
  const counts = new Map(groups.map(([key]) => [key, rows.filter((row) => row.status[2] === key).length]));
  const active = counts.get(filter) ? filter : "all";
  const shown = active === "all" ? rows : rows.filter((row) => row.status[2] === active);
  return (
    <section className="bare ks-set" aria-labelledby="requests-title">
      <div className="ks-sec">
        <h2 id="requests-title">Requests</h2>
        <small>this close</small>
        <div className="ks-end">
          {rows.length > 0 && <Segmented label="Filter requests" items={[{ key: "all", label: "All", count: rows.length }, ...groups.filter(([key]) => counts.get(key)).map(([key, label]) => ({ key, label, count: counts.get(key) }))]} value={active} onChange={setFilter} />}
          <Link className="button" to={`${base}?tab=requests&view=draft`}>
            New request
          </Link>
        </div>
      </div>
      <div className="ks-table ks-requests" role="table" aria-labelledby="requests-title">
        <div className="ks-tr head" role="row">
          <span role="columnheader">Request</span>
          <span role="columnheader">To</span>
          <span role="columnheader">Sent</span>
          <span role="columnheader">Status</span>
          <span role="columnheader">
            <span className="visually-hidden">Open</span>
          </span>
        </div>
        {proposed && active === "all" && (
          <div className="ks-tr linked" role="row">
            <span className="ks-who" role="cell">
              <Link className="ks-rowlink" to={`${base}?tab=requests&view=draft`}>
                {proposed.questions.length === 1 ? proposed.questions[0].question : `${proposed.questions.length} questions · ${proposed.questions[0]?.question ?? ""}`}
              </Link>
            </span>
            <span className="ks-mut" role="cell">
              {proposed.recipient || "—"}
            </span>
            <span className="ks-mut" role="cell">
              —
            </span>
            <span role="cell">
              <span className="pill muted">Draft · needs your decision</span>
            </span>
            <span role="cell">
              <Icon name="arrow" className="ks-go" />
            </span>
          </div>
        )}
        {shown.map(({ q, status: [label, tone] }) => (
          <div className="ks-tr linked" role="row" key={q.id}>
            <span className="ks-who" role="cell">
              <Link className="ks-rowlink" to={q.job ? `${base}?job=${q.job}` : `${base}#coverage-work`} title={q.body}>
                {titleOf(q.body)}
                <small> · #{q.id}</small>
              </Link>
            </span>
            <span className="ks-mut" role="cell">
              {q.recipient}
            </span>
            <span className="ks-mut" role="cell">
              {q.sent_at ? dates.dayMonth(q.sent_at) : "—"}
            </span>
            <span role="cell">
              <span className={`pill ${tone}`}>{label}</span>
            </span>
            <span role="cell">
              <Icon name="arrow" className="ks-go" />
            </span>
          </div>
        ))}
        {rows.length === 0 && !proposed && <p className="ks-empty-row">No requests yet. The agent drafts one when a document or an account needs the client; or write one yourself.</p>}
      </div>
    </section>
  );
}

const views: Record<string, string> = { draft: "Draft a request", replies: "Email replies" };
const requestsView = (asked: string | null, hash: string) => (asked && asked in views ? asked : hash === "#client-request" ? "draft" : "");

// RequestsTab is the tab's body: on the left what needs the person here and the requests, or one of the tab's views; on
// the right what is held and waiting, the agent and the tab's other views.
export function RequestsTab({ close, papers, needs, brief, briefRead, decisionsError, status, base, onChange }: { close: Close; papers: Paper[]; needs: Need[]; brief: Brief | undefined; briefRead: { dataUpdatedAt: number; isFetching: boolean; refetch: () => unknown }; decisionsError: string; status: Status | undefined; base: string; onChange: () => void }) {
  const [params] = useSearchParams();
  const location = useLocation();
  const view = requestsView(params.get("view"), location.hash);
  const approved = useApproved(close.id);
  const proposed = useProposed(close.id).data?.proposal;
  // Back on the table from a view the component stays mounted, so the list is read again here.
  const { refetch } = approved;
  const previous = useRef(view);
  useEffect(() => {
    if (previous.current && !view) void refetch();
    previous.current = view;
  }, [view, refetch]);
  const now = Date.now();
  const list = approved.data ?? [];
  const by = (group: string) => list.filter((q) => requestStatus(q, now)[2] === group);
  const waiting = by("waiting").filter((q) => q.sent_at);
  const longest = waiting.length ? Math.max(...waiting.map((q) => Math.floor((now - new Date(q.sent_at!).getTime()) / day))) : 0;
  const figures: Figure[] = [
    ...(by("held").length ? ([[String(by("held").length), by("held").length === 1 ? "reply held for review" : "replies held for review", "att"]] as Figure[]) : []),
    ...(by("attention").length ? ([[String(by("attention").length), "not with the client", "att"]] as Figure[]) : []),
    ...(by("waiting").length ? ([[String(by("waiting").length), by("waiting").length === 1 ? "request waiting for client" : "requests waiting for client"]] as Figure[]) : []),
    ...(longest ? ([[`${longest} day${longest === 1 ? "" : "s"}`, "longest wait"]] as Figure[]) : []),
    ...(by("answered").length ? ([[String(by("answered").length), "answered"]] as Figure[]) : []),
  ].slice(0, 4);
  const to = (name: string) => `${base}?tab=requests&view=${name}`;
  const lines = brief ? { ...brief, lines: brief.lines.filter((line) => line.tab === "requests") } : undefined;
  return (
    <div className="ks-close-grid">
      <div className="ks-col">
        {view ? (
          <>
            <p className="ks-view-crumbs">
              <Link to={`${base}?tab=requests`}>Requests</Link>
              <span>/</span>
              <b>{views[view]}</b>
            </p>
            <div className="stack">
              {view === "draft" && <Requests closeId={close.id} />}
              {view === "replies" && <EmailReplies closeId={close.id} onReleased={onChange} open />}
            </div>
          </>
        ) : (
          <>
            <NeedsYou key={close.id} closeId={close.id} papers={papers} needs={needs} teammates={0} brief={lines} briefRead={briefRead} decisionsError={decisionsError} base={base} preparing={false} />
            {approved.isError && !approved.data ? <p role="alert">{approved.error.message || "The requests could not be read."}</p> : <RequestsTable approved={list} proposed={proposed} base={base} />}
          </>
        )}
      </div>
      <aside className="ks-rail" aria-label="About the requests">
        <Glance figures={figures} />
        <AgentPanel close={close} status={status} />
        <MoreLinks
          title="More on Requests"
          links={[
            { label: views.draft, note: proposed ? "a draft waits" : undefined, to: to("draft") },
            { label: views.replies, note: by("held").length ? `${by("held").length} held` : undefined, to: to("replies") },
            { label: "Requests across the client's months", to: clientPage(close.entity) },
          ]}
        />
      </aside>
    </div>
  );
}
