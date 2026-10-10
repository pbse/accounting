import { useState } from "react";
import { BrowserRouter, Link, Outlet, Route, Routes, useOutletContext } from "react-router";
import { signOut } from "./auth";
import { GrantCount, GrantList, GrantsProvider } from "./components/Grants";
import { useResource } from "./api";
import { Overview } from "./pages/Overview";
import { Firms } from "./pages/Firms";
import { Firm } from "./pages/Firm";
import { Workflows } from "./pages/Workflows";
import { Runs } from "./pages/Runs";
import { Run } from "./pages/Run";
import { StartRun } from "./pages/StartRun";
import { Connections, Jobs } from "./pages/Operations";
import { People } from "./pages/People";
import { Audit } from "./pages/Audit";
import { ContactNotes } from "./pages/ContactNotes";
import { Plan, Plans } from "./pages/Plans";
import { PlanBuilder } from "./pages/PlanBuilder";
import { Avatar } from "@close/ui/components/Avatar";
import { Icon } from "@close/ui/components/Icon";
import { Sidebar, type SidebarItem } from "@close/ui/components/Sidebar";

type Me = { subject: string; permissions: string[]; mode: string };

// The signed-in staff member's permissions, for disabling what they cannot do. The API decides.
export const usePermissions = () => useOutletContext<string[]>();

export function App() {
  return (
    <BrowserRouter basename="/admin">
      <Routes>
        <Route element={<Layout />}>
          <Route index element={<Overview />} />
          <Route path="firms" element={<Firms />} />
          <Route path="firms/:id" element={<Firm />} />
          <Route path="people" element={<People />} />
          <Route path="audit" element={<Audit />} />
          <Route path="contact-notes" element={<ContactNotes />} />
          <Route path="workflows" element={<Workflows />} />
          <Route path="runs" element={<Runs />} />
          <Route path="runs/new" element={<StartRun />} />
          <Route path="runs/:id" element={<Run />} />
          <Route path="plans" element={<Plans />} />
          <Route path="plans/new" element={<PlanBuilder />} />
          <Route path="plans/:id" element={<Plan />} />
          <Route path="plans/:id/edit" element={<PlanBuilder />} />
          <Route path="jobs" element={<Jobs />} />
          <Route path="connections" element={<Connections />} />
          <Route path="*" element={<p>This page does not exist. <Link to="/">Back to the overview</Link></p>} />
        </Route>
      </Routes>
    </BrowserRouter>
  );
}

function Layout() {
  const me = useResource<Me>("/v1/admin/me");
  const [signOutError, setSignOutError] = useState("");
  const permissions = me.data?.permissions ?? [];
  const items: SidebarItem[] = [
    { to: "/", label: "Overview", icon: "home", end: true },
    { to: "/firms", label: "Firms", icon: "firm" },
    { to: "/people", label: "People", icon: "clients" },
    { to: "/audit", label: "Staff audit", icon: "shield" },
    { to: "/contact-notes", label: "Contact notes", icon: "file" },
    { label: "Workflows", icon: "run", pages: [{ to: "/workflows", label: "Catalog" }, { to: "/runs", label: "Runs", end: true }, ...(permissions.includes("platform:plans") ? [{ to: "/plans", label: "Plans" }] : [])] },
    { label: "Operations", icon: "blocked", pages: [{ to: "/jobs", label: "Stuck work" }, { to: "/connections", label: "Connections" }] },
  ];
  return (
    <GrantsProvider enabled={permissions.includes("platform:content")}>
    <div className="app-shell">
      <a className="skip" href="#main">Skip to content</a>
      <Sidebar
        brandTo="/"
        brandLabel="Krith staff console, overview"
        line="Platform staff"
        items={items}
        navLabel="Staff console"
        person={
          <details className="ks-me">
            <summary>
              <span className="visually-hidden">Account: </span>
              <Avatar label={me.data?.subject ?? "?"} />
              <span className="ks-me-name">
                <b>{me.data?.subject ?? "Account"}</b>
                <small>{me.data?.mode === "local-development" ? "Local development" : "Staff"}</small>
              </span>
              <GrantCount />
              <Icon name="select" />
            </summary>
            <div>
              <GrantList />
              {me.data && (
                <p>
                  Signed in as {me.data.subject}
                  {me.data.mode === "local-development" && " (local development)"}
                  <br />
                  {me.data.permissions.join(", ")}
                </p>
              )}
              {me.error && <p role="alert">{me.error}</p>}
              {signOutError && <p role="alert">{signOutError}</p>}
              <button onClick={() => signOut().catch((error: Error) => setSignOutError(error.message))}>Sign out</button>
            </div>
          </details>
        }
      />
      <main id="main" className="app-main">
        <div className="page">
          <Outlet context={permissions} />
        </div>
      </main>
    </div>
    </GrantsProvider>
  );
}
