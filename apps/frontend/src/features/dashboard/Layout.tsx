import { Suspense, useEffect, useRef } from "react";
import { Link, NavLink, Outlet, useNavigate } from "react-router";
import { SidebarAccount } from "./Access";
import { Assistant } from "./Assistant";
import { Icon, type IconName } from "./Icon";

const groups: { label: string; items: { to: string; label: string; icon: IconName; end?: boolean }[] }[] = [
  { label: "Essentials", items: [{ to: "/dashboard", label: "Work", icon: "work", end: true }] },
  { label: "Clients", items: [{ to: "/dashboard/closes", label: "Client closes", icon: "clients" }] },
  {
    label: "Prepare",
    items: [
      { to: "/dashboard/prepaids/new", label: "Prepaid schedule", icon: "prepaid" },
      { to: "/dashboard/accruals/new", label: "Accrued expense", icon: "accrual" },
      { to: "/dashboard/loans/new", label: "Loan schedule", icon: "loan" },
      { to: "/dashboard/owners/new", label: "Owner items", icon: "owner" },
      { to: "/dashboard/reconciliations/new", label: "Statement reconciliation", icon: "reconcile" },
      { to: "/dashboard/settlements/new", label: "Stripe payouts", icon: "stripe" },
    ],
  },
  { label: "Measure", items: [{ to: "/dashboard/measure", label: "Human effort", icon: "measure" }] },
  { label: "Firm", items: [{ to: "/dashboard/firm", label: "Firm settings", icon: "firm" }] },
  {
    label: "Examples",
    items: [
      { to: "/dashboard/close", label: "Example close", icon: "book" },
      { to: "/dashboard/review", label: "Example review", icon: "book" },
    ],
  },
];

export default function WorkspaceLayout() {
  const search = useRef<HTMLInputElement>(null);
  const navigate = useNavigate();
  useEffect(() => {
    const focusSearch = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement;
      if (event.key !== "/" || target.closest("input, textarea, select, [contenteditable]")) return;
      event.preventDefault();
      search.current?.focus();
    };
    window.addEventListener("keydown", focusSearch);
    return () => window.removeEventListener("keydown", focusSearch);
  }, []);
  return (
    <div className="app-shell">
      <a className="skip" href="#main">
        Skip to content
      </a>
      <aside className="sidebar">
        <Link className="sidebar-brand" to="/dashboard">
          <span className="logo" aria-hidden="true">c</span>
          <span>
            <strong>Close</strong>
            <small>Preparation workspace</small>
          </span>
        </Link>
        <form
          role="search"
          onSubmit={(event) => {
            event.preventDefault();
            const query = String(new FormData(event.currentTarget).get("q") ?? "").trim();
            navigate(`/dashboard/closes${query ? `?q=${encodeURIComponent(query)}` : ""}`);
          }}
        >
          <label className="sidebar-search">
            <Icon name="search" size={16} />
            <input ref={search} name="q" aria-label="Search clients" placeholder="Search clients" maxLength={200} autoComplete="off" />
            <kbd aria-hidden="true">/</kbd>
          </label>
        </form>
        <nav aria-label="Workspace">
          {groups.map((group) => (
            <div className="nav-group" key={group.label}>
              <div className="nav-label">{group.label}</div>
              {group.items.map((item) => (
                <NavLink className="nav-item" end={item.end} to={item.to} key={item.to}>
                  <Icon name={item.icon} />
                  {item.label}
                </NavLink>
              ))}
            </div>
          ))}
        </nav>
        <div className="sidebar-footer">
          <SidebarAccount />
          <p>Sandbox posting only. Production posting requires separate client authority.</p>
        </div>
      </aside>
      <main id="main" className="app-main">
        <div className="page">
          <Suspense fallback={<p role="status">Opening…</p>}>
            <Outlet />
          </Suspense>
        </div>
      </main>
      <Assistant />
    </div>
  );
}
