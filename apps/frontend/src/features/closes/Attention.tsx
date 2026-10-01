import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router";
import { workspaceRequest } from "./workspace";
import { SectionHeader } from "../../ui/SectionHeader";

type Event = { key: string; kind: string; text: string; at: string; workpaper_id?: string; job_id?: string; account_id?: string; read: boolean; snoozed_until?: string };
type Group = { close_id: string; entity: string; period: string; due_on: string; priority: string; owner: "mine" | "other" | "none"; events: Event[] };
type Inbox = { groups: Group[]; unread: number; unread_keys: string[]; page: number; has_more: boolean; kinds: string[]; preferences: { kinds: string[]; browser: boolean } };

const kindLabels: Record<string, string> = {
  answered: "Client replies",
  confirm: "Facts to confirm",
  failed: "Failed preparation",
  approve: "Drafts ready for approval",
  changed: "Changed sources",
  fix: "Requested changes",
  release: "Client files and emails to release",
  send: "Approved requests with no delivery",
  email: "Undelivered request emails",
  goal: "Delegated closes that need you",
  handoff: "Documents handed to you",
};
const views = { all: "All", mine: "Assigned to me", unread: "Unread", snoozed: "Snoozed" } as const;

// The sidebar's unread count. With the member's consent, newly seen items also raise a browser notification that names no client.
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
        .then((inbox: Inbox) => {
          if (!live) return;
          setUnread(inbox.unread);
          const fresh = seen.current ? inbox.unread_keys.filter((k) => !seen.current!.has(k)) : [];
          seen.current = new Set([...(seen.current ?? []), ...inbox.unread_keys]);
          if (fresh.length && inbox.preferences.browser && "Notification" in window && Notification.permission === "granted") {
            const note = new Notification("Close", { body: `${fresh.length} new ${fresh.length === 1 ? "item needs" : "items need"} your attention` });
            note.onclick = () => {
              window.focus();
              go.current("/dashboard/attention");
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

function Preferences({ inbox, onSaved }: { inbox: Inbox; onSaved: () => void }) {
  const [kinds, setKinds] = useState(inbox.preferences.kinds);
  const [browser, setBrowser] = useState(inbox.preferences.browser);
  const [message, setMessage] = useState("");
  async function save(e: FormEvent) {
    e.preventDefault();
    try {
      if (browser && "Notification" in window && Notification.permission === "default") await Notification.requestPermission();
      await workspaceRequest("/attention/preferences", { kinds, browser });
      setMessage(browser && (!("Notification" in window) || Notification.permission !== "granted") ? "Saved. This browser blocks notifications, so only the badge shows new items." : "Saved.");
      onSaved();
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "Preferences could not be saved.");
    }
  }
  return (
    <details>
      <summary>Notification preferences</summary>
      <form onSubmit={save}>
        <fieldset>
          <legend>Counted in your unread badge and notifications</legend>
          {inbox.kinds.map((k) => (
            <label key={k}>
              <input type="checkbox" checked={kinds.includes(k)} onChange={(e) => setKinds(e.target.checked ? [...kinds, k] : kinds.filter((x) => x !== k))} /> {kindLabels[k] ?? k}
            </label>
          ))}
        </fieldset>
        <label>
          <input type="checkbox" checked={browser} onChange={(e) => setBrowser(e.target.checked)} /> Show a browser notification for new items while the workspace is open. It says only how
          many items are new, never a client, amount or document, and the page it opens reads them with your current access.
        </label>
        <button className="primary">Save preferences</button>
        {message && <p role="status">{message}</p>}
      </form>
    </details>
  );
}

// One place for what needs a person, drawn from work, review and request states; each item links to where it is acted on.
export default function Attention() {
  const [view, setView] = useState<keyof typeof views>("all");
  const [page, setPage] = useState(0);
  const [inbox, setInbox] = useState<Inbox>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [version, setVersion] = useState(0);
  useEffect(() => {
    workspaceRequest(`/attention?page=${page}&view=${view}`)
      .then((d) => {
        setInbox(d);
        setPage(d.page);
        setError("");
      })
      .catch((e) => setError(e instanceof Error ? e.message : "The attention inbox is unavailable."));
  }, [view, page, version]);
  async function act(path: string, body: unknown) {
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(path, body);
      setVersion((v) => v + 1);
    } catch (e) {
      setError(e instanceof Error ? e.message : "The change could not be saved.");
    } finally {
      setBusy(false);
    }
  }
  const mark = (action: string, keys: string[], until?: string) => void act("/attention", { action, keys, until });
  const later = (days: number) => new Date(Date.now() + days * 86400000).toISOString();
  const groups = inbox?.groups ?? [];
  return (
    <section aria-label="Attention">
      <SectionHeader title={<>Attention{inbox && inbox.unread > 0 ? ` · ${inbox.unread} unread` : ""}</>} level={1} />
      <p>What needs a person across your clients: replies, confirmations, failures, drafts to approve or fix, changed sources, files to release and requests not delivered. Running work is left out.</p>
      <div role="group" aria-label="Show">
        {(Object.keys(views) as (keyof typeof views)[]).map((v) => (
          <button
            key={v}
            aria-pressed={view === v}
            onClick={() => {
              setView(v);
              setPage(0);
            }}
          >
            {views[v]}
          </button>
        ))}
      </div>
      {error && <p role="alert">{error}</p>}
      {inbox && groups.length === 0 && <p className="empty">Nothing needs you here.</p>}
      {groups.map((g) => {
        const keys = g.events.map((e) => e.key);
        return (
          <article key={g.close_id}>
            <h2>
              <Link to={`/dashboard/closes/${g.close_id}`}>
                {g.entity} · {g.period}
              </Link>
            </h2>
            <p>
              {g.owner === "mine" ? "Assigned to you" : g.owner === "other" ? "Assigned to someone else" : "Unassigned"}
              {g.due_on && ` · due ${g.due_on}`}
              {g.priority !== "normal" && ` · ${g.priority} priority`}{" "}
              {g.owner !== "other" && (
                <button disabled={busy} onClick={() => void act(`/${g.close_id}/planning`, { due_on: g.due_on, priority: g.priority, assignment: g.owner === "mine" ? "release" : "take" })}>
                  {g.owner === "mine" ? "Release" : "Take"}
                </button>
              )}
            </p>
            <ul>
              {g.events.map((e) => (
                <li key={e.key}>
                  {!e.read && <span className="pill">New</span>}{" "}
                  <Link to={e.workpaper_id ? `/dashboard/closes/${g.close_id}/workpapers/${e.workpaper_id}` : `/dashboard/closes/${g.close_id}${e.job_id ? `?job=${e.job_id}` : e.account_id ? "#coverage-work" : ""}`}>{e.text}</Link> · {new Date(e.at).toLocaleString()}
                  {e.snoozed_until && ` · snoozed until ${new Date(e.snoozed_until).toLocaleString()}`}{" "}
                  <button disabled={busy} onClick={() => mark(e.read ? "unread" : "read", [e.key])}>
                    {e.read ? "Mark unread" : "Mark read"}
                  </button>
                </li>
              ))}
            </ul>
            <p>
              <button disabled={busy} onClick={() => mark("read", keys)}>
                Mark all read
              </button>
              {view === "snoozed" ? (
                <button disabled={busy} onClick={() => mark("wake", keys)}>
                  Wake
                </button>
              ) : (
                <>
                  <button disabled={busy} onClick={() => mark("snooze", keys, later(1))}>
                    Snooze a day
                  </button>
                  <button disabled={busy} onClick={() => mark("snooze", keys, later(7))}>
                    Snooze a week
                  </button>
                </>
              )}
            </p>
          </article>
        );
      })}
      {(page > 0 || inbox?.has_more) && (
        <p>
          <button disabled={page === 0} onClick={() => setPage(page - 1)}>
            Newer items
          </button>
          <button disabled={!inbox?.has_more} onClick={() => setPage(page + 1)}>
            Older items
          </button>
        </p>
      )}
      {inbox && <Preferences inbox={inbox} onSaved={() => setVersion((v) => v + 1)} />}
    </section>
  );
}
