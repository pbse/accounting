import { useAuth } from "@workos-inc/authkit-react";
import { useEffect, useRef, useState } from "react";
import { Link, useLocation } from "react-router";
import { authKitClientId } from "../auth/Auth";
import { useUnlocked, workspaceRequest } from "../closes/workspace";
import { Icon } from "./Icon";

type Message = { id: string; role: "user" | "assistant"; body: string; workpaper_id?: string; citations: { label: string; href: string }[] };

function readOpen() {
  try {
    return localStorage.getItem("assistant-open") === "1";
  } catch {
    return false;
  }
}

// The panel shows only in a signed-in (or unlocked) workspace.
function useSignedIn() {
  return authKitClientId ? !!useAuth().user : useUnlocked();
}

export function Assistant() {
  return useSignedIn() ? <Panel /> : null;
}

function Panel() {
  const location = useLocation();
  const match = /^\/dashboard\/closes\/(\d+)(?:\/workpapers\/(\d+))?/.exec(location.pathname);
  const closeId = match?.[1] ?? "";
  const paperId = match?.[2] ?? "";
  const [open, setOpen] = useState(readOpen);
  const toggleButton = useRef<HTMLButtonElement>(null);
  const opened = useRef(open);
  useEffect(() => {
    if (!open && opened.current) toggleButton.current?.focus();
    opened.current = open;
  }, [open]);
  function toggle(next: boolean) {
    setOpen(next);
    try {
      localStorage.setItem("assistant-open", next ? "1" : "0");
    } catch {
      // The panel still works without remembering its state.
    }
  }
  if (!open)
    return (
      <button ref={toggleButton} type="button" className="assistant-toggle" onClick={() => toggle(true)} aria-label="Open the assistant">
        <Icon name="agent" /> Assistant
      </button>
    );
  // A new close or draft mounts a fresh conversation, so nothing from the previous context can show.
  return <Conversation key={`${closeId}/${paperId}`} closeId={closeId} paperId={paperId} onClose={() => toggle(false)} />;
}

function Conversation({ closeId, paperId, onClose }: { closeId: string; paperId: string; onClose: () => void }) {
  const [messages, setMessages] = useState<Message[]>([]);
  const [live, setLive] = useState<string[]>([]);
  const [label, setLabel] = useState("");
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const generation = useRef(0);
  const list = useRef<HTMLOListElement>(null);
  const input = useRef<HTMLTextAreaElement>(null);
  const path = closeId ? `/${closeId}/chat` : "/chat";
  useEffect(() => {
    input.current?.focus();
    const asked = ++generation.current;
    workspaceRequest(path)
      .then((data) => asked === generation.current && setMessages(data.messages))
      .catch((e) => asked === generation.current && setError(e instanceof Error ? e.message : "Conversation unavailable."));
    if (closeId)
      workspaceRequest(`/${closeId}`)
        .then((data) => setLabel(`${data.close.entity} · ${data.close.period}`))
        .catch(() => undefined);
  }, []);
  useEffect(() => {
    list.current?.lastElementChild?.scrollIntoView({ block: "end" });
  }, [messages, live]);
  // Only the latest request may paint: an earlier read never overwrites a newer answer.
  async function send(body: Record<string, string>) {
    const asked = ++generation.current;
    setBusy(true);
    setError("");
    try {
      const data = await workspaceRequest(path, body);
      if (asked !== generation.current) return;
      setMessages(data.messages);
      setLive(data.live ?? []);
      setText("");
    } catch (e) {
      if (asked === generation.current) setError(e instanceof Error ? e.message : "The assistant is unavailable.");
    } finally {
      setBusy(false);
    }
  }
  function submit(event?: { preventDefault(): void }) {
    event?.preventDefault();
    if (text.trim() && !busy) void send({ text: text.trim(), workpaper_id: paperId });
  }
  return (
    <aside className="assistant" aria-label="Assistant">
      <header>
        <strong>
          <Icon name="agent" /> Assistant
        </strong>
        <button type="button" onClick={onClose} aria-label="Close the assistant">
          ×
        </button>
      </header>
      <p className="assistant-context" aria-label="Conversation context">
        {closeId ? <span className="pill">{label || `Close #${closeId}`}</span> : <span className="pill">Firm-wide</span>}
        {paperId && <span className="pill">Draft #{paperId}</span>}
      </p>
      <ol ref={list} className="assistant-messages" aria-live="polite">
        {messages.length === 0 && (
          <li className="assistant-hint">
            {closeId
              ? "Ask where this close stands, what blocks sign-off, which balances don't tie, what changed from last month or what exceptions the ledger shows."
              : "Ask what needs you today, what is ready for review or what is blocked. Open a client close to ask about it."}
          </li>
        )}
        {messages.map((m, i) => (
          <li key={m.id} className={m.role}>
            <p>{m.body}</p>
            {m.role === "assistant" && i === messages.length - 1 && live.length > 0 && (
              <ul>
                {live.map((line, n) => (
                  <li key={n}>{line}</li>
                ))}
              </ul>
            )}
            {m.citations.map((c) => (
              <Link key={c.href} to={c.href}>
                {c.label} →
              </Link>
            ))}
          </li>
        ))}
      </ol>
      {error && <p role="alert">{error}</p>}
      <form onSubmit={submit}>
        <textarea
          ref={input}
          aria-label="Ask the assistant"
          value={text}
          maxLength={1000}
          rows={2}
          placeholder={closeId ? "Ask about this close…" : "Ask about your work…"}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) submit(e);
          }}
        />
        <div>
          <button type="button" disabled={busy || messages.length === 0} onClick={() => void send({ action: "clear" })}>
            Clear
          </button>
          <button className="primary" disabled={busy || !text.trim()}>
            {busy ? "Answering…" : "Ask"}
          </button>
        </div>
      </form>
      <p className="assistant-note">Answers come from saved work you can access. The assistant never approves, sends or posts anything.</p>
    </aside>
  );
}
