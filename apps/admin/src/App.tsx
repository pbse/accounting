import { useState } from "react";
import { BrowserRouter, Link, NavLink, Outlet, Route, Routes, useOutletContext } from "react-router";
import { signOut } from "./auth";
import { GrantList, GrantsProvider } from "./components/Grants";
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
  return (
    <GrantsProvider enabled={me.data?.permissions.includes("platform:content") ?? false}>
    <div className="app-shell">
      <a className="skip" href="#main">Skip to content</a>
      <header className="topbar">
        <Link className="topbar-brand" to="/">
          <span className="logo" aria-hidden="true">c</span>
          <span>
            <strong>Close</strong>
            <small>Platform staff</small>
          </span>
        </Link>
        <nav aria-label="Staff console">
          <NavLink className="nav-item" end to="/"><Icon name="work" />Overview</NavLink>
          <NavLink className="nav-item" to="/firms"><Icon name="firm" />Firms</NavLink>
          <NavLink className="nav-item" to="/people"><Icon name="clients" />People</NavLink>
          <NavLink className="nav-item" to="/audit"><Icon name="shield" />Staff audit</NavLink>
          <NavLink className="nav-item" to="/contact-notes"><Icon name="file" />Contact notes</NavLink>
          <NavLink className="nav-item" to="/workflows"><Icon name="book" />Catalog</NavLink>
          <NavLink className="nav-item" end to="/runs"><Icon name="run" />Runs</NavLink>
          {me.data?.permissions.includes("platform:plans") && <NavLink className="nav-item" to="/plans"><Icon name="file" />Plans</NavLink>}
          <NavLink className="nav-item" to="/jobs"><Icon name="blocked" />Stuck work</NavLink>
          <NavLink className="nav-item" to="/connections"><Icon name="link" />Connections</NavLink>
        </nav>
        <div className="topbar-tools">
          <details className="topbar-account">
            <summary aria-label="Account">
              <Avatar label={me.data?.subject ?? "?"} />
              <span>{me.data?.subject ?? "Account"}</span>
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
        </div>
      </header>
      <main id="main" className="app-main">
        <div className="page">
          <Outlet context={me.data?.permissions ?? []} />
        </div>
      </main>
    </div>
    </GrantsProvider>
  );
}
