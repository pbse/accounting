import { useEffect, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { workspaceQuery } from "../../data/queries";
import { useNavigate } from "react-router";

export type AttentionEvent = { key: string; kind: string; yours: boolean; text: string; at: string; workpaper_id?: string; job_id?: string; account_id?: string; read: boolean; snoozed_until?: string; info?: boolean; decisions?: number };
type AttentionGroup = { close_id: string; entity: string; period: string; due_on: string; priority: string; owner: "mine" | "other" | "none"; events: AttentionEvent[] };
export type AttentionInbox = { groups: AttentionGroup[]; unread: number; unread_keys: string[]; page: number; has_more: boolean; kinds: string[]; preferences: { kinds: string[]; browser: boolean }; version?: string };

// The one inbox read the shell makes: the reader's unread items by priority, which carries the count, the keys and the
// preferences the badge needs and the notices the sidebar shows. The page's loader prefetches it.
export const attentionPath = "/attention?view=unread&order=priority";

export const kindLabels: Record<string, string> = {
  answered: "Client replies",
  confirm: "Facts to confirm",
  failed: "Failed preparation",
  approve: "Drafts ready for approval",
  changed: "Changed sources",
  fix: "Requested changes",
  request: "Drafted client requests to approve",
  release: "Client files and emails to release",
  send: "Approved requests with no delivery",
  email: "Undelivered request emails",
  unanswered: "Unanswered requests to follow up",
  goal: "Delegated closes that need you",
  handoff: "Work handed to you",
};

// Where an event's decision is, the same from Home, the decision list and the sidebar's notices: its draft, its job, its
// coverage work, or the part of the close that holds it (a drafted request in Requests' draft, a reply or request on Requests, a held email reply among the
// email replies, a client's scanned file in coverage, a forwarded file among Documents' forwarded files, the goal in the agent's details).
export function eventTarget(closeId: string, e: AttentionEvent) {
  const base = `/dashboard/closes/${closeId}`;
  const prefix = e.key.slice(0, e.key.indexOf(":"));
  if (e.workpaper_id) return `${base}/workpapers/${e.workpaper_id}`;
  if (e.job_id) return `${base}?job=${e.job_id}`;
  if (e.account_id || e.kind === "unanswered" || prefix === "release") return `${base}#coverage-work`;
  if (prefix === "held") return `${base}?tab=requests&view=replies`;
  if (prefix === "forwarded") return `${base}?tab=evidence&view=add#forwarded-${e.key.slice(e.key.indexOf(":") + 1)}`;
  if (e.kind === "request") return `${base}?tab=requests&view=draft`;
  if (e.kind === "answered" || e.kind === "send" || e.kind === "email") return `${base}?tab=requests`;
  if (e.kind === "goal") return `${base}#agent-details`;
  return base;
}

// The sidebar's unread count on Home. With the member's consent, newly seen items also raise a browser notification
// that names no client.
export function AttentionBadge() {
  // Read with the page (the layout's loader), then every minute, in a background tab too (that is when a
  // notification matters), and again on returning to the tab.
  const inbox = useQuery({ ...workspaceQuery<AttentionInbox>(attentionPath), refetchInterval: 60000, refetchIntervalInBackground: true, refetchOnWindowFocus: true });
  const unread = inbox.data?.unread ?? 0;
  // A failed read is not zero, even with an earlier count cached: the badge says it could not be read.
  const failed = inbox.isError;
  const seen = useRef<Set<string>>(undefined);
  const navigate = useNavigate();
  const go = useRef(navigate);
  go.current = navigate;
  useEffect(() => {
    const read = inbox.data;
    if (!read) return;
    const fresh = seen.current ? read.unread_keys.filter((k) => !seen.current!.has(k)) : [];
    seen.current = new Set([...(seen.current ?? []), ...read.unread_keys]);
    if (fresh.length && read.preferences.browser && "Notification" in window && Notification.permission === "granted") {
      const note = new Notification("Close", { body: `${fresh.length} new ${fresh.length === 1 ? "item needs" : "items need"} your attention` });
      note.onclick = () => {
        window.focus();
        go.current("/dashboard/closes#decisions");
      };
    }
  }, [inbox.data]);
  if (failed) return <span className="ks-count" aria-label="New items could not be read" title="New items could not be read">–</span>;
  return unread > 0 ? <span className="ks-count" aria-label={`${unread} new`} title={`${unread} new ${unread === 1 ? "decision" : "decisions"}`}>{unread}</span> : null;
}
