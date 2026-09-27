import { useAuth } from "@workos-inc/authkit-react";
import { useEffect, useRef, useState } from "react";
import { Link, useLocation, useNavigate } from "react-router";
import { authKitClientId } from "../auth/Auth";
import { GoalCard, localDate } from "../closes/Goal";
import { useUnlocked, workspaceRequest } from "../closes/workspace";
import { ActionCard, type CardAction, type Outcome } from "./ActionCard";
import { RequestCard } from "./RequestCard";
import { askAssistant, nextReview, takePending, usePending } from "./assistantStore";
import { CommandPalette, openPalette } from "./CommandPalette";
import { commands, parseCommand, runCommand } from "./commands";
import { Icon } from "./Icon";

type Message = { id: string; role: "user" | "assistant"; body: string; workpaper_id?: string; citations: { label: string; href: string }[]; action?: CardAction | "delegate" | "request"; due_on?: string; finish?: string; outcome?: Outcome | null };

function readOpen() {
  try {
    return localStorage.getItem("assistant-open") === "1";
  } catch {
    return false;
  }
}

// The panel shows only in a signed-in (or unlocked) workspace.
export function useSignedIn() {
  return authKitClientId ? !!useAuth().user : useUnlocked();
}

export function Assistant() {
  return useSignedIn() ? (
    <>
      <Panel />
      <CommandPalette />
    </>
  ) : null;
}

function Panel() {
  const location = useLocation();
  const match = /^\/dashboard\/closes\/(\d+)(?:\/workpapers\/(\d+))?/.exec(location.pathname);
  const closeId = match?.[1] ?? "";
  const paperId = match?.[2] ?? "";
  const [open, setOpen] = useState(readOpen);
  const asked = usePending();
  useEffect(() => {
    if (asked && !open) toggle(true);
  }, [asked]);
  // A link such as /dashboard/closes/7?ask=… asks that close's assistant once.
  const navigate = useNavigate();
  useEffect(() => {
    const params = new URLSearchParams(location.search);
    const question = params.get("ask");
    if (!question || !closeId) return;
    params.delete("ask");
    navigate({ search: params.toString() }, { replace: true });
    askAssistant(question, closeId);
  }, [location.search, closeId]);
  useEffect(() => {
    document.body.classList.toggle("assistant-open", open);
    return () => document.body.classList.remove("assistant-open");
  }, [open]);
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
  const [liveLinks, setLiveLinks] = useState<{ label: string; href: string }[]>([]);
  const [label, setLabel] = useState("");
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [loaded, setLoaded] = useState(false);
  const generation = useRef(0);
  const list = useRef<HTMLOListElement>(null);
  const input = useRef<HTMLTextAreaElement>(null);
  const navigate = useNavigate();
  const waiting = usePending();
  const path = closeId ? `/${closeId}/chat` : "/chat";
  // A question from the palette is asked once the history has loaded, so its answer is the last thing shown.
  useEffect(() => {
    if (!loaded || busy) return;
    const question = takePending(closeId);
    if (question) void send({ text: question, workpaper_id: paperId });
  }, [waiting, busy, loaded]);
  useEffect(() => {
    input.current?.focus();
    const asked = ++generation.current;
    workspaceRequest(path)
      .then((data) => asked === generation.current && setMessages(data.messages))
      .catch((e) => asked === generation.current && setError(e instanceof Error ? e.message : "Conversation unavailable."))
      .finally(() => setLoaded(true));
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
      // The member's own date resolves day names such as "by Friday".
      const data = await workspaceRequest(path, { ...body, today: localDate() });
      if (asked !== generation.current) return;
      setMessages(data.messages);
      setLive(data.live ?? []);
      setLiveLinks(data.live_citations ?? []);
      setText("");
    } catch (e) {
      if (asked === generation.current) setError(e instanceof Error ? e.message : "The assistant is unavailable.");
    } finally {
      setBusy(false);
    }
  }
  // A confirmation holds the composer, so no other answer can replace the card's result while it runs.
  async function confirm(id: string, body: Record<string, string | boolean>) {
    if (busy) throw new Error("Wait for the current answer, then confirm.");
    const asked = ++generation.current;
    setBusy(true);
    try {
      const data = await workspaceRequest(`${path}/${id}/confirm`, body);
      if (asked === generation.current) setMessages(data.messages);
    } finally {
      setBusy(false);
    }
  }
  // Only the newest card follows the close's work; older ones keep what they started.
  const newestCard = messages.map((m) => !!m.action).lastIndexOf(true);
  const slash = text.startsWith("/") && !text.includes(" ") ? commands.filter((c) => c.name.startsWith(text.slice(1).toLowerCase())) : [];
  function submit(event?: { preventDefault(): void }) {
    event?.preventDefault();
    const line = text.trim();
    if (!line || busy) return;
    if (!line.startsWith("/")) {
      void send({ text: line, workpaper_id: paperId });
      return;
    }
    const { command, argument } = parseCommand(line);
    if (!command) {
      setError("Unknown command. Type / to see the commands.");
      return;
    }
    setText("");
    if (command.close && !closeId) {
      openPalette(command.argument ? `/${command.name} ` : line, command.argument ? argument : "");
      return;
    }
    setError("");
    void runCommand(command, argument, { closeId, navigate, nextReview, ask: (question) => void send({ text: question, workpaper_id: paperId }) })
      .then(setError)
      .catch((e) => setError(e instanceof Error ? e.message : "The command could not run."));
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
              ? "Say “Get this close ready for review by Friday” to hand it to the agent, or ask where it stands, what blocks sign-off, which balances don't tie, what changed from last month or what exceptions the ledger shows."
              : "Ask what needs you today, what is ready for review or what is blocked, or name a client to hand it to the agent (“Get Acme ready for review by Friday”)."}
          </li>
        )}
        {messages.map((m, i) => (
          <li key={m.id} className={m.role}>
            <p>{m.body}</p>
            {m.action &&
              closeId &&
              (m.action === "delegate" ? (
                <GoalCard closeId={closeId} dueOn={m.due_on ?? ""} finish={m.finish ?? ""} outcome={m.outcome} live={i === newestCard} onConfirm={(body) => confirm(m.id, body)} />
              ) : m.action === "request" ? (
                <RequestCard closeId={closeId} outcome={m.outcome} live={i === newestCard} onConfirm={(body) => confirm(m.id, body)} />
              ) : (
                <ActionCard closeId={closeId} action={m.action} outcome={m.outcome} live={i === newestCard} onConfirm={(body) => confirm(m.id, body)} />
              ))}
            {m.role === "assistant" && i === messages.length - 1 && live.length > 0 && (
              <ul>
                {live.map((line, n) => (
                  <li key={n}>{line}</li>
                ))}
              </ul>
            )}
            {[...m.citations, ...(m.role === "assistant" && i === messages.length - 1 ? liveLinks : [])].map((c, n) => (
              <Link key={n} to={c.href}>
                {c.label} →
              </Link>
            ))}
          </li>
        ))}
      </ol>
      {error && <p role="alert">{error}</p>}
      <form onSubmit={submit}>
        {slash.length > 0 && (
          <ul className="assistant-commands" aria-label="Command suggestions">
            {slash.map((c) => (
              <li key={c.name}>
                <button type="button" onClick={() => { setText(`/${c.name}${c.argument ? " " : ""}`); input.current?.focus(); }}>
                  <strong>/{c.name}</strong> {c.description}
                </button>
              </li>
            ))}
          </ul>
        )}
        <textarea
          ref={input}
          aria-label="Ask the assistant"
          value={text}
          maxLength={1000}
          rows={2}
          placeholder={closeId ? "Ask about this close, or type / for commands…" : "Ask about your work, or type / for commands…"}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            const completed = slash.length > 0 ? `/${slash[0].name}${slash[0].argument ? " " : ""}` : "";
            if (e.key === "Tab" && completed && completed !== text) {
              e.preventDefault();
              setText(completed);
            } else if (e.key === "Enter" && !e.shiftKey) submit(e);
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
      <p className="assistant-note">
        Answers come from saved work you can access. A card's action runs only after you confirm it, through the same checks as the close page; the assistant never approves, sends or posts anything.
        {" "}If your firm turns on written answers, a question in a client close that none of its fixed reads matches goes to OpenAI with this close's saved status, sign-off blockers, tie-out, changes, exceptions and any open draft, and the answer says it was written by the model.
      </p>
    </aside>
  );
}
