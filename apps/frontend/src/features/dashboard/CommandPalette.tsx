import { useEffect, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router";
import { workspaceRequest } from "../closes/workspace";
import { askAssistant, nextReview } from "./assistantStore";
import { commands, parseCommand, runCommand, type Ask, type Command } from "./commands";

type Close = { id: string; entity: string; period: string };
type Option = { key: string; label: string; detail: string; run: () => void };

// openPalette opens the palette with a command typed elsewhere, such as a close command in the firm-wide assistant.
export function openPalette(text: string, account = "") {
  window.dispatchEvent(new CustomEvent("palette:open", { detail: { text, account } }));
}

export function CommandPalette() {
  const [initial, setInitial] = useState<{ text: string; account: string } | null>(null);
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && !event.shiftKey && !event.altKey && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setInitial((value) => (value === null ? { text: "", account: "" } : null));
      }
    };
    const onOpen = (event: Event) => setInitial((event as CustomEvent<{ text: string; account: string }>).detail);
    window.addEventListener("keydown", onKey);
    window.addEventListener("palette:open", onOpen);
    return () => {
      window.removeEventListener("keydown", onKey);
      window.removeEventListener("palette:open", onOpen);
    };
  }, []);
  return initial === null ? null : <Palette key={initial.text + "|" + initial.account} initial={initial.text} account={initial.account} onClose={() => setInitial(null)} />;
}

function Palette({ initial, account, onClose }: { initial: string; account: string; onClose: () => void }) {
  const location = useLocation();
  const navigate = useNavigate();
  const here = /^\/dashboard\/closes\/(\d+)/.exec(location.pathname)?.[1] ?? "";
  const [text, setText] = useState(initial);
  const [active, setActive] = useState(0);
  const [chosen, setChosen] = useState<Close>();
  const [found, setFound] = useState<{ query: string; closes: Close[] }>();
  const [message, setMessage] = useState("");
  const input = useRef<HTMLInputElement>(null);
  const opener = useRef(document.activeElement as HTMLElement | null);
  useEffect(() => {
    input.current?.focus();
    return () => opener.current?.focus();
  }, []);
  const { command, argument } = parseCommand(text);
  const target = chosen?.id ?? here;
  const pickClose = !!command?.close && !target;
  // Outside a close, what follows a close command searches closes; once one is chosen it is the command's argument.
  useEffect(() => {
    if (!pickClose) return;
    let live = true;
    workspaceRequest(argument ? `?q=${encodeURIComponent(argument)}` : "")
      .then((data) => live && setFound({ query: argument, closes: (data.closes as Close[]).slice(0, 8) }))
      .catch(() => live && setFound({ query: argument, closes: [] }));
    return () => {
      live = false;
    };
  }, [pickClose, argument]);
  function run(cmd: Command, arg: string, closeId: string) {
    const ctx = {
      closeId,
      navigate,
      nextReview,
      ask: (what: Ask, close: string) => {
        if (close !== here) navigate(close ? `/dashboard/closes/${close}` : "/dashboard");
        askAssistant(what, close);
      },
    };
    void runCommand(cmd, arg, ctx)
      .then((result) => (result ? setMessage(result) : onClose()))
      .catch((e) => setMessage(e instanceof Error ? e.message : "The command could not run."));
  }
  function choose(cmd: Command, close: Close) {
    if (cmd.argument) {
      setChosen(close);
      setText(`/${cmd.name} ${account}`);
      setActive(0);
      input.current?.focus();
    } else run(cmd, "", close.id);
  }
  const query = text.trim().replace(/^\//, "").toLowerCase();
  // Results show only for the search that produced them, so an earlier search is never chosen.
  const closes = found?.query === argument ? found.closes : null;
  const options: Option[] = !command
    ? commands
        .filter((c) => c.name.startsWith(query) || c.description.toLowerCase().includes(query))
        .map((c) => ({ key: c.name, label: `/${c.name}${c.argument ? ` <${c.argument}>` : ""}`, detail: c.description, run: () => (c.argument || (c.close && !target) ? setText(`/${c.name} `) : run(c, "", target)) }))
    : pickClose
      ? (closes ?? []).map((c) => ({ key: c.id, label: `${c.entity} · ${c.period}`, detail: `/${command.name}`, run: () => choose(command, c) }))
      : [{ key: "run", label: `/${command.name}${argument ? ` ${argument}` : ""}`, detail: chosen ? `${command.description} · ${chosen.entity} · ${chosen.period}` : command.description, run: () => run(command, argument, target) }];
  const selected = Math.min(active, Math.max(options.length - 1, 0));
  return (
    <div className="palette-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="palette" role="dialog" aria-modal="true" aria-label="Command palette" onKeyDown={(e) => e.key === "Tab" && e.preventDefault()}>
        {chosen && <p className="assistant-context"><span className="pill">{chosen.entity} · {chosen.period}</span></p>}
        <input
          ref={input}
          role="combobox"
          aria-expanded={options.length > 0}
          aria-controls="palette-options"
          aria-activedescendant={options[selected] ? `palette-${options[selected].key}` : undefined}
          aria-label="Type a command"
          placeholder={target ? "Type a command, such as /status" : "Type a command, such as /review or /prepare Acme"}
          value={text}
          onChange={(e) => {
            setText(e.target.value);
            setActive(0);
            setMessage("");
          }}
          onKeyDown={(e) => {
            if (e.key === "Escape") onClose();
            else if (e.key === "ArrowDown") {
              e.preventDefault();
              setActive((selected + 1) % Math.max(options.length, 1));
            } else if (e.key === "ArrowUp") {
              e.preventDefault();
              setActive((selected - 1 + options.length) % Math.max(options.length, 1));
            } else if (e.key === "Enter") {
              e.preventDefault();
              options[selected]?.run();
            }
          }}
        />
        <ul id="palette-options" role="listbox" aria-label="Commands">
          {options.map((o, i) => (
            <li key={o.key} id={`palette-${o.key}`} role="option" aria-selected={i === selected} className={i === selected ? "active" : ""} onMouseDown={(e) => { e.preventDefault(); o.run(); }}>
              <strong>{o.label}</strong> <span>{o.detail}</span>
            </li>
          ))}
          {pickClose && closes === null && <li className="palette-empty">Searching your closes…</li>}
          {pickClose && closes?.length === 0 && <li className="palette-empty">No accessible close matches. Type a client name or YYYY-MM.</li>}
        </ul>
        {message && <p role="alert">{message}</p>}
        <p className="palette-note">Commands open the screen that does the work or ask the assistant; actions still need their preview and confirmation.</p>
      </div>
    </div>
  );
}
