import { Link } from "react-router";

export function NotFound() {
  return (
    <main id="main" className="k-col k-page">
      <div className="k-page-head">
        <p className="k-label">Not found</p>
        <h1>We couldn’t find that page.</h1>
        <p className="k-lede">
          The address may be old or mistyped. <Link to="/">Return home</Link>
        </p>
      </div>
    </main>
  );
}
