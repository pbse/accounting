import type { ReactNode } from "react";
import { Link } from "react-router";
import { Icon, type IconName } from "./Icon";
import "./VerticalTabs.css";

export type VerticalTab = { key: string; label: string; to: string; icon?: IconName; group?: string };

// A page's sections as a column of links on the left, under their group names when the items name one, with the
// selected section's cards (the children) beside it. On a phone the column becomes a strip above the cards that
// scrolls sideways.
export function VerticalTabs({ label, items, value, children }: { label: string; items: VerticalTab[]; value: string; children: ReactNode }) {
  return (
    <div className="vertical-tabs-layout">
      <nav className="vertical-tabs" aria-label={label}>
        {items.map((item, index) => (
          <span key={item.key} className="vertical-tabs-item">
            {item.group && item.group !== items[index - 1]?.group && <span className="vertical-tabs-group">{item.group}</span>}
            <Link to={item.to} aria-current={value === item.key ? "page" : undefined}>
              {item.icon && <Icon name={item.icon} />}
              {item.label}
            </Link>
          </span>
        ))}
      </nav>
      <div className="vertical-tabs-panel">{children}</div>
    </div>
  );
}
