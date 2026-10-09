import { useEffect, useRef, useState, type ReactNode } from "react";
import { Link, useLocation, useNavigate } from "react-router";
import { authKitClientId, useSession } from "../auth/Auth";
import { GoalCard, localDate } from "../closes/Goal";
import { useUnlocked, workspaceRequest, type AgentEvent } from "../closes/workspace";
import { ActionCard, type CardAction, type Outcome } from "./ActionCard";
import { RequestCard } from "./RequestCard";
import { ProposalCard } from "./ProposalCard";
import { askAssistant, bumpSessions, clearSessionRequest, loadSessions, nextReview, openSession, starterCommands, starters, takePending, useOpenRequests, usePending, useSessionRequest, useSessionsVersion, type ChatSession } from "./assistantStore";
import { CommandPalette, openPalette } from "./CommandPalette";
import { commands, parseCommand, runCommand, type Ask } from "./commands";
import { AgentMark } from "@close/ui/components/AgentMark";
import { Icon } from "@close/ui/components/Icon";
import { Suggestions } from "@close/ui/components/Suggestions";

// The agent panel: one conversation per close (or firm-wide), as a drawer beside the page on a desktop and a sheet on a
// phone. Free text goes to the agent when the firm allows it; commands run their fixed read without a model; every
// action the agent proposes is a card the member confirms. What the agent is doing shows step by step as it runs.

// A step the agent made for an answer: what ran, its outcome and where its detail lives; never its content.
export type Step = { id: string; tool: string; title: string; status: string; size?: number; truncated?: boolean; link?: { label: string; href: string } };
type Message = { id: string; role: "user" | "assistant"; body: string; workpaper_id?: string; citations: { label: string; href: string }[]; action?: CardAction | "delegate" | "request" | "approval" | "portfolio" | "open_close"; due_on?: string; finish?: string; every_month?: boolean; outcome?: Outcome | null; proposal?: Record<string, unknown>; target_close_id?: string; target_close?: string; steps?: Step[] };

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

// An executive gets the panel over their own pages: the agent reads their published reports, cash plan and budget, and
// proposes nothing; the command palette, whose commands read the firm's work, is not theirs.
export function Assistant({ executive = false }: { executive?: boolean }) {
  return useSignedIn() ? (
    <>
      <Panel executive={executive} />
      {!executive && <CommandPalette />}
    </>
  ) : null;
}

function Panel({ executive }: { executive: boolean }) {
  const location = useLocation();
  const match = /^\/dashboard\/closes\/(\d+)(?:\/workpapers\/(\d+))?/.exec(location.pathname);
  const closeId = match?.[1] ?? "";
  const paperId = match?.[2] ?? "";
  // Closed on the server and in the browser's first render alike; a panel left open is reopened once hydrated.
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (readOpen()) setOpen(true);
  }, []);
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
  // A link such as /dashboard/closes/7?ask=… asks that close's agent once.
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
  // Closing the panel hands focus back to the top bar's Ask the agent button, which opened it.
  const opened = useRef(open);
  useEffect(() => {
    if (!open && opened.current) document.querySelector<HTMLButtonElement>(".topbar .ask-agent")?.focus();
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
  if (!open) return null;
  // A new close or draft mounts a fresh conversation, so nothing from the previous context can show.
  return <Conversation key={`${closeId}/${paperId}`} closeId={closeId} paperId={paperId} executive={executive} focus={openRequests} onClose={() => toggle(false)} />;
}

function Conversation({ closeId, paperId, executive, focus, onClose }: { closeId: string; paperId: string; executive: boolean; focus: number; onClose: () => void }) {
  const [messages, setMessages] = useState<Message[]>([]);
  const [live, setLive] = useState<string[]>([]);
  const [liveLinks, setLiveLinks] = useState<{ label: string; href: string }[]>([]);
  const [label, setLabel] = useState("");
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [lastBody, setLastBody] = useState<Record<string, string>>();
  const [loaded, setLoaded] = useState(false);
  const [pending, setPending] = useState("");
  const [steps, setSteps] = useState<Step[]>([]);
  const [asking, setAsking] = useState("");
  const [slow, setSlow] = useState(false);
  // Counts every step event, so the waiting timer restarts on a step's change of state, not only on a new step.
  const [stepTick, setStepTick] = useState(0);
  const [menu, setMenu] = useState(false);
  const [about, setAbout] = useState(false);
  // The chat shown: null is a fresh chat, saved on its first question. The list view shows every chat to switch to.
  const [session, setSession] = useState<{ id: string; title: string } | null>(null);
  // picked says the member opened this chat on purpose (from the list or the rail): it is continued by id. The current
  // chat is the server's to continue or, after 12 quiet hours, to replace, even with the panel left open.
  const [picked, setPicked] = useState(false);
  const [view, setView] = useState<"chat" | "chats">("chat");
  const [renaming, setRenaming] = useState<string | null>(null);
  const [selected, setSelected] = useState(0);
  const generation = useRef(0);
  const list = useRef<HTMLOListElement>(null);
  const input = useRef<HTMLTextAreaElement>(null);
  const navigate = useNavigate();
  const waiting = usePending();
  // A page that leads with its own "Ask the agent" composer (Work, Attention, a close's Overview) already offers the
  // starter questions, so the panel shows no chips of its own beside it.
  const [pageComposer, setPageComposer] = useState(false);
  useEffect(() => {
    // The page's composer may mount after its own read, or unmount on a hand-over, so the page is watched, not probed.
    const look = () => setPageComposer(!!document.querySelector(".agent-composer"));
    look();
    const watcher = new MutationObserver(look);
    watcher.observe(document.body, { childList: true, subtree: true });
    return () => watcher.disconnect();
  }, []);
  const path = closeId ? `/${closeId}/chat` : "/chat";
  // A question from the palette is asked once the history has loaded, so its answer is the last thing shown.
  useEffect(() => {
    if (!loaded || busy) return;
    const what = takePending(closeId);
    if (what) void send({ ...what, workpaper_id: paperId });
  }, [waiting, busy, loaded]);
  // load shows one chat: the id given, or the scope's current one (a chat less than 12 hours quiet), else a fresh one.
  function load(id?: string) {
    const asked = ++generation.current;
    setPicked(!!id);
    setRenaming(null);
    // Nothing of the chat shown before stays while the selected one loads, not even an answer still arriving (its
    // request is ignored once it ends), and nothing is sent until it has.
    setSession(null);
    setMessages([]);
    setBusy(false);
    setAsking("");
    setPending("");
    setSteps([]);
    setLoaded(false);
    setError("");
    setLive([]);
    setLiveLinks([]);
    setView("chat");
    workspaceRequest(id ? `${path}?session=${encodeURIComponent(id)}` : path)
      .then((data) => {
        if (asked !== generation.current) return;
        setMessages(data.messages);
        setSession(data.session ? { id: data.session.id, title: data.session.title } : null);
      })
      .catch((e) => {
        if (asked !== generation.current) return;
        // A chat that could not be opened (deleted elsewhere, no longer accessible) leaves nothing of the one before.
        setMessages([]);
        setSession(null);
        setPicked(false);
        setError(e instanceof Error ? e.message : "Conversation unavailable.");
      })
      .finally(() => asked === generation.current && setLoaded(true));
  }
  // startNew leaves the current chat where it is and opens a fresh one; nothing is saved until its first question.
  function startNew() {
    ++generation.current;
    setMessages([]);
    setSession(null);
    setBusy(false);
    setAsking("");
    setPending("");
    setSteps([]);
    setPicked(false);
    setRenaming(null);
    setLive([]);
    setLiveLinks([]);
    setError("");
    setView("chat");
    setLoaded(true);
    input.current?.focus();
  }
  // A chat asked for from outside (the rail, the list) is taken by the panel of its scope: on mount, the request
  // decides what is shown; afterwards each new request does. Without one, the scope's current chat is shown.
  const request = useSessionRequest();
  const handledSession = useRef(0);
  const started = useRef(false);
  useEffect(() => {
    const fresh = request && request.n !== handledSession.current && request.closeId === closeId;
    if (fresh) {
      handledSession.current = request.n;
      if (request.session === "new") startNew();
      else if (request.session === "all") {
        if (!started.current) load();
        setView("chats");
      } else load(request.session);
      started.current = true;
      clearSessionRequest();
      return;
    }
    if (!started.current) load();
    started.current = true;
  }, [request, closeId]);
  useEffect(() => {
    input.current?.focus();
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
  // "Waiting for the agent" after three seconds without a step: honest about a queue, never a fake pulse.
  useEffect(() => {
    if (!busy) {
      setSlow(false);
      return;
    }
    setSlow(false);
    const timer = setTimeout(() => setSlow(true), 3000);
    return () => clearTimeout(timer);
  }, [busy, stepTick, pending]);
  // The box grows with its text, to five lines.
  useEffect(() => {
    const box = input.current;
    if (!box) return;
    box.style.height = "auto";
    box.style.height = `${Math.min(box.scrollHeight, 5 * 22 + 20)}px`;
  }, [text]);
  // Only the latest request may paint: an earlier read never overwrites a newer answer.
  async function send(body: Record<string, string>) {
    if (busy || !loaded) return;
    const asked = ++generation.current;
    setBusy(true);
    setError("");
    setSteps([]);
    setLastBody(body);
    setView("chat");
    setAsking("text" in body ? body.text : body.command ? `/${body.command}${body.argument ? ` ${body.argument}` : ""}` : "");
    const onEvent = (event: AgentEvent) => {
      if (asked !== generation.current) return;
      if (event.type === "step") {
        setStepTick((n) => n + 1);
        setSteps((current) => {
          const step: Step = { id: event.id, tool: event.tool, title: event.title, status: event.state, size: event.size, truncated: event.truncated };
          return current.some((s) => s.id === step.id) ? current.map((s) => (s.id === step.id ? step : s)) : [...current, step];
        });
      } else if (event.type === "text") setPending(event.body);
    };
    try {
      // The member's own date resolves day names such as "by Friday"; the agent's steps and text arrive as events.
      const data = await workspaceRequest(path, { ...body, session_id: session?.id ?? "new", picked, today: localDate() } as Record<string, unknown>, false, onEvent);
      if (asked !== generation.current) return;
      setMessages(data.messages);
      if (data.session) setSession({ id: data.session.id, title: data.session.title });
      else setSession(null);
      bumpSessions();
      setLive(data.live ?? []);
      setLiveLinks(data.live_citations ?? []);
      setText("");
    } catch (e) {
      if (asked === generation.current) {
        setError(e instanceof Error ? e.message : "The agent is unavailable.");
        // Nothing from a failed attempt is saved; the member's text stays in the box to send again.
        if ("text" in body) setText(body.text);
        // What was saved before the answer was cut off is shown, with the chat it was saved in.
        workspaceRequest(session ? `${path}?session=${encodeURIComponent(session.id)}` : path).then((data) => {
          if (asked !== generation.current) return;
          setMessages(data.messages);
          if (data.session) setSession({ id: data.session.id, title: data.session.title });
        }, () => undefined);
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
  // Typing "/" lists the commands, the same list the palette shows; arrows choose, Tab or Enter completes.
  const slash = !executive && text.startsWith("/") && !text.includes(" ") ? commands.filter((c) => c.name.startsWith(text.slice(1).toLowerCase()) && (!c.close || closeId || true)) : [];
  const chosen = slash[Math.min(selected, Math.max(slash.length - 1, 0))];
  // Tab completes the command in the box; Enter on a chosen command sends it when it takes no argument.
  function complete(c: (typeof commands)[number], run: boolean) {
    const line = `/${c.name}${c.argument ? " " : ""}`;
    setText(line);
    setSelected(0);
    input.current?.focus();
    if (run && !c.argument) submitLine(line);
  }
  function submit(event?: { preventDefault(): void }) {
    event?.preventDefault();
    submitLine(text.trim());
  }
  function submitLine(line: string) {
    if (!line || busy) return;
    // An executive has no commands: whatever they type is a question.
    if (executive || !line.startsWith("/")) {
      void send({ text: line, workpaper_id: paperId });
      return;
    }
    const { command, argument } = parseCommand(line);
    // A command that cannot run shows its message with nothing to retry: retry belongs to a request that was sent.
    setLastBody(undefined);
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
  const empty = messages.length === 0 && !busy;
  return (
    <aside className="assistant" aria-label="Agent">
      <header>
        <button type="button" className="assistant-back" onClick={onClose} aria-label="Close the agent">
          <Icon name="back" />
        </button>
        <strong>
          <AgentMark size="sm">Agent</AgentMark>
        </strong>
        <span className="assistant-tools">
          <button type="button" aria-label="New chat" title="New chat" disabled={busy} onClick={startNew}>
            <Icon name="plus" size={14} />
          </button>
          <button type="button" aria-label="Your chats" title="Your chats" aria-pressed={view === "chats"} onClick={() => setView((v) => (v === "chats" ? "chat" : "chats"))}>
            <Icon name="history" size={14} />
          </button>
          <button type="button" aria-label="What the agent sends" aria-pressed={about} onClick={() => setAbout((v) => !v)}>
            i
          </button>
          <span className="assistant-menu">
            <button type="button" aria-label="More" aria-haspopup="menu" aria-expanded={menu} onClick={() => setMenu((v) => !v)}>
              …
            </button>
            {menu && (
              <ul role="menu">
                <li role="none">
                  <button type="button" role="menuitem" disabled={busy || !session} onClick={() => { setMenu(false); setRenaming(session?.title ?? ""); }}>
                    Rename this chat
                  </button>
                </li>
                <li role="none">
                  <button type="button" role="menuitem" disabled={busy || !session} onClick={() => { setMenu(false); void send({ action: "clear" }); }}>
                    Delete this chat
                  </button>
                </li>
              </ul>
            )}
          </span>
          <button type="button" className="assistant-close" onClick={onClose} aria-label="Close the agent">
            ×
          </button>
        </span>
      </header>
      <p className="assistant-context" aria-label="Conversation context">
        {closeId ? <span className="pill">{label || `Close #${closeId}`}</span> : <span className="pill">{executive ? "Your business" : "Firm-wide"}</span>}
        {paperId && <span className="pill">Draft #{paperId}</span>}
        {renaming !== null && session ? (
          <form
            className="assistant-rename"
            onSubmit={(e) => {
              e.preventDefault();
              const title = renaming.trim();
              if (!title) return;
              const renamed = session.id;
              workspaceRequest(`/chat/sessions/${renamed}`, { action: "rename", title })
                .then(() => {
                  // The chat shown may have changed meanwhile; only the renamed one takes the title.
                  setSession((current) => (current && current.id === renamed ? { ...current, title } : current));
                  setRenaming(null);
                  bumpSessions();
                })
                .catch((err) => setError(err instanceof Error ? err.message : "The chat could not be renamed."));
            }}
          >
            <input aria-label="Chat name" value={renaming} maxLength={80} autoFocus onChange={(e) => setRenaming(e.target.value)} onKeyDown={(e) => e.key === "Escape" && setRenaming(null)} />
            <button type="submit" className="primary">Save</button>
          </form>
        ) : (
          session && <span className="assistant-title" title={session.title}>{session.title}</span>
        )}
      </p>
      {view === "chats" && (
        <Chats
          closeId={closeId}
          current={session?.id ?? ""}
          onOpen={(id, target) => {
            if (target === closeId) load(id);
            else {
              // A close's chat is read with its page beside it; a firm-wide one outside a close.
              navigate(target ? `/dashboard/closes/${target}` : "/dashboard");
              openSession(id, target);
            }
          }}
          onNew={startNew}
        />
      )}
      {about && executive && (
        <p className="assistant-about">
          Answers come from your clients’ published reports, cash plan and budget, read with your access and listed under each answer. With the firm’s Agent chat on firm-wide, every question goes to the configured model provider with the reads the agent chooses and the last turns of this conversation; below that level a question gets one fixed line and is not sent anywhere. The agent proposes nothing for an executive and changes nothing.
        </p>
      )}
      {about && !executive && (
        <p className="assistant-about">
          Answers come from saved work you can access, read with your permissions and listed under each answer. With Agent chat on for client closes, every question you type in a client close goes to the configured model provider with the reads of this close the agent chooses and the last turns of this conversation; commands typed with / never go to a model, except /ask. With it on firm-wide, a question asked outside a close goes the same way with the reads the agent chooses across the closes you can open, and an answer built on a read that cannot be tied to particular closes is shown once and not kept. An action the agent proposes runs only after you confirm its card, through the same checks as the page; the agent never approves, sends or posts anything.
        </p>
      )}
      <ol ref={list} className="assistant-messages" aria-live="polite" hidden={view === "chats"}>
        {empty && !pageComposer && (
          <li className="assistant-hint">
            <Suggestions label="Starter questions" items={executive ? starters.executive : closeId ? starters.close : starters.firm} disabled={busy || !loaded} onPick={(label) => void send({ ...((!executive && starterCommands[label]) || { text: label }), workpaper_id: paperId })} />
          </li>
        )}
        {messages.map((m, i) => {
          // A firm-wide card acts on the close it named; the card reads that close as a close conversation's would.
          const cardClose = m.target_close_id || closeId;
          const last = m.role === "assistant" && i === messages.length - 1;
          return (
            <li key={m.id} className={m.role}>
              {m.role === "assistant" && <AgentMark size="sm" />}
              <div className="assistant-body">
                <Prose text={m.body} />
                {last && live.length > 0 && <Prose text={live.join("\n")} />}
                {m.steps && m.steps.length > 0 && <StepList steps={m.steps} done />}
                {m.action && m.target_close_id && (
                  <p className="assistant-note">
                    Acts on <Link to={`/dashboard/closes/${m.target_close_id}`}>{m.target_close || `close #${m.target_close_id}`}</Link>, not on another close.
                  </p>
                )}
                {m.action === "open_close" ? (
                  <ProposalCard kind="open_close" proposal={m.proposal ?? {}} outcome={m.outcome} live={i === newestCard} onConfirm={(body) => confirm(m.id, body)} />
                ) : m.action === "portfolio" ? (
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
                {(m.citations.length > 0 || (last && liveLinks.length > 0)) && (
                  <span className="assistant-chips" aria-label="Where this comes from">
                    {[...m.citations, ...(last ? liveLinks : [])].map((c, n) => (
                      <Link key={n} to={c.href} className="chip">
                        {c.label}
                      </Link>
                    ))}
                  </span>
                )}
              </div>
            </li>
          );
        })}
        {busy && asking && (
          <li className="user">
            <div className="assistant-body">
              <Prose text={asking} />
            </div>
          </li>
        )}
        {busy && (
          <li className="assistant working">
            <AgentMark size="sm" />
            <div className="assistant-body">
              {steps.length > 0 && <StepList steps={steps} done={false} />}
              {pending ? (
                <Prose text={pending} />
              ) : (
                <p className="assistant-working" role="status">
                  <span className="dots" aria-hidden="true">
                    <i />
                    <i />
                    <i />
                  </span>
                  {slow ? "Waiting for the agent…" : steps.length ? "Reading…" : "Thinking…"}
                </p>
              )}
            </div>
          </li>
        )}
      </ol>
      {error && (
        <p role="alert" className="assistant-error">
          {error}{" "}
          {lastBody && !busy && (
            <button type="button" onClick={() => void send("text" in lastBody ? { ...lastBody, text: text.trim() || lastBody.text } : lastBody)}>
              Try again
            </button>
          )}
        </p>
      )}
      <form className="assistant-composer" onSubmit={submit}>
        {slash.length > 0 && (
          <ul id="assistant-commands" className="assistant-commands" role="listbox" aria-label="Commands">
            {slash.map((c) => (
              <li key={c.name} id={`assistant-command-${c.name}`} role="option" aria-selected={c === chosen} className={c === chosen ? "active" : ""} onMouseDown={(e) => { e.preventDefault(); complete(c, true); }}>
                <strong>/{c.name}{c.argument ? ` <${c.argument}>` : ""}</strong> <span>{c.description}</span>
              </li>
            ))}
          </ul>
        )}
        <div className="assistant-field">
          <textarea
            ref={input}
            aria-label="Message the agent"
            role="combobox"
            aria-expanded={slash.length > 0}
            aria-controls="assistant-commands"
            aria-activedescendant={chosen && slash.length > 0 ? `assistant-command-${chosen.name}` : undefined}
            aria-autocomplete="list"
            value={text}
            maxLength={1000}
            rows={1}
            placeholder={executive ? "Ask about your reports, cash plan or budget" : closeId ? "Ask about this close, or / for commands" : "Ask about your work, or / for commands"}
            onChange={(e) => {
              setText(e.target.value);
              setSelected(0);
            }}
            onKeyDown={(e) => {
              if (slash.length > 0 && (e.key === "ArrowDown" || e.key === "ArrowUp")) {
                e.preventDefault();
                setSelected((n) => (e.key === "ArrowDown" ? n + 1 : n - 1 + slash.length) % slash.length);
              } else if (slash.length > 0 && chosen && e.key === "Tab" && !e.shiftKey && text !== `/${chosen.name}${chosen.argument ? " " : ""}`) {
                // Tab completes the command; once it is complete, Tab moves focus on as anywhere else.
                e.preventDefault();
                complete(chosen, false);
              } else if (slash.length > 0 && chosen && e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                // A command typed in full sends on Enter, with or without its optional argument; a partial one completes.
                if (text.trim() === `/${chosen.name}`) submitLine(text.trim());
                else complete(chosen, true);
              } else if (e.key === "Enter" && !e.shiftKey) submit(e);
              else if (e.key === "Escape" && menu) setMenu(false);
            }}
          />
          <button className="assistant-send" disabled={busy || !text.trim()} aria-label={busy ? "Answering" : "Send"}>
            <svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true">
              <path d="M12 19V5M5 12l7-7 7 7" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
          </button>
        </div>
        {empty && !about && <p className="assistant-note">Reads your firm's saved work with your permissions. Actions need your confirmation.</p>}
      </form>
    </aside>
  );
}

// Prose renders an answer's text: paragraphs, lists ("- " lines) and **bold**, nothing else, so a model's text can never
// carry markup into the page.
function Prose({ text }: { text: string }) {
  const blocks = text.split(/\n{2,}/).map((b) => b.trim()).filter(Boolean);
  return (
    <>
      {blocks.map((block, i) => {
        const lines = block.split("\n");
        // Numbered lines keep their order as an ordered list; bulleted lines are an unordered one.
        if (lines.length > 0 && lines.every((l) => /^\d+[.)]\s+/.test(l))) {
          return (
            <ol key={i}>
              {lines.map((l, n) => (
                <li key={n}>{bold(l.replace(/^\d+[.)]\s+/, ""))}</li>
              ))}
            </ol>
          );
        }
        if (lines.length > 0 && lines.every((l) => /^[-•*]\s+/.test(l))) {
          return (
            <ul key={i}>
              {lines.map((l, n) => (
                <li key={n}>{bold(l.replace(/^[-•*]\s+/, ""))}</li>
              ))}
            </ul>
          );
        }
        return <p key={i}>{bold(block)}</p>;
      })}
    </>
  );
}

function bold(line: string): ReactNode {
  const parts = line.split(/\*\*([^*]+)\*\*/);
  return parts.map((part, i) => (i % 2 === 1 ? <strong key={i}>{part}</strong> : part));
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
  if (!done)
    return (
      <ol className="assistant-steps" aria-label="What the agent is doing">
        {items}
      </ol>
    );
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

// Chats lists every chat of the member, newest first, grouped by day, each with its scope; one opens in its own scope.
function Chats({ closeId, current, onOpen, onNew }: { closeId: string; current: string; onOpen: (id: string, closeId: string) => void; onNew: () => void }) {
  const version = useSessionsVersion();
  const [sessions, setSessions] = useState<ChatSession[]>();
  const [more, setMore] = useState(false);
  const [filter, setFilter] = useState("");
  // The filter asks the server, so a chat on any page is found; it waits a moment for the typing to settle.
  useEffect(() => {
    let live = true;
    const timer = setTimeout(
      () => {
        loadSessions(50, 0, filter)
          .then((page) => live && (setSessions(page.sessions), setMore(page.hasMore)))
          .catch(() => live && setSessions([]));
      },
      filter ? 250 : 0,
    );
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [version, filter]);
  // Older chats come a page at a time, appended below, under the same filter; a page that arrives after the filter
  // or the list changed is dropped.
  const listing = useRef(0);
  useEffect(() => {
    listing.current += 1;
  }, [version, filter]);
  function showMore() {
    const asked = listing.current;
    loadSessions(50, sessions?.length ?? 0, filter)
      .then((page) => {
        if (asked !== listing.current) return;
        setSessions((current) => [...(current ?? []), ...page.sessions]);
        setMore(page.hasMore);
      })
      .catch(() => asked === listing.current && setMore(false));
  }
  const shown = sessions ?? [];
  const day = (iso: string) => {
    const at = new Date(iso);
    const today = new Date();
    const diff = Math.round((new Date(today.getFullYear(), today.getMonth(), today.getDate()).getTime() - new Date(at.getFullYear(), at.getMonth(), at.getDate()).getTime()) / 86400000);
    return diff <= 0 ? "Today" : diff === 1 ? "Yesterday" : diff < 7 ? "This week" : "Earlier";
  };
  const groups: { label: string; items: ChatSession[] }[] = [];
  for (const s of shown) {
    const label = day(s.last_at);
    const group = groups.find((g) => g.label === label);
    if (group) group.items.push(s);
    else groups.push({ label, items: [s] });
  }
  return (
    <section className="assistant-chats" aria-label="Your chats">
      <div className="assistant-chats-head">
        <input type="search" aria-label="Find a chat" placeholder="Find a chat" value={filter} onChange={(e) => setFilter(e.target.value)} />
        <button type="button" className="primary" onClick={onNew}>
          <Icon name="plus" size={14} /> New chat
        </button>
      </div>
      {sessions === undefined && <p role="status">Loading your chats…</p>}
      {sessions && shown.length === 0 && <p className="assistant-note">{filter ? "No chat matches." : "No chats yet. Ask anything to start one."}</p>}
      {groups.map((g) => (
        <div key={g.label}>
          <h3>{g.label}</h3>
          <ul>
            {g.items.map((s) => (
              <li key={s.id}>
                <button type="button" className={s.id === current ? "current" : ""} aria-current={s.id === current ? "true" : undefined} onClick={() => onOpen(s.id, s.close_id)}>
                  <span className="assistant-chat-title">{s.title}</span>
                  <span className="assistant-chat-scope">{s.close || "Firm-wide"}{s.close_id && s.close_id !== closeId ? " · opens its close" : ""}</span>
                </button>
              </li>
            ))}
          </ul>
        </div>
      ))}
      {more && (
        <button type="button" className="assistant-more" onClick={showMore}>
          Show older chats
        </button>
      )}
    </section>
  );
}
