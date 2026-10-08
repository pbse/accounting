import type { ReactNode } from "react";
import { Link } from "react-router";
import { Icon, type IconName } from "./Icon";
import "./VerticalTabs.css";

export type VerticalTab = { key: string; label: string; to: string; icon?: IconName };

// A page's sections as a column of links on the left, with the selected section's cards (the children) beside it.
// On a phone the column becomes a strip above the cards.
export function VerticalTabs({ label, items, value, children }: { label: string; items: VerticalTab[]; value: string; children: ReactNode }) {
  return (
    <div className="vertical-tabs-layout">
      <nav className="vertical-tabs" aria-label={label}>
        {items.map((item) => (
          <Link key={item.key} to={item.to} aria-current={value === item.key ? "page" : undefined}>
            {item.icon && <Icon name={item.icon} />}
            {item.label}
          </Link>
        ))}
      </nav>
      <div className="vertical-tabs-panel">{children}</div>
    </div>
  );
}
