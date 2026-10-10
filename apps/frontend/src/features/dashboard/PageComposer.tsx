import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Icon } from "@close/ui/components/Icon";
import { workspaceQuery } from "../../data/queries";
import { askAssistant, starterCommands, starters } from "./assistantStore";

// The composer at the foot of every page but Home (docs/design-system.md, Composer): a box with room for sentences
// and three controls, the plus (the asks the agent answers here), the scope, and send. What is sent opens the agent
// beside the page, in the close's conversation or the firm's; nothing runs from the page itself.
export function PageComposer({ closeId, executive }: { closeId: string; executive: boolean }) {
  const [text, setText] = useState("");
  const [menu, setMenu] = useState(false);
  const box = useRef<HTMLTextAreaElement>(null);
  const close = useQuery({ ...workspaceQuery<{ close: { entity: string; period: string } }>(`/${closeId}`), enabled: !!closeId }).data?.close;
  const scope = executive ? "Your business" : close ? `${close.entity} · ${close.period}` : closeId ? `Close #${closeId}` : "Firm-wide";
  const asks = executive ? starters.executive : closeId ? starters.close : starters.firm;
  const send = (what: string) => {
    const line = what.trim();
    if (!line) return;
    askAssistant(!executive && starterCommands[line] ? starterCommands[line] : line, closeId);
    setText("");
    setMenu(false);
  };
  // The box grows with its text, to seven lines.
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 7 * 24 + 4)}px`;
  }, [text]);
  return (
    <div className="ks-composer foot">
      <form
        className="ks-box"
        onSubmit={(event) => {
          event.preventDefault();
          send(text);
        }}
      >
        <textarea
          ref={box}
          id="page-composer"
          aria-label="Message the agent from this page"
          value={text}
          maxLength={1000}
          rows={2}
          placeholder={executive ? "Ask about your reports, cash plan or budget" : closeId ? "Ask about this close, or tell the agent what to do" : "Ask anything, or tell the agent what to do"}
          onChange={(event) => setText(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter" && !event.shiftKey) {
              event.preventDefault();
              send(text);
            } else if (event.key === "Escape" && menu) setMenu(false);
          }}
        />
        <div className="ks-box-row">
          <span className="ks-plus">
            <button type="button" className="ks-round" aria-label="Choose an ask" aria-haspopup="menu" aria-expanded={menu} onClick={() => setMenu((v) => !v)}>
              <Icon name="plus" size={18} />
            </button>
            {menu && (
              <div className="ks-menu" role="menu" aria-label="Asks">
                <div className="ks-menu-head">Ask</div>
                {asks.map((ask) => (
                  <button type="button" role="menuitem" key={ask} onClick={() => send(ask)}>
                    <Icon name="agent" />
                    {ask}
                  </button>
                ))}
              </div>
            )}
          </span>
          <span className="ks-scope">{scope}</span>
          <span className="ks-spacer" />
          <button type="submit" className="ks-send" aria-label="Send" disabled={!text.trim()}>
            <Icon name="up" />
          </button>
        </div>
      </form>
    </div>
  );
}
