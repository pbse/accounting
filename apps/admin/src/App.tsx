import { useState } from "react";
import { BrowserRouter, Link, NavLink, Outlet, Route, Routes, useOutletContext } from "react-router";
import { signOut } from "./auth";
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
import { Plan, Plans } from "./pages/Plans";

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
          <Route path="workflows" element={<Workflows />} />
          <Route path="runs" element={<Runs />} />
          <Route path="runs/new" element={<StartRun />} />
          <Route path="runs/:id" element={<Run />} />
          <Route path="plans" element={<Plans />} />
          <Route path="plans/:id" element={<Plan />} />
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
  return (
    <div className="app-shell">
      <a className="skip" href="#main">Skip to content</a>
      <aside className="sidebar">
        <Link className="sidebar-brand" to="/">
          <span className="logo" aria-hidden="true">c</span>
          <span>
            <strong>Close</strong>
            <small>Platform staff</small>
          </span>
        </Link>
        <nav aria-label="Staff console">
          <div className="nav-group">
            <div className="nav-label">Platform</div>
            <NavLink className="nav-item" end to="/">Overview</NavLink>
            <NavLink className="nav-item" to="/firms">Firms</NavLink>
            <NavLink className="nav-item" to="/people">People</NavLink>
          </div>
          <div className="nav-group">
            <div className="nav-label">Workflows</div>
            <NavLink className="nav-item" to="/workflows">Catalog</NavLink>
            <NavLink className="nav-item" end to="/runs">Runs</NavLink>
            {me.data?.permissions.includes("platform:plans") && <NavLink className="nav-item" to="/plans">Plans</NavLink>}
          </div>
          <div className="nav-group">
            <div className="nav-label">Operations</div>
            <NavLink className="nav-item" to="/jobs">Stuck work</NavLink>
            <NavLink className="nav-item" to="/connections">Connections</NavLink>
          </div>
        </nav>
        <div className="sidebar-footer">
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
      </aside>
      <main id="main" className="app-main">
        <div className="page">
          <Outlet context={me.data?.permissions ?? []} />
        </div>
      </main>
    </div>
  );
}
