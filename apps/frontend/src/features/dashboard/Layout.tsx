import { Link, NavLink, Outlet } from "react-router";

export default function WorkspaceLayout() {
  return (
    <>
      <a className="skip" href="#main">
        Skip to content
      </a>
      <header>
        <Link className="brand" to="/">
          close<span> / preparation workspace</span>
        </Link>
        <span className="mode">
          Preparation workspace · sandbox posting only
        </span>
      </header>
      <div className="workspace">
        <nav aria-label="Workspace">
          <NavLink end to="/dashboard">
            Work
          </NavLink>
          <NavLink to="/dashboard/closes">Client closes</NavLink>
          <NavLink to="/dashboard/reconciliations/new">
            Reconcile a statement
          </NavLink>
          <NavLink to="/dashboard/prepaids/new">Prepare a prepaid</NavLink>
          <NavLink to="/dashboard/close">Example close</NavLink>
          <NavLink to="/dashboard/review">Example review</NavLink>
          <p>
            Prepared work.
            <br />
            Clear decisions.
            <br />
            Your control.
          </p>
        </nav>
        <main id="main">
          <Outlet />
          <footer>
            Production posting is not enabled. Supported-scope sign-off is
            recorded separately. Sandbox posting requires explicit approval and
            verified readback.
          </footer>
        </main>
      </div>
    </>
  );
}
