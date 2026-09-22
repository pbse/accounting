import { Link, NavLink, Outlet } from "react-router";

export function MarketingLayout() {
  return (
    <div className="marketing">
      <a className="skip" href="#main">
        Skip to content
      </a>
      <header className="public-header">
        <Link className="brand" to="/" aria-label="Close home">
          close<span> / for accounting firms</span>
        </Link>
        <nav aria-label="Main navigation" className="public-nav">
          <NavLink to="/contact">Contact us</NavLink>
          <NavLink className="button" to="/login">
            Log in <span aria-hidden="true">↗</span>
          </NavLink>
        </nav>
      </header>
      <Outlet />
      <footer className="public-footer">
        <Link className="brand" to="/">
          close
        </Link>
        <p>More prepared work. Your firm stays in control.</p>
        <Link to="/contact">Reach out to us</Link>
      </footer>
    </div>
  );
}
