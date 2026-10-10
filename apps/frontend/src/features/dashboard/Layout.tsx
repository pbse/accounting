import { Suspense, useEffect } from "react";
import { Navigate, Outlet, useLocation, useNavigate } from "react-router";
import { AccountMenu } from "./Access";
import { Assistant, useSignedIn } from "./Assistant";
import { closeOf, isHomePath, openSession } from "./assistantStore";
import { SideChats } from "./SideChats";
import { SideNotices } from "./SideNotices";
import { PageComposer } from "./PageComposer";
import { AttentionBadge } from "../closes/Attention";
import { useQuery } from "@tanstack/react-query";
import { workspaceQuery } from "../../data/queries";
import { authKitClientId, useSession } from "../auth/Auth";
import { Sidebar, SidebarFootLink, type SidebarItem } from "@close/ui/components/Sidebar";
import { useShellThemeColor } from "@close/ui/theme/useShellThemeColor";

// The firm's pages, in the sidebar's fixed order (docs/design-system.md, The shell). Settings and Help sit at the
// foot; Measure opens from Settings and the preparation forms from a close. Only Krith's own firm has the worked
// example (see examples.go), and sees it here.
const firmNav: SidebarItem[] = [
  { to: "/dashboard", label: "Home", icon: "home", end: true },
  { to: "/dashboard/clients", label: "Clients", icon: "clients" },
  { to: "/dashboard/closes", label: "Closes", icon: "check", end: true },
];
const exampleNav: SidebarItem = { to: "/dashboard/close", label: "Example", icon: "book" };

// A client's executive sees their business results and nothing of the firm's workspace; the server enforces the same.
// Links keep the client and month they chose.
const executiveNav = (close: string | null): SidebarItem[] =>
  (
    [
      { to: "/dashboard/executive", label: "Business overview", icon: "measure", end: true },
      { to: "/dashboard/executive/reports", label: "Reports", icon: "file" },
      { to: "/dashboard/executive/cash", label: "Cash plan", icon: "reconcile" },
      { to: "/dashboard/executive/decisions", label: "My decisions", icon: "decision" },
    ] as SidebarItem[]
  ).map((item) => ("to" in item ? { ...item, to: close ? `${item.to}?close=${encodeURIComponent(close)}` : item.to } : item));

export default function WorkspaceLayout() {
  const navigate = useNavigate();
  const location = useLocation();
  const signedIn = useSignedIn();
  // Unknown until the server says; nothing of the workspace shows before then, so an executive never sees it.
  const executiveRead = useQuery({ ...workspaceQuery<{ executive: boolean; closes?: { id: string; entity: string }[] }>("/executive"), enabled: signedIn });
  const executive = !signedIn ? undefined : executiveRead.data ? !!executiveRead.data.executive : executiveRead.isError ? false : undefined;
  const examples = !!useQuery({ ...workspaceQuery<{ available: boolean }>("/examples"), enabled: executive === false }).data?.available;
  // A hosted session still loading is not yet signed out.
  const session = authKitClientId ? useSession() : undefined;
  const sessionLoading = session?.isLoading ?? false;
  const known = !sessionLoading && (!signedIn || executive !== undefined);
  useEffect(() => {
    if (executive && !location.pathname.startsWith("/dashboard/executive")) navigate("/dashboard/executive", { replace: true });
  }, [executive, location.pathname, navigate]);
  useShellThemeColor();
  const closeId = executive ? "" : closeOf(location.pathname, location.search).closeId;
  const newChat = () => {
    openSession("new", closeId);
  };
  // ⇧⌘O starts a chat from anywhere; ⌘K stays the command palette's.
  useEffect(() => {
    if (!signedIn) return;
    const key = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.shiftKey && !event.altKey && event.key.toLowerCase() === "o") {
        event.preventDefault();
        newChat();
      }
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  });
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
  const chosen = new URLSearchParams(location.search).get("close");
  const items = executive ? executiveNav(chosen) : known ? (examples ? [...firmNav, exampleNav] : firmNav) : [];
  // An executive's line names the client whose results the links open: the one they chose, else the first.
  const executiveCloses = executiveRead.data?.closes ?? [];
  const client = (executiveCloses.find((c) => c.id === chosen) ?? executiveCloses[0])?.entity ?? "Your business";
  // Closes carries the count of new decisions, as Home's badge did; the page itself says how many are waiting.
  const withCount = items.map((item) => ("to" in item && item.to === "/dashboard/closes" && !executive && signedIn ? { ...item, count: <AttentionBadge /> } : item));
  // The firm is known by its sign-in domain until the firm's own name is kept; local development has none.
  const firm = session?.user ? session.user.email.split("@")[1] : signedIn ? "Local development" : "";
  return (
    <div className="app-shell">
      <a className="skip" href="#main">
        Skip to content
      </a>
      {known && (
        <Sidebar
          brandTo="/dashboard"
          brandLabel="Krith, home"
          line={executive ? client : firm}
          newChat={signedIn ? { onClick: newChat, hint: "⇧⌘O" } : undefined}
          items={withCount}
          navLabel="Workspace"
          foot={
            !executive && (
              <>
                <SidebarFootLink to="/dashboard/settings" icon="settings">
                  Settings
                </SidebarFootLink>
                <SidebarFootLink to="/contact" icon="help">
                  Help
                </SidebarFootLink>
              </>
            )
          }
          person={<AccountMenu />}
        >
          {signedIn && !executive && (
            <>
              <SideChats closeId={closeId} navigate={navigate} />
              <SideNotices />
            </>
          )}
          {signedIn && executive && <SideChats closeId="" navigate={navigate} />}
        </Sidebar>
      )}
      <main id="main" className="app-main">
        <div className="page">
          <Suspense fallback={<p role="status">Opening…</p>}>
            {known ? <Outlet /> : <p role="status">Opening…</p>}
          </Suspense>
        </div>
        {/* The composer at the foot of every page; Home has its own, the stage. */}
        {known && signedIn && !isHomePath(location.pathname) && <PageComposer closeId={closeId} executive={!!executive} />}
      </main>
      {executive !== undefined && <Assistant executive={executive} />}
    </div>
  );
}
