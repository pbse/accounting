import { useEffect, useRef, useState } from "react";
import { Link, NavLink, Outlet, useLocation } from "react-router";
import { drawLandscape } from "./landscape";

// The public site's frame (docs/marketing-site.md): a masthead that stays at the top, the page, and a footer that ends
// in a pen-and-ink landscape.
export function MarketingLayout() {
  const { pathname, hash, key } = useLocation();
  const [scrolled, setScrolled] = useState(false);
  const lastPath = useRef(pathname), lastKey = useRef("");
  useEffect(() => {
    const update = () => setScrolled(window.scrollY > 8);
    update();
    window.addEventListener("scroll", update, { passive: true });
    return () => window.removeEventListener("scroll", update);
  }, []);
  // The layout stays mounted between pages: a link to a section opens at that section, and any other link, the wordmark
  // on the home page included, opens its page at the top. On first load only a section is looked for.
  useEffect(() => {
    const first = lastKey.current === "", newPage = lastPath.current !== pathname;
    lastKey.current = key;
    lastPath.current = pathname;
    let id = hash.slice(1);
    try {
      id = decodeURIComponent(id);
    } catch {
      // A malformed escape is looked up as written.
    }
    const target = id ? document.getElementById(id) : null;
    if (target) target.scrollIntoView({ behavior: newPage || first ? "instant" : "smooth" });
    else if (!first) window.scrollTo({ top: 0, behavior: newPage ? "instant" : "smooth" });
  }, [pathname, hash, key]);
  return (
    <div className="k-site">
      <a className="k-skip" href="#main">
        Skip to content
      </a>
      <header className={scrolled ? "k-mast is-scrolled" : "k-mast"}>
        <div className="k-col k-bar">
          <Link className="k-logo" to="/" aria-label="Krith, home">
            krith
          </Link>
          <nav className="k-nav" aria-label="Main">
            <Link to="/#how">How it works</Link>
            <NavLink to="/security">Security</NavLink>
            <NavLink to="/about">About</NavLink>
          </nav>
          <div className="k-actions">
            <NavLink className="k-login" to="/login">
              Log in
            </NavLink>
            <Link className="k-pill" to="/contact">
              Talk to us <span aria-hidden="true">→</span>
            </Link>
          </div>
        </div>
      </header>
      <Outlet />
      <footer className="k-foot">
        <div className="k-col k-foot-top">
          <div>
            <Link className="k-logo" to="/" aria-label="Krith, home">
              krith
            </Link>
            <p className="k-tagline">The work, prepared.</p>
          </div>
          <nav aria-label="Krith">
            <h3>Krith</h3>
            <ul>
              <li>
                <Link to="/#how">How it works</Link>
              </li>
              <li>
                <Link to="/security">Security</Link>
              </li>
            </ul>
          </nav>
          <nav aria-label="Company">
            <h3>Company</h3>
            <ul>
              <li>
                <Link to="/about">About</Link>
              </li>
              <li>
                <Link to="/contact">Contact</Link>
              </li>
              <li>
                <Link to="/login">Log in</Link>
              </li>
            </ul>
          </nav>
        </div>
        <div className="k-col k-foot-base">
          <span>
            © {new Date().getFullYear()} Krith · <i>krith</i>, from the Sanskrit <i>kṛta</i>: done
          </span>
          <span>
            Made with{" "}
            <span className="k-heart" role="img" aria-label="love">
              ♥
            </span>{" "}
            in NYC
          </span>
          <nav aria-label="Legal">
            <Link to="/privacy">Privacy notice</Link>
          </nav>
        </div>
        <Landscape />
      </footer>
    </div>
  );
}

function Landscape() {
  const canvas = useRef<HTMLCanvasElement>(null);
  useEffect(() => {
    const element = canvas.current!;
    let width = 0, timer = 0;
    const draw = () => {
      const next = element.getBoundingClientRect().width;
      if (next === width) return;
      width = next;
      drawLandscape(element);
    };
    draw();
    const onResize = () => {
      clearTimeout(timer);
      timer = window.setTimeout(draw, 180);
    };
    window.addEventListener("resize", onResize);
    return () => {
      clearTimeout(timer);
      window.removeEventListener("resize", onResize);
    };
  }, []);
  return <canvas className="k-engraving" ref={canvas} aria-hidden="true" />;
}
