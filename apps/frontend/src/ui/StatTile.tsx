import type { ReactNode } from "react";
import { Link } from "react-router";
import type { IconName } from "./Icon";
import { IconTile, type Tone } from "./IconTile";
import "./StatTile.css";

type Action = { to: string; onClick?: never } | { onClick: () => void; to?: never } | { to?: undefined; onClick?: undefined };

// A row of stat tiles in one white tray.
export function StatGroup({ children }: { children: ReactNode }) {
  return <div className="stat-group">{children}</div>;
}

// One number with its label and a caption that says what it covers. A tile with `to` or `onClick`
// is a link or a button; without either it only reads.
export function StatTile({
  icon,
  tone,
  label,
  value,
  caption,
  ...action
}: {
  icon: IconName;
  tone?: Tone;
  label: ReactNode;
  value?: ReactNode;
  caption: ReactNode;
} & Action) {
  const content = (
    <>
      <span className="stat-head">
        <IconTile icon={icon} tone={tone} />
        {label}
      </span>
      <span className="stat-body">
        <span className="stat-value">{value ?? "–"}</span>
        <span className="stat-caption">{caption}</span>
      </span>
    </>
  );
  if (action.to !== undefined)
    return (
      <Link className="stat-tile" to={action.to}>
        {content}
      </Link>
    );
  if (action.onClick)
    return (
      <button type="button" className="stat-tile" onClick={action.onClick}>
        {content}
      </button>
    );
  return <div className="stat-tile">{content}</div>;
}
