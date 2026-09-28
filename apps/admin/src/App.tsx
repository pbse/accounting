import { useState } from "react";
import { BrowserRouter, Link, NavLink, Outlet, Route, Routes } from "react-router";
import { signOut } from "./auth";
import { useResource } from "./api";
import { Overview } from "./pages/Overview";
import { Firms } from "./pages/Firms";
import { Firm } from "./pages/Firm";

type Me = { subject: string; permissions: string[]; mode: string };

export function App() {
  return (
    <BrowserRouter basename="/admin">
      <Routes>
        <Route element={<Layout />}>
          <Route index element={<Overview />} />
          <Route path="firms" element={<Firms />} />
          <Route path="firms/:id" element={<Firm />} />
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
          <Outlet />
        </div>
      </main>
    </div>
  );
}
