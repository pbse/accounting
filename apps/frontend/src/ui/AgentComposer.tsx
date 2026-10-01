import type { ReactNode } from "react";
import { AgentMark } from "./AgentMark";
import { Suggestions } from "./Suggestions";
import "./AgentComposer.css";

// The card that leads a page with the agent: what it can do, a large input, one-click asks, then whatever
// the caller shows below (help, previews, results).
export function AgentComposer({
  title,
  description,
  value,
  onChange,
  onSubmit,
  busy,
  placeholder,
  submitLabel,
  suggestions,
  onSuggestion,
  children,
}: {
  title: string;
  description: ReactNode;
  value: string;
  onChange: (value: string) => void;
  onSubmit: () => void;
  busy: boolean;
  placeholder: string;
  submitLabel: string;
  suggestions: readonly string[];
  onSuggestion: (text: string) => void;
  children?: ReactNode;
}) {
  return (
    <section className="agent-composer" aria-label={title}>
      <div className="agent-composer-head">
        <AgentMark />
        <span>
          <h2>{title}</h2>
          <p>{description}</p>
        </span>
      </div>
      <form
        className="agent-composer-ask"
        onSubmit={(e) => {
          e.preventDefault();
          if (!busy && value.trim()) onSubmit();
        }}
      >
        <input aria-label={title} value={value} readOnly={busy} onChange={(e) => onChange(e.target.value)} placeholder={placeholder} maxLength={220} />
        <button className="primary" disabled={busy || !value.trim()}>
          {busy ? "Working…" : submitLabel}
        </button>
      </form>
      <Suggestions label="Suggestions" items={suggestions} onPick={onSuggestion} disabled={busy} />
      {children}
    </section>
  );
}
