import type { ReactNode } from "react";
import { Icon, type IconName } from "./Icon";
import "./Card.css";

// A tile's tint says what kind of thing the card is about, as the agent's notices do: doc for something to read,
// reply for something answered or done, wait for something that needs a person, done (the neutral fill) otherwise.
export type CardTone = "doc" | "reply" | "wait" | "done";

// Cards side by side, as many to a row as fit at 260px each (docs/design-system.md, Card).
export function Cards({ label, children }: { label?: string; children: ReactNode }) {
  return (
    <div className="ks-cards" role={label ? "group" : undefined} aria-label={label}>
      {children}
    </div>
  );
}

// A section card: its icon tile and title, one line on where it stands, and at its foot the way to change it and the
// state as a chip. Every change it offers is made in a Dialog, never in a fold-out.
export function Card({ icon, tone = "done", title, chip, action, children }: { icon: IconName; tone?: CardTone; title: string; chip?: ReactNode; action?: ReactNode; children: ReactNode }) {
  return (
    <section className="ks-card" aria-label={title}>
      <div className="ks-card-head">
        <span className={`ks-tile ${tone}`} aria-hidden="true">
          <Icon name={icon} size={14} />
        </span>
        <h3>{title}</h3>
      </div>
      <p>{children}</p>
      {(action || chip) && (
        <div className="ks-card-foot">
          {action}
          {chip}
        </div>
      )}
    </section>
  );
}
