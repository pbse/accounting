import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router";
import { workspaceRequest } from "./workspace";

export type AttentionEvent = { key: string; kind: string; yours: boolean; text: string; at: string; workpaper_id?: string; job_id?: string; account_id?: string; read: boolean; snoozed_until?: string };
export type AttentionGroup = { close_id: string; entity: string; period: string; due_on: string; priority: string; owner: "mine" | "other" | "none"; events: AttentionEvent[] };
export type AttentionInbox = { groups: AttentionGroup[]; unread: number; unread_keys: string[]; page: number; has_more: boolean; kinds: string[]; preferences: { kinds: string[]; browser: boolean } };

export const kindLabels: Record<string, string> = {
  answered: "Client replies",
  confirm: "Facts to confirm",
  failed: "Failed preparation",
  approve: "Drafts ready for approval",
  changed: "Changed sources",
  fix: "Requested changes",
  release: "Client files and emails to release",
  send: "Approved requests with no delivery",
  email: "Undelivered request emails",
  unanswered: "Unanswered requests to follow up",
  goal: "Delegated closes that need you",
  handoff: "Work handed to you",
};

// The top bar's unread count on Home. With the member's consent, newly seen items also raise a browser notification
// that names no client.
export function AttentionBadge() {
  const [unread, setUnread] = useState(0);
  const seen = useRef<Set<string>>(undefined);
  const navigate = useNavigate();
  const go = useRef(navigate);
  go.current = navigate;
  useEffect(() => {
    let live = true;
    const poll = () =>
      workspaceRequest("/attention")
        .then((inbox: AttentionInbox) => {
          if (!live) return;
          setUnread(inbox.unread);
          const fresh = seen.current ? inbox.unread_keys.filter((k) => !seen.current!.has(k)) : [];
          seen.current = new Set([...(seen.current ?? []), ...inbox.unread_keys]);
          if (fresh.length && inbox.preferences.browser && "Notification" in window && Notification.permission === "granted") {
            const note = new Notification("Close", { body: `${fresh.length} new ${fresh.length === 1 ? "item needs" : "items need"} your attention` });
            note.onclick = () => {
              window.focus();
              go.current("/dashboard");
            };
          }
        })
        .catch(() => {});
    void poll();
    const timer = setInterval(poll, 60000);
    return () => {
      live = false;
      clearInterval(timer);
    };
  }, []);
  return unread > 0 ? <span className="pill" aria-label={`${unread} unread`}>{unread}</span> : null;
}
