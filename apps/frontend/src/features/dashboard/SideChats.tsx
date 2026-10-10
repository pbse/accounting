import { useEffect, useState } from "react";
import { SidebarCategory } from "@close/ui/components/Sidebar";
import { loadSessions, openSession, useSessionsVersion, type ChatSession } from "./assistantStore";

// The sidebar's chats: the four most recent by title, and All chats. A close's chat is read on its close's page; a
// firm-wide one anywhere outside a close.
export function SideChats({ closeId, navigate }: { closeId: string; navigate: (to: string) => void }) {
  const version = useSessionsVersion();
  const [sessions, setSessions] = useState<ChatSession[]>([]);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let live = true;
    setFailed(false);
    loadSessions(4)
      .then((page) => live && setSessions(page.sessions))
      .catch(() => live && (setSessions([]), setFailed(true)));
    return () => {
      live = false;
    };
  }, [version]);
  function go(session: string, target: string) {
    if (target !== closeId) navigate(target ? `/dashboard/closes/${target}` : "/dashboard");
    openSession(session, target);
  }
  return (
    <div className="ks-section">
      <SidebarCategory>Chats</SidebarCategory>
      {sessions.map((s) => (
        <button type="button" className="ks-chat" key={s.id} title={s.close ? `${s.title} · ${s.close}` : s.title} onClick={() => go(s.id, s.close_id)}>
          <span className="ks-t">{s.title}</span>
        </button>
      ))}
      {sessions.length === 0 && <p className="ks-empty">{failed ? "Your chats could not be read." : "No chats yet."}</p>}
      <button type="button" className="ks-more" onClick={() => go("all", closeId)}>
        All chats
      </button>
    </div>
  );
}
