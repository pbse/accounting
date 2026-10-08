import type { ReactNode } from "react";
import { Icon } from "./Icon";
import "./AgentMark.css";

// The agent's mark: a solid blue tile with the sparkle. With children it leads a label, such as the
// heading of something the agent wrote.
export function AgentMark({ size = "md", children }: { size?: "sm" | "md"; children?: ReactNode }) {
  const mark = (
    <span className={`agent-mark agent-mark-${size}`} aria-hidden="true">
      <Icon name="agent" size={size === "sm" ? 14 : 16} />
    </span>
  );
  if (children === undefined) return mark;
  return (
    <span className="agent-label">
      {mark}
      {children}
    </span>
  );
}
