import { AccountAccess, authKitClientId } from "./Auth";
import { Link } from "react-router";

export function Login() {
  return (
    <main id="main" className="public-page compact-page">
      <p className="eyebrow">YOUR WORKSPACE</p>
      <h1>
        A clearer close
        <br />
        <em>starts here.</em>
      </h1>
      <section>
        {authKitClientId ? (
          <AccountAccess />
        ) : (
          <p>AuthKit is not configured in this environment yet.</p>
        )}
        <p>
          Your credentials are handled by WorkOS AuthKit. Access to saved client
          work requires membership in the configured firm.
        </p>
        <Link className="button primary" to="/dashboard">
          Open preparation workspace →
        </Link>
        <p className="source-label">
          Reference examples are public. Saved client work requires workspace
          access.
        </p>
      </section>
      <p>
        Interested in Close for your firm?{" "}
        <Link to="/contact">Reach out to us.</Link>
      </p>
    </main>
  );
}
