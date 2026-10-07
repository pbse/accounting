import { Suspense, useEffect, useRef, useState } from "react";
import { Link, Navigate, NavLink, Outlet, useLocation, useNavigate } from "react-router";
import { SidebarAccount } from "./Access";
import { Assistant, useSignedIn } from "./Assistant";
import { openAssistant } from "./assistantStore";
import { AttentionBadge } from "../closes/Attention";
import { workspaceRequest } from "../closes/workspace";
import { authKitClientId, useSession } from "../auth/Auth";
import { Icon, type IconName } from "@close/ui/components/Icon";
import { useShellThemeColor } from "@close/ui/theme/useShellThemeColor";

const groups: { label: string; items: { to: string; label: string; icon: IconName; end?: boolean }[] }[] = [
  {
    label: "Essentials",
    items: [
      { to: "/dashboard", label: "Work", icon: "work", end: true },
      { to: "/dashboard/attention", label: "Attention", icon: "decision" },
    ],
  },
  { label: "Clients", items: [{ to: "/dashboard/closes", label: "Client closes", icon: "clients" }] },
  {
    label: "Prepare",
    items: [
      { to: "/dashboard/prepaids/new", label: "Prepaid schedule", icon: "prepaid" },
      { to: "/dashboard/accruals/new", label: "Accrued expense", icon: "accrual" },
      { to: "/dashboard/loans/new", label: "Loan schedule", icon: "loan" },
      { to: "/dashboard/owners/new", label: "Owner items", icon: "owner" },
      { to: "/dashboard/payroll/new", label: "Payroll", icon: "book" },
      { to: "/dashboard/reconciliations/new", label: "Statement reconciliation", icon: "reconcile" },
      { to: "/dashboard/settlements/new", label: "Stripe payouts", icon: "stripe" },
    ],
  },
  { label: "Measure", items: [{ to: "/dashboard/measure", label: "Human effort", icon: "measure" }] },
  {
    label: "Firm",
    items: [
      { to: "/dashboard/firm", label: "Firm settings", icon: "firm" },
      { to: "/dashboard/security", label: "Security", icon: "shield" },
    ],
  },
  // Only Krith's own firm has the worked example (see examples.go).
  {
    label: "Examples",
    items: [
      { to: "/dashboard/close", label: "Example close", icon: "book" },
      { to: "/dashboard/review", label: "Example review", icon: "book" },
    ],
  },
];

// A client's executive sees their business results and nothing of the firm's workspace; the server enforces the same.
// Links keep the client and month they chose.
const executiveGroups = (close: string | null): typeof groups => [
  {
    label: "Business",
    items: ([
      { to: "/dashboard/executive", label: "Business overview", icon: "measure", end: true },
      { to: "/dashboard/executive/reports", label: "Reports", icon: "file" },
      { to: "/dashboard/executive/cash", label: "Cash plan", icon: "reconcile" },
      { to: "/dashboard/executive/decisions", label: "My decisions", icon: "decision" },
    ] as (typeof groups)[number]["items"]).map((item) => ({ ...item, to: close ? `${item.to}?close=${encodeURIComponent(close)}` : item.to })),
  },
];

export default function WorkspaceLayout() {
  const search = useRef<HTMLInputElement>(null);
  const navigate = useNavigate();
  const location = useLocation();
  const signedIn = useSignedIn();
  // Unknown until the server says; nothing of the workspace shows before then, so an executive never sees it.
  const [executive, setExecutive] = useState<boolean>();
  const [examples, setExamples] = useState(false);
  // A hosted session still loading is not yet signed out.
  const sessionLoading = authKitClientId ? useSession().isLoading : false;
  const known = !sessionLoading && (!signedIn || executive !== undefined);
  useEffect(() => {
    if (!signedIn) return;
    let live = true;
    workspaceRequest("/executive")
      .then((data) => live && setExecutive(!!data.executive))
      .catch(() => live && setExecutive(false));
    return () => {
      live = false;
    };
  }, [signedIn]);
  useEffect(() => {
    if (executive && !location.pathname.startsWith("/dashboard/executive")) navigate("/dashboard/executive", { replace: true });
  }, [executive, location.pathname, navigate]);
  useEffect(() => {
    if (executive !== false) return;
    let live = true;
    workspaceRequest("/examples")
      .then((data) => live && setExamples(!!data.available))
      .catch(() => live && setExamples(false));
    return () => {
      live = false;
    };
  }, [executive]);
  useShellThemeColor();
  useEffect(() => {
    const focusSearch = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement;
      if (event.key !== "/" || target.closest("input, textarea, select, [contenteditable]") || !search.current) return;
      event.preventDefault();
      search.current.focus();
    };
    window.addEventListener("keydown", focusSearch);
    return () => window.removeEventListener("keydown", focusSearch);
  }, []);
  // Signed out, the workspace shows nothing of itself: the visitor signs in and comes back to this page.
  if (authKitClientId && !sessionLoading && !signedIn) return <Navigate to={`/login?return=${encodeURIComponent(location.pathname + location.search)}`} replace />;
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
        {known && !executive && <form
          role="search"
          onSubmit={(event) => {
            event.preventDefault();
            const query = String(new FormData(event.currentTarget).get("q") ?? "").trim();
            navigate(`/dashboard/closes${query ? `?q=${encodeURIComponent(query)}` : ""}`);
          }}
        >
          <label className="sidebar-search">
            <Icon name="search" />
            <input ref={search} name="q" aria-label="Search clients" placeholder="Search clients" maxLength={200} autoComplete="off" />
            <kbd aria-hidden="true">/</kbd>
          </label>
        </form>}
        <nav aria-label="Workspace">
          {(executive ? executiveGroups(new URLSearchParams(location.search).get("close")) : known ? groups.filter((group) => examples || group.label !== "Examples") : []).map((group) => (
            <div className="nav-group" key={group.label}>
              <div className="nav-label">{group.label}</div>
              {group.items.map((item) => (
                <NavLink className="nav-item" end={item.end} to={item.to} key={item.to}>
                  <Icon name={item.icon} />
                  {item.label}
                  {item.to === "/dashboard/attention" && signedIn && <AttentionBadge />}
                </NavLink>
              ))}
              {group.label === "Essentials" && signedIn && (
                <button type="button" className="nav-item" onClick={openAssistant}>
                  <Icon name="agent" />
                  Ask the agent
                </button>
              )}
            </div>
          ))}
        </nav>
        <div className="sidebar-footer">
          <SidebarAccount />
          <p>Nothing posts on its own. A person previews, approves and executes each posting, to a sandbox or, under the client's grant, to the client's own ledger; each is attempted once and counts as posted only once its readback matches.</p>
        </div>
      </aside>
      <main id="main" className="app-main">
        <div className="page">
          <Suspense fallback={<p role="status">Opening…</p>}>
            {known ? <Outlet /> : <p role="status">Opening…</p>}
          </Suspense>
        </div>
      </main>
      {executive === false && <Assistant />}
    </div>
  );
}
