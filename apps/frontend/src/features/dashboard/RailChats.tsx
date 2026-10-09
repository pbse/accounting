import { useEffect, useState } from "react";
import { Icon } from "@close/ui/components/Icon";
import { loadSessions, openSession, useSessionsVersion, type ChatSession } from "./assistantStore";

// RailChats is the rail's chat group: a new chat, and the five latest chats to switch to; the full list is the panel's.
export function RailChats({ closeId, navigate }: { closeId: string; navigate: (to: string) => void }) {
  const version = useSessionsVersion();
  const [sessions, setSessions] = useState<ChatSession[]>([]);
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (!open) return;
    let live = true;
    loadSessions(5)
      .then((list) => live && setSessions(list))
      .catch(() => live && setSessions([]));
    return () => {
      live = false;
    };
  }, [open, version]);
  // A close's chat is read on its close's page; a firm-wide one anywhere outside a close.
  function go(session: string, target: string) {
    setOpen(false);
    if (target !== closeId) navigate(target ? `/dashboard/closes/${target}` : "/dashboard");
    openSession(session, target);
  }
  return (
    <details className="rail-chats" open={open} onToggle={(e) => setOpen((e.currentTarget as HTMLDetailsElement).open)}>
      <summary className="rail-item" title="Chats" aria-label="Your chats">
        <Icon name="history" size={18} />
      </summary>
      <div role="menu" aria-label="Your chats">
        <button type="button" role="menuitem" className="rail-chat new" onClick={() => go("new", closeId)}>
          <Icon name="plus" size={14} /> New chat
        </button>
        {sessions.map((s) => (
          <button type="button" role="menuitem" className="rail-chat" key={s.id} title={s.close ? `${s.title} · ${s.close}` : s.title} onClick={() => go(s.id, s.close_id)}>
            <span className="rail-chat-title">{s.title}</span>
            <span className="rail-chat-scope">{s.close || "Firm-wide"}</span>
          </button>
        ))}
        {sessions.length === 0 && <p className="rail-chat-empty">No chats yet.</p>}
        <button type="button" role="menuitem" className="rail-chat all" onClick={() => go("all", closeId)}>
          All chats
        </button>
      </div>
    </details>
  );
}
