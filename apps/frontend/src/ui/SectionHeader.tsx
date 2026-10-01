import type { ReactNode } from "react";
import "./SectionHeader.css";

// A section's heading with an optional aside on the right: a count, a note or the section's actions.
export function SectionHeader({ title, id, level = 2, children }: { title: ReactNode; id?: string; level?: 1 | 2; children?: ReactNode }) {
  const Heading = level === 1 ? "h1" : "h2";
  return (
    <div className="section-header" id={id}>
      <Heading>{title}</Heading>
      {children}
    </div>
  );
}
