import type { ReactNode } from "react";
import { Avatar } from "./Avatar";
import { Icon, type IconName } from "./Icon";
import "./PageHeader.css";

// The 68px bar at the top of a workspace page: an icon (or a client's avatar), the title, one line of
// description (the children) under it, and the page's own actions on the right. Its bottom line meets the
// sidebar header's.
export function PageHeader({
  title,
  icon,
  avatar,
  actions,
  children,
}: {
  title: ReactNode;
  icon?: IconName;
  avatar?: string;
  actions?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <div className="page-header">
      <div className="page-header-main">
        {avatar !== undefined ? (
          <Avatar label={avatar} />
        ) : (
          icon && (
            <span className="page-header-icon" aria-hidden="true">
              <Icon name={icon} size={24} />
            </span>
          )
        )}
        <span>
          <h1>{title}</h1>
          {children && <p>{children}</p>}
        </span>
      </div>
      {actions}
    </div>
  );
}
