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
  const inbox = useQuery({ ...workspaceQuery<AttentionInbox>("/attention"), refetchInterval: 60000, refetchOnWindowFocus: true });
  const navigate = useNavigate();
  const queries = useQueryClient();
  const [leaving, setLeaving] = useState<string[]>([]);
  const notices = (inbox.data?.groups ?? [])
    .flatMap((g) => g.events.filter((e) => !e.read && !e.snoozed_until).map((e) => ({ key: e.key, kind: kinds[e.kind] ?? "doc", text: `${g.entity}: ${e.text}`, close: g.close_id })))
    .slice(0, 4);
  const unread = inbox.data?.unread ?? 0;
  function take(key: string, close: string) {
    setLeaving((l) => [...l, key]);
    window.setTimeout(() => {
      void workspaceRequest("/attention", { action: "read", keys: [key] })
        .catch(() => undefined)
        .finally(() => void queries.invalidateQueries({ queryKey: workspaceQuery("/attention").queryKey }));
      navigate(`/dashboard/closes/${close}`);
    }, 220);
  }
  return (
    <div className="ks-section">
      <SidebarCategory aside={unread > 0 ? <span className="ks-n">{unread}</span> : undefined}>Needs you</SidebarCategory>
      <div className="ks-notes">
        {notices.map((n) => (
          <button type="button" className={`ks-note${leaving.includes(n.key) ? " leaving" : ""}`} key={n.key} title={n.text} onClick={() => take(n.key, n.close)}>
            <span className={`ks-tile ${n.kind}`}>
              <Icon name={icons[n.kind]} size={14} />
            </span>
            <span className="ks-t">{n.text}</span>
            <Icon name="arrow" className="ks-go" size={14} />
          </button>
        ))}
        {notices.length === 0 && <div className="ks-notes-empty">{inbox.isError ? "Notices could not be read" : "Nothing needs you"}</div>}
      </div>
      <Link className="ks-more" to="/dashboard#decisions">
        All notices
      </Link>
    </div>
  );
}
