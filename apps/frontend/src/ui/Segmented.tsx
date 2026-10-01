import type { ReactNode } from "react";
import { Link } from "react-router";
import "./Segmented.css";

export type Segment = {
  key: string;
  label: ReactNode;
  // A count after the label; `alert` adds a red dot when the count needs a person.
  count?: number;
  alert?: boolean;
  // A segment with `to` is a link (a tab of the page); without it, a toggle button.
  to?: string;
};

// A grey track of segments with the selected one raised in white.
export function Segmented({ label, items, value, onChange }: { label?: string; items: Segment[]; value: string; onChange?: (key: string) => void }) {
  return (
    <div className="segmented" role={label ? "group" : undefined} aria-label={label}>
      {items.map((item) => {
        const count = item.count !== undefined && <span className={item.alert ? "count alert" : "count"}>{item.count}</span>;
        return item.to !== undefined ? (
          <Link key={item.key} to={item.to} aria-current={value === item.key ? "page" : undefined}>
            {item.label}
            {count}
          </Link>
        ) : (
          <button key={item.key} type="button" aria-pressed={value === item.key} onClick={() => onChange?.(item.key)}>
            {item.label}
            {count}
          </button>
        );
      })}
    </div>
  );
}
