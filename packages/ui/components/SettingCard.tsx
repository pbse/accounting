import type { ReactNode } from "react";
import { IconTile, type Tone } from "./IconTile";
import type { IconName } from "./Icon";
import "./SettingCard.css";

export type StatusTone = "on" | "off" | "info" | "warn";

// A setting's state as a chip with a dot: green when on, grey when off, blue for a value, amber when it needs someone.
export function StatusChip({ tone, children }: { tone: StatusTone; children: ReactNode }) {
  return (
    <span className={`status-chip status-chip-${tone}`}>
      <i aria-hidden="true" />
      {children}
    </span>
  );
}

// Cards of settings side by side, as many to a row as fit.
export function SettingGrid({ children }: { children: ReactNode }) {
  return <div className="setting-grid">{children}</div>;
}

// One setting as a card: its icon in a tinted circle, its state as a chip, its name, one line on where it stands,
// and at the foot the way to change it (the action) beside any word on the last change (the children).
export function SettingCard({
  icon,
  tone = "blue",
  title,
  summary,
  status,
  statusTone,
  action,
  children,
}: {
  icon: IconName;
  tone?: Tone;
  title: ReactNode;
  summary: ReactNode;
  status: ReactNode;
  statusTone: StatusTone;
  action?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <section className="setting-card" aria-label={typeof title === "string" ? title : undefined}>
      <div className="setting-card-head">
        <IconTile icon={icon} tone={tone} />
        <StatusChip tone={statusTone}>{status}</StatusChip>
      </div>
      <h3>{title}</h3>
      <p>{summary}</p>
      {(action || children) && (
        <div className="setting-card-foot">
          <div className="setting-card-note">{children}</div>
          {action}
        </div>
      )}
    </section>
  );
}
