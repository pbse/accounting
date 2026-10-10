import { useEffect, useRef, useState, type ReactNode } from "react";
import { Link, NavLink, useLocation } from "react-router";
import { Icon, type IconName } from "./Icon";
import "./Sidebar.css";

// The Krith sidebar both apps share (docs/design-system.md, The shell), in its fixed order: the wordmark and the
// collapse control, the firm line, New chat, the pages (a module is a group that opens to its pages), whatever the app
// puts beneath (chats, notices), and at the foot the quiet links and the person. 256px open, 56px of icons collapsed;
// below 860px a sheet opened from the phone bar's brand button.

export type SidebarPage = { to: string; label: string; icon: IconName; end?: boolean; count?: ReactNode };
export type SidebarGroup = { label: string; icon: IconName; pages: { to: string; label: string; end?: boolean }[] };
export type SidebarItem = SidebarPage | SidebarGroup;

type Props = {
  brandTo: string;
  brandLabel: string;
  /** The line under the wordmark: the firm, or what this console is. */
  line?: ReactNode;
  newChat?: { onClick: () => void; hint?: string };
  items: SidebarItem[];
  navLabel: string;
  /** The sections between the pages and the foot: chats, notices. */
  children?: ReactNode;
  /** The quiet links above the person: Settings, Help. */
  foot?: ReactNode;
  /** The person, which opens the account menu. */
  person: ReactNode;
};

const collapsedKey = "ks-side-collapsed";

function isGroup(item: SidebarItem): item is SidebarGroup {
  return "pages" in item;
}

export function Sidebar({ brandTo, brandLabel, line, newChat, items, navLabel, children, foot, person }: Props) {
  const location = useLocation();
  const [collapsed, setCollapsed] = useState(false);
  const [open, setOpen] = useState(false);
  useEffect(() => {
    try {
      setCollapsed(localStorage.getItem(collapsedKey) === "1");
    } catch {
      /* a private window keeps the sidebar open */
    }
  }, []);
  // The sheet closes when the person goes somewhere, and on Escape, which hands focus back to the menu button.
  useEffect(() => setOpen(false), [location.pathname, location.search]);
  const menuButton = useRef<HTMLButtonElement>(null);
  const sheet = useRef<HTMLElement>(null);
  const [restore, setRestore] = useState(false);
  useEffect(() => {
    if (!open) return;
    // Focus moves into the sheet; the phone bar and the page behind it leave the tab order while it is open.
    sheet.current?.focus();
    const page = document.getElementById("main");
    page?.setAttribute("inert", "");
    const key = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      setOpen(false);
      setRestore(true);
    };
    window.addEventListener("keydown", key);
    return () => {
      window.removeEventListener("keydown", key);
      page?.removeAttribute("inert");
    };
  }, [open]);
  // Escape hands focus back to the menu button once the bar is no longer inert.
  useEffect(() => {
    if (open || !restore) return;
    menuButton.current?.focus();
    setRestore(false);
  }, [open, restore]);
  const toggleCollapsed = () => {
    const next = !collapsed;
    setCollapsed(next);
    try {
      localStorage.setItem(collapsedKey, next ? "1" : "0");
    } catch {
      /* not remembered */
    }
  };
  return (
    <>
      <div className="ks-phonebar" inert={open || undefined}>
        <button type="button" className="ks-phonebar-brand" aria-label="Open the menu" aria-expanded={open} onClick={() => setOpen(!open)} ref={menuButton}>
          <span className="ks-wordmark">krith</span>
        </button>
        {newChat && (
          <button type="button" className="ks-phonebar-new" aria-label="New chat" onClick={newChat.onClick}>
            <Icon name="plus" />
          </button>
        )}
      </div>
      {open && <div className="ks-backdrop" onClick={() => setOpen(false)} />}
      <nav
        className={`ks-side${collapsed ? " collapsed" : ""}${open ? " open" : ""}`}
        aria-label={navLabel}
        ref={sheet}
        tabIndex={-1}
        // The sheet closes on anything that goes somewhere or opens a chat; opening a group, collapsing or the person's
        // menu keep it.
        onClick={(event) => {
          const control = (event.target as HTMLElement).closest("a, button");
          if (control && !control.closest(".ks-group, .ks-collapse, .ks-me")) setOpen(false);
        }}
      >
        <div className="ks-brand">
          <Link className="ks-wordmark" to={brandTo} aria-label={brandLabel}>
            krith
          </Link>
          <button type="button" className="ks-collapse" aria-label={collapsed ? "Expand the sidebar" : "Collapse the sidebar"} aria-pressed={collapsed} onClick={toggleCollapsed}>
            <Icon name={collapsed ? "sidebar-open" : "sidebar"} />
          </button>
        </div>
        {line && <div className="ks-firm">{line}</div>}
        {newChat && (
          <button type="button" className="ks-new-chat" onClick={newChat.onClick} title="New chat" aria-label="New chat">
            <Icon name="plus" />
            <span className="ks-label">New chat</span>
            {newChat.hint && <kbd className="ks-kbd">{newChat.hint}</kbd>}
          </button>
        )}
        <div className="ks-pages">
          {items.map((item) => (isGroup(item) ? <Group key={item.label} group={item} path={location.pathname} collapsed={collapsed} expand={toggleCollapsed} /> : <Page key={item.to} item={item} />))}
        </div>
        {children}
        <div className="ks-foot">
          {foot}
          {person}
        </div>
      </nav>
    </>
  );
}

function Page({ item }: { item: SidebarPage }) {
  return (
    <NavLink className="ks-nav" end={item.end} to={item.to} title={item.label} aria-label={item.label}>
      <Icon name={item.icon} />
      <span className="ks-label">{item.label}</span>
      {item.count}
    </NavLink>
  );
}

// A module is one line until opened; the one the person is in stays open and reads as current. Collapsed to icons,
// opening a module expands the sidebar, so its pages are always reachable.
function Group({ group, path, collapsed, expand }: { group: SidebarGroup; path: string; collapsed: boolean; expand: () => void }) {
  const inside = group.pages.some((p) => (p.end ? path === p.to : path.startsWith(p.to)));
  const [open, setOpen] = useState(inside);
  useEffect(() => {
    if (inside) setOpen(true);
  }, [inside]);
  const toggle = () => {
    if (collapsed) {
      expand();
      setOpen(true);
    } else setOpen(!open);
  };
  return (
    <>
      <button type="button" className={`ks-nav ks-group${inside ? " has-current" : ""}`} aria-expanded={open && !collapsed} onClick={toggle} title={group.label} aria-label={group.label}>
        <Icon name={group.icon} />
        <span className="ks-label">{group.label}</span>
        <Icon name="chevron" className="ks-chev" />
      </button>
      <div className={`ks-sub${open ? " open" : ""}`}>
        {group.pages.map((p) => (
          <NavLink className="ks-nav" end={p.end} to={p.to} key={p.to}>
            <span className="ks-label">{p.label}</span>
          </NavLink>
        ))}
      </div>
    </>
  );
}

// A category label above a section, with an optional count or action on the right.
export function SidebarCategory({ children, aside }: { children: ReactNode; aside?: ReactNode }) {
  return (
    <div className="ks-cat">
      <span className="ks-label">{children}</span>
      {aside}
    </div>
  );
}

// A quiet link at the foot: Settings, Help.
export function SidebarFootLink({ to, icon, children }: { to: string; icon: IconName; children: ReactNode }) {
  return (
    <NavLink className="ks-nav" to={to} title={typeof children === "string" ? children : undefined} aria-label={typeof children === "string" ? children : undefined}>
      <Icon name={icon} />
      <span className="ks-label">{children}</span>
    </NavLink>
  );
}
