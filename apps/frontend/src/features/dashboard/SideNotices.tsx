import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Icon, type IconName } from "@close/ui/components/Icon";
import { SidebarCategory } from "@close/ui/components/Sidebar";
import { workspaceQuery } from "../../data/queries";
import { workspaceRequest } from "../closes/workspace";
import type { AttentionInbox } from "../closes/Attention";

// Needs you: what the agent noticed, one line each, from the attention inbox the Home badge already reads. Taking one
// marks it read, slides it out and opens its close; All notices opens Home's decisions, where read ones stay.
type Kind = "doc" | "reply" | "wait" | "done";
const kinds: Record<string, Kind> = { answered: "reply", handoff: "reply", fix: "reply", approve: "done", goal: "done", changed: "doc", release: "doc", confirm: "wait", unanswered: "wait", send: "wait", email: "wait", failed: "wait" };
const icons: Record<Kind, IconName> = { doc: "file", reply: "mail", wait: "waiting", done: "review" };

export function SideNotices() {
  // The unread view by priority, so the four notices are the person's most pressing even when newer read or teammate
  // events fill the inbox's first page; the count comes with it.
  const path = "/attention?view=unread&order=priority";
  const inbox = useQuery({ ...workspaceQuery<AttentionInbox>(path), refetchInterval: 60000, refetchOnWindowFocus: true });
  const navigate = useNavigate();
  const queries = useQueryClient();
  const [leaving, setLeaving] = useState<string[]>([]);
  const [failed, setFailed] = useState("");
  // The reader's own unread decisions, as the badge counts them; what waits on a teammate stays on Home.
  const notices = (inbox.data?.groups ?? [])
    .flatMap((g) =>
      g.events
        .filter((e) => e.yours && !e.read && !e.snoozed_until)
        .map((e) => ({
          key: e.key,
          kind: kinds[e.kind] ?? "doc",
          text: `${g.entity}: ${e.text}`,
          // The same target Home's decision list opens: the draft, the job, the coverage work, or the close.
          to: e.workpaper_id ? `/dashboard/closes/${g.close_id}/workpapers/${e.workpaper_id}` : `/dashboard/closes/${g.close_id}${e.job_id ? `?job=${e.job_id}` : e.account_id || e.kind === "unanswered" ? "#coverage-work" : ""}`,
        })),
    )
    .slice(0, 4);
  const unread = inbox.data?.unread ?? 0;
  function take(key: string, to: string) {
    setFailed("");
    setLeaving((l) => [...l, key]);
    window.setTimeout(() => {
      workspaceRequest("/attention", { action: "read", keys: [key] })
        .then(() => navigate(to))
        .catch((e: unknown) => setFailed(e instanceof Error ? e.message : "The notice could not be marked read."))
        // Read or not, the key leaves the leaving state: a notice marked new again on Home must show again.
        .then(() => setLeaving((l) => l.filter((k) => k !== key)))
        .finally(() => void queries.invalidateQueries({ predicate: (q) => q.queryKey[0] === "workspace" && String(q.queryKey[1]).startsWith("/attention") }));
    }, 220);
  }
  return (
    <div className="ks-section">
      <SidebarCategory aside={unread > 0 ? <span className="ks-n">{unread}</span> : undefined}>Needs you</SidebarCategory>
      <div className="ks-notes">
        {notices.map((n) => (
          <button type="button" className={`ks-note${leaving.includes(n.key) ? " leaving" : ""}`} key={n.key} title={n.text} onClick={() => take(n.key, n.to)}>
            <span className={`ks-tile ${n.kind}`}>
              <Icon name={icons[n.kind]} size={14} />
            </span>
            <span className="ks-t">{n.text}</span>
            <Icon name="arrow" className="ks-go" size={14} />
          </button>
        ))}
        {notices.length === 0 && <div className="ks-notes-empty">{inbox.isPending ? "Reading…" : inbox.isError ? "Notices could not be read" : "Nothing needs you"}</div>}
      </div>
      {inbox.isError && notices.length > 0 && <p className="ks-empty">Not refreshed since {new Date(inbox.dataUpdatedAt).toLocaleTimeString([], { timeStyle: "short" })}.</p>}
      {failed && (
        <p className="ks-empty" role="alert">
          {failed}
        </p>
      )}
      <Link className="ks-more" to="/dashboard#decisions">
        All notices
      </Link>
    </div>
  );
}
