import type { ReactNode } from "react";
import { Avatar } from "./Avatar";
import "./RowList.css";

// A white tray of rows, 4px apart, with optional column names above them. Rows are `.row` elements
// (links, summaries or plain blocks) laid out on the same four-column grid as the names.
export function RowList({ columns, label, children }: { columns?: ReactNode[]; label?: string; children: ReactNode }) {
  return (
    <div className="row-list" aria-label={label}>
      {columns && (
        <div className="row-list-head">
          {columns.map((column, index) => (
            <span key={index}>{column}</span>
          ))}
        </div>
      )}
      {children}
    </div>
  );
}

// A row's first cell: the client's avatar (or another leading mark such as a checkbox), a title, a line of
// detail, and any further lines as children.
export function EntityCell({
  name,
  leading,
  title,
  detail,
  children,
}: {
  name?: string;
  leading?: ReactNode;
  title: ReactNode;
  detail?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <span className="entity-cell">
      {leading ?? (name !== undefined && <Avatar label={name} />)}
      <span>
        <strong>{title}</strong>
        {detail !== undefined && <small>{detail}</small>}
        {children}
      </span>
    </span>
  );
}
