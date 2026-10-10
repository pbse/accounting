import { useEffect, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Icon, type IconName } from "@close/ui/components/Icon";
import { SidebarCategory } from "@close/ui/components/Sidebar";
import { workspaceQuery } from "../../data/queries";
import { workspaceRequest } from "../closes/workspace";
import { attentionPath, type AttentionInbox } from "../closes/Attention";

// Needs you: what the agent noticed, one line each, from the attention inbox the Home badge already reads. Taking one
// marks it read, slides it out and opens its close; All notices opens Home's decisions, where read ones stay.
type Kind = "doc" | "reply" | "wait" | "done";
const kinds: Record<string, Kind> = { answered: "reply", handoff: "reply", fix: "reply", approve: "done", goal: "done", changed: "doc", release: "doc", confirm: "wait", unanswered: "wait", send: "wait", email: "wait", failed: "wait" };
const icons: Record<Kind, IconName> = { doc: "file", reply: "mail", wait: "waiting", done: "review" };

export function SideNotices() {
  // The same read as the badge: the unread view by priority, so the four notices are the person's most pressing even
  // when newer read or teammate events fill the inbox's first page.
  const inbox = useQuery({ ...workspaceQuery<AttentionInbox>(attentionPath), refetchInterval: 60000, refetchOnWindowFocus: true });
  const navigate = useNavigate();
  const location = useLocation();
  const queries = useQueryClient();
  // Live: one long-lived request says when the person's unread items change, and the inbox is read again then. The
  // request ends on the server's schedule and is opened again; a failure waits a while before trying again. The
  // 60-second read above stands behind it.
  // Each reading the stream sends carries a version. The very first is a snapshot, read again only when it differs
  // from what is held here; from then on, across reconnects too, a reading is followed only when its version moved.
  const seen = useRef("");
  useEffect(() => {
    const controller = new AbortController();
    let timer: number | undefined;
    const listen = async () => {
      try {
        await workspaceRequest(
          "/attention/stream",
          undefined,
          false,
          (event) => {
            const change = event as { type: string; version?: string; unread_keys?: string[] };
            if (change.type !== "attention") return;
            const version = change.version ?? "";
            if (seen.current === "") {
              seen.current = version;
              const held = queries.getQueryData<AttentionInbox>(workspaceQuery(attentionPath).queryKey)?.unread_keys ?? [];
              if (held.join("\n") === (change.unread_keys ?? []).join("\n")) return;
            } else if (version === seen.current) return;
            else seen.current = version;
            // A change reaches the inbox and Home's and Closes' counts and decisions alike, which read /home.
            void queries.invalidateQueries({ predicate: (q) => q.queryKey[0] === "workspace" && /^\/(attention|home)\b/.test(String(q.queryKey[1])) });
          },
          controller.signal,
        );
        // A normal ending (the server's final document) is followed at once; a failure waits.
        if (!controller.signal.aborted) timer = window.setTimeout(listen, 1000);
      } catch {
        if (!controller.signal.aborted) timer = window.setTimeout(listen, 30_000);
      }
    };
    void listen();
    return () => {
      controller.abort();
      window.clearTimeout(timer);
    };
  }, [queries]);
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
      // The notice opens whether or not the mark saves; a failed mark is said, and the notice comes back.
      navigate(to);
      workspaceRequest("/attention", { action: "read", keys: [key] })
        .catch((e: unknown) => setFailed(e instanceof Error ? e.message : "The notice could not be marked read."))
        .then(() => setLeaving((l) => l.filter((k) => k !== key)))
        // Home's counts and decisions come from /home, so they refresh with the inbox.
        .finally(() => void queries.invalidateQueries({ predicate: (q) => q.queryKey[0] === "workspace" && /^\/(attention|home)\b/.test(String(q.queryKey[1])) }));
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
      <button
        type="button"
        className="ks-more"
        onClick={() => {
          // Closes' decisions, where read notices stay: scrolled to when already there, opened otherwise.
          const list = location.pathname === "/dashboard/closes" ? document.getElementById("decisions") : null;
          if (list) list.scrollIntoView({ block: "start", behavior: "smooth" });
          else navigate("/dashboard/closes#decisions");
        }}
      >
        All notices
      </button>
    </div>
  );
}
