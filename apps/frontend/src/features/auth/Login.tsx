import { AccountAccess, authKitClientId } from "./Auth";
import { Link } from "react-router";

export function Login() {
  return (
    <main id="main" className="k-col k-page">
      <div className="k-page-head">
        <p className="k-label">Log in</p>
        <h1>Your workspace.</h1>
        <p className="k-lede">Sign in to open your firm’s work. Saved client work needs membership in your firm.</p>
      </div>
      <div className="k-signin">
        {authKitClientId ? <AccountAccess /> : <p>Sign-in is not configured in this environment yet.</p>}
        <Link to="/dashboard">Open the workspace →</Link>
        <p className="k-fine">
          New to Krith? <Link to="/contact">Talk to us</Link>
        </p>
      </div>
    </main>
  );
}
