import type { ReactNode } from "react";
import "./SettingList.css";

export type SettingTone = "on" | "off" | "info" | "neutral" | "warn";

// A card of settings, each a row that opens: the row names the setting, says in a line where it stands, and shows
// its state as a pill; opening it shows what the setting means and, for those allowed, the way to change it.
export function SettingList({ title, intro, children }: { title: ReactNode; intro?: ReactNode; children: ReactNode }) {
  return (
    <section className="setting-list" aria-label={typeof title === "string" ? title : undefined}>
      <div className="setting-list-head">
        <h2>{title}</h2>
        {intro && <p>{intro}</p>}
      </div>
      <div className="setting-rows">{children}</div>
    </section>
  );
}

export function Setting({
  title,
  summary,
  status,
  tone = "neutral",
  open,
  children,
}: {
  title: ReactNode;
  summary?: ReactNode;
  status?: ReactNode;
  tone?: SettingTone;
  open?: boolean;
  children?: ReactNode;
}) {
  const head = (
    <>
      <span className="setting-text">
        <strong>{title}</strong>
        {summary && <small>{summary}</small>}
      </span>
      {status !== undefined && <span className={`setting-status setting-status-${tone}`}>{status}</span>}
    </>
  );
  // A setting with nothing more to show is a plain row, not something that opens.
  if (!children) return <div className="setting">{head}</div>;
  return (
    <details className="setting" open={open}>
      <summary>
        {head}
        <span className="setting-chevron" aria-hidden="true" />
      </summary>
      <div className="setting-body">{children}</div>
    </details>
  );
}
