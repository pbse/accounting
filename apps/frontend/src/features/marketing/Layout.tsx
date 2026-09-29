import { useEffect } from "react";
import { Link, NavLink, Outlet, useLocation } from "react-router";
import { legalPages } from "./Legal";

export function MarketingLayout() {
  // The layout stays mounted between public pages, so a new page starts at its top.
  const { pathname } = useLocation();
  useEffect(() => window.scrollTo(0, 0), [pathname]);
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
        <nav aria-label="Legal and data" className="public-legal">
          {legalPages.map((page) => (
            <Link key={page.to} to={page.to}>
              {page.label}
            </Link>
          ))}
        </nav>
      </footer>
    </div>
  );
}
