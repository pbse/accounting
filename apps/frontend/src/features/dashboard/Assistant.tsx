import { useEffect, useRef, useState } from "react";
import { Link, useLocation, useNavigate } from "react-router";
import { authKitClientId, useSession } from "../auth/Auth";
import { GoalCard, localDate } from "../closes/Goal";
import { useUnlocked, workspaceRequest, type AgentEvent } from "../closes/workspace";
import { ActionCard, type CardAction, type Outcome } from "./ActionCard";
import { RequestCard } from "./RequestCard";
import { ProposalCard } from "./ProposalCard";
import { askAssistant, nextReview, starterCommands, starters, takePending, useOpenRequests, usePending } from "./assistantStore";
import { CommandPalette, openPalette } from "./CommandPalette";
import { commands, parseCommand, runCommand, type Ask } from "./commands";
import { AgentMark } from "@close/ui/components/AgentMark";
import { Icon } from "@close/ui/components/Icon";
import { Suggestions } from "@close/ui/components/Suggestions";

// A step the agent made for an answer: what ran, its outcome and where its detail lives; never its content.
export type Step = { id: string; tool: string; title: string; status: string; size?: number; truncated?: boolean; link?: { label: string; href: string } };
type Message = { id: string; role: "user" | "assistant"; body: string; workpaper_id?: string; citations: { label: string; href: string }[]; action?: CardAction | "delegate" | "request" | "approval" | "portfolio"; due_on?: string; finish?: string; every_month?: boolean; outcome?: Outcome | null; proposal?: Record<string, unknown>; target_close_id?: string; target_close?: string; steps?: Step[] };

function readOpen() {
  try {
    return localStorage.getItem("assistant-open") === "1";
  } catch {
    return false;
  }
}

// The panel shows only in a signed-in (or unlocked) workspace.
export function useSignedIn() {
  return authKitClientId ? !!useSession().user : useUnlocked();
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
  // Only a request made while this panel is mounted opens it, so an old one never overrides a closed panel.
  const openRequests = useOpenRequests();
  const handledRequests = useRef(openRequests);
  useEffect(() => {
    if (openRequests === handledRequests.current) return;
    handledRequests.current = openRequests;
    if (!open) toggle(true);
  }, [openRequests]);
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
        <Icon name="agent" /> Ask the agent
      </button>
    );
  // A new close or draft mounts a fresh conversation, so nothing from the previous context can show.
  return <Conversation key={`${closeId}/${paperId}`} closeId={closeId} paperId={paperId} focus={openRequests} onClose={() => toggle(false)} />;
}

function Conversation({ closeId, paperId, focus, onClose }: { closeId: string; paperId: string; focus: number; onClose: () => void }) {
  const [messages, setMessages] = useState<Message[]>([]);
  const [live, setLive] = useState<string[]>([]);
  const [liveLinks, setLiveLinks] = useState<{ label: string; href: string }[]>([]);
  const [label, setLabel] = useState("");
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [pending, setPending] = useState("");
  // What the agent is doing for the message being answered: each step as it starts and ends.
  const [steps, setSteps] = useState<Step[]>([]);
  const [asking, setAsking] = useState("");
  const generation = useRef(0);
  const list = useRef<HTMLOListElement>(null);
  const input = useRef<HTMLTextAreaElement>(null);
  const navigate = useNavigate();
  const waiting = usePending();
  const path = closeId ? `/${closeId}/chat` : "/chat";
  // A question from the palette is asked once the history has loaded, so its answer is the last thing shown.
  useEffect(() => {
    if (!loaded || busy) return;
    const what = takePending(closeId);
    if (what) void send({ ...what, workpaper_id: paperId });
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
  }, [messages, live, steps, pending]);
  useEffect(() => {
    if (focus) input.current?.focus();
  }, [focus]);
  // Only the latest request may paint: an earlier read never overwrites a newer answer.
  async function send(body: Record<string, string>) {
    const asked = ++generation.current;
    setBusy(true);
    setError("");
    setSteps([]);
    setAsking("text" in body ? body.text : `/${body.command}${body.argument ? ` ${body.argument}` : ""}`);
    const onEvent = (event: AgentEvent) => {
      if (asked !== generation.current) return;
      if (event.type === "step") {
        setSteps((current) => {
          const step: Step = { id: event.id, tool: event.tool, title: event.title, status: event.state, size: event.size, truncated: event.truncated };
          return current.some((s) => s.id === step.id) ? current.map((s) => (s.id === step.id ? step : s)) : [...current, step];
        });
      } else if (event.type === "text") setPending(event.body);
    };
    try {
      // The member's own date resolves day names such as "by Friday"; the agent's steps and text arrive as events.
      const data = await workspaceRequest(path, { ...body, today: localDate() }, false, onEvent);
      if (asked !== generation.current) return;
      setMessages(data.messages);
      setLive(data.live ?? []);
      setLiveLinks(data.live_citations ?? []);
      setText("");
    } catch (e) {
      if (asked === generation.current) {
        setError(e instanceof Error ? e.message : "The assistant is unavailable.");
        // Nothing from a failed attempt is saved; the member's text stays in the box to send again.
        if ("text" in body) setText(body.text);
        workspaceRequest(path).then((data) => asked === generation.current && setMessages(data.messages), () => undefined);
      }
    } finally {
      if (asked === generation.current) {
        setPending("");
        setSteps([]);
        setAsking("");
      }
      setBusy(false);
    }
  }
  // A confirmation holds the composer, so no other answer can replace the card's result while it runs.
  async function confirm(id: string, body: Record<string, string | boolean>) {
    if (busy) throw new Error("Wait for the current answer, then confirm.");
    const asked = ++generation.current;
    setBusy(true);
    try {
      const data = await workspaceRequest(closeId ? `${path}/${id}/confirm` : `/chat/confirm/${id}`, body);
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
    void runCommand(command, argument, {
      closeId,
      navigate,
      nextReview,
      ask: (what: Ask, close: string) => {
        // A firm-wide command asked from a close opens the firm conversation on the dashboard.
        if (close !== closeId) {
          navigate("/dashboard");
          askAssistant(what, "");
          return;
        }
        void send({ ...what, workpaper_id: paperId });
      },
    })
      .then(setError)
      .catch((e) => setError(e instanceof Error ? e.message : "The command could not run."));
  }
  return (
    <aside className="assistant" aria-label="Assistant">
      <header>
        <strong>
          <AgentMark size="sm">Agent</AgentMark>
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
            <Suggestions label="Starter questions" items={closeId ? starters.close : starters.firm} disabled={busy || !loaded} onPick={(label) => void send({ ...(starterCommands[label] ?? { text: label }), workpaper_id: paperId })} />
          </li>
        )}
        {messages.map((m, i) => {
          // A firm-wide card acts on the close it named; the card reads that close as a close conversation's would.
          const cardClose = m.target_close_id || closeId;
          return (
          <li key={m.id} className={m.role}>
            <p>{m.body}</p>
            {m.steps && m.steps.length > 0 && <StepList steps={m.steps} done />}
            {m.action && m.target_close_id && (
              <p className="assistant-note">
                Acts on <Link to={`/dashboard/closes/${m.target_close_id}`}>{m.target_close || `close #${m.target_close_id}`}</Link>, not on another close.
              </p>
            )}
            {m.action === "portfolio" ? (
              <ProposalCard kind="portfolio" proposal={m.proposal ?? {}} outcome={m.outcome} live={i === newestCard} onConfirm={(body) => confirm(m.id, body)} />
            ) : m.action === "approval" ? (
              cardClose && <ProposalCard kind="approval" proposal={m.proposal ?? {}} closeId={cardClose} outcome={m.outcome} live={i === newestCard} onConfirm={(body) => confirm(m.id, body)} />
            ) : (
              m.action &&
              cardClose &&
              (m.action === "delegate" ? (
                <GoalCard closeId={cardClose} dueOn={m.due_on ?? ""} finish={m.finish ?? ""} everyMonth={!!m.every_month} outcome={m.outcome} live={i === newestCard} onConfirm={(body) => confirm(m.id, body)} />
              ) : m.action === "request" ? (
                <RequestCard closeId={cardClose} outcome={m.outcome} live={i === newestCard} onConfirm={(body) => confirm(m.id, body)} />
              ) : (
                <ActionCard closeId={cardClose} action={m.action} outcome={m.outcome} live={i === newestCard} proposedReason={String((m.proposal?.args as Record<string, unknown> | undefined)?.reason ?? "")} onConfirm={(body) => confirm(m.id, body)} />
              ))
            )}
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
          );
        })}
        {busy && asking && (
          <li className="user">
            <p>{asking}</p>
          </li>
        )}
        {busy && (steps.length > 0 || pending) && (
          <li className="assistant">
            {steps.length > 0 && <StepList steps={steps} done={false} />}
            {pending ? <p>{pending}</p> : <p className="assistant-working" role="status">{steps.length ? "Working…" : "Thinking…"}</p>}
          </li>
        )}
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
        {" "}With Agent chat on for client closes, every question you type in a client close goes to the configured model provider, with the reads of this close the agent chooses (its status, blockers, tie-out, changes, exceptions, documents, drafts, procedures, a search) made with your permissions and listed under the answer, and the last turns of this conversation; commands typed with / never go to a model, except /ask, which sends its question as free text. With it on firm-wide, a question asked outside a close goes the same way with the reads the agent chooses across the closes you can open, and an answer built on a read that cannot be tied to particular closes is shown once and not kept.
      </p>
    </aside>
  );
}

// StepList shows what the agent read for an answer: running steps with their state, finished ones collapsed to a line.
const stepWords: Record<string, string> = { running: "reading", ok: "read", proposed: "proposed", refused: "refused", unavailable: "unavailable", omitted: "too large" };
function StepList({ steps, done }: { steps: Step[]; done: boolean }) {
  const items = steps.map((s) => (
    <li key={s.id} className={`step-${s.status}`}>
      <span>{done && s.link ? <Link to={s.link.href}>{s.title || s.tool}</Link> : s.title || s.tool}</span>
      <small>
        {stepWords[s.status] ?? s.status}
        {s.truncated ? " · cut to the first rows" : ""}
      </small>
    </li>
  ));
  if (!done) return <ol className="assistant-steps" aria-label="What the agent is doing">{items}</ol>;
  const read = steps.filter((s) => s.status === "ok").length;
  return (
    <details className="assistant-steps-done">
      <summary>
        {read === 1 ? "Read 1 thing" : `Read ${read} things`}
        {steps.length > read ? ` · ${steps.length - read} other step${steps.length - read === 1 ? "" : "s"}` : ""}
      </summary>
      <ol className="assistant-steps">{items}</ol>
    </details>
  );
}
