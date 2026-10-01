import type { ReactNode } from "react";
import { Link } from "react-router";
import { Icon } from "./Icon";
import "./BackLink.css";

// The way back to the page this one belongs to, above its header.
export function BackLink({ to, children }: { to: string; children: ReactNode }) {
  return (
    <Link className="back-link" to={to}>
      <Icon name="back" size={15} /> {children}
    </Link>
  );
}
