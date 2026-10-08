import { Suspense, useEffect, useRef, useState } from "react";
import { Link, Navigate, NavLink, Outlet, useLocation, useNavigate } from "react-router";
import { AccountMenu } from "./Access";
import { Assistant, useSignedIn } from "./Assistant";
import { openAssistant } from "./assistantStore";
import { AttentionBadge } from "../closes/Attention";
import { workspaceRequest } from "../closes/workspace";
import { authKitClientId, useSession } from "../auth/Auth";
import { Icon, type IconName } from "@close/ui/components/Icon";
import { useShellThemeColor } from "@close/ui/theme/useShellThemeColor";

type NavItem = { to: string; label: string; icon: IconName; end?: boolean };

// The firm's pages. Security and Measure open from Firm and the preparation forms from a close; neither needs a place
// in the bar. Only Krith's own firm has the worked example (see examples.go), and sees it here.
const firmNav: NavItem[] = [
  { to: "/dashboard", label: "Work", icon: "work", end: true },
  { to: "/dashboard/attention", label: "Attention", icon: "decision" },
  { to: "/dashboard/closes", label: "Clients", icon: "clients" },
  { to: "/dashboard/firm", label: "Firm", icon: "firm" },
];
const exampleNav: NavItem = { to: "/dashboard/close", label: "Example", icon: "book" };

// A client's executive sees their business results and nothing of the firm's workspace; the server enforces the same.
// Links keep the client and month they chose.
const executiveNav = (close: string | null): NavItem[] =>
  (
    [
      { to: "/dashboard/executive", label: "Business overview", icon: "measure", end: true },
      { to: "/dashboard/executive/reports", label: "Reports", icon: "file" },
      { to: "/dashboard/executive/cash", label: "Cash plan", icon: "reconcile" },
      { to: "/dashboard/executive/decisions", label: "My decisions", icon: "decision" },
    ] as NavItem[]
  ).map((item) => ({ ...item, to: close ? `${item.to}?close=${encodeURIComponent(close)}` : item.to }));

export default function WorkspaceLayout() {
  const search = useRef<HTMLInputElement>(null);
  const navigate = useNavigate();
  const location = useLocation();
  const signedIn = useSignedIn();
  // Unknown until the server says; nothing of the workspace shows before then, so an executive never sees it.
  const [executive, setExecutive] = useState<boolean>();
  const [examples, setExamples] = useState(false);
  // A hosted session still loading is not yet signed out.
  const session = authKitClientId ? useSession() : undefined;
  const sessionLoading = session?.isLoading ?? false;
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
  // Signed out, the workspace shows nothing of itself, not even while the session is checked: the visitor signs in and
  // comes back to this page, at the same place on it.
  if (authKitClientId && sessionLoading)
    return (
      <main id="main" className="app-main">
        <p role="status">Checking your session…</p>
      </main>
    );
  // A session that could not be checked is not a sign-out: the workspace stays closed, and the visitor can try again.
  if (session?.unavailable)
    return (
      <main id="main" className="app-main">
        <section className="gate" role="alert">
          <h1>Your session couldn’t be checked</h1>
          <p>Krith couldn’t reach sign-in just now. Nothing in your workspace has changed.</p>
          <button type="button" className="primary" onClick={() => window.location.reload()}>
            Try again
          </button>
        </section>
      </main>
    );
  if (authKitClientId && !signedIn) return <Navigate to={`/login?return=${encodeURIComponent(location.pathname + location.search + location.hash)}`} replace />;
  const items = executive ? executiveNav(new URLSearchParams(location.search).get("close")) : known ? (examples ? [...firmNav, exampleNav] : firmNav) : [];
  return (
    <div className="app-shell">
      <a className="skip" href="#main">
        Skip to content
      </a>
      <header className="topbar">
        <Link className="topbar-brand" to="/dashboard">
          <span className="logo" aria-hidden="true">c</span>
          <span>
            <strong>Close</strong>
            <small>Preparation workspace</small>
          </span>
        </Link>
        <nav aria-label="Workspace">
          {items.map((item) => (
            <NavLink className="nav-item" end={item.end} to={item.to} key={item.to}>
              <Icon name={item.icon} />
              {item.label}
              {item.to === "/dashboard/attention" && signedIn && <AttentionBadge />}
            </NavLink>
          ))}
        </nav>
        <div className="topbar-tools">
          {known && !executive && (
            <form
              role="search"
              onSubmit={(event) => {
                event.preventDefault();
                const query = String(new FormData(event.currentTarget).get("q") ?? "").trim();
                navigate(`/dashboard/closes${query ? `?q=${encodeURIComponent(query)}` : ""}`);
              }}
            >
              <label className="topbar-search">
                <Icon name="search" />
                <input ref={search} name="q" aria-label="Search clients" placeholder="Search clients" maxLength={200} autoComplete="off" />
                <kbd aria-hidden="true">/</kbd>
              </label>
            </form>
          )}
          {known && !executive && signedIn && (
            <button type="button" className="button primary" onClick={openAssistant}>
              <Icon name="agent" /> Ask the agent
            </button>
          )}
          <AccountMenu />
        </div>
      </header>
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
