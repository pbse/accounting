import type { ReactNode } from "react";
import { Avatar } from "./Avatar";
import "./PageHeader.css";

// The title row at the top of a workspace page: the title in the serif (led by a client's avatar on its page), one
// line of description (the children) under it, and the page's own actions on the right.
export function PageHeader({ title, avatar, actions, children }: { title: ReactNode; avatar?: string; actions?: ReactNode; children?: ReactNode }) {
  return (
    <div className="page-header">
      <div className="page-header-main">
        {avatar !== undefined && <Avatar label={avatar} />}
        <span>
          <h1>{title}</h1>
          {children && <p>{children}</p>}
        </span>
      </div>
      {actions}
    </div>
  );
}
