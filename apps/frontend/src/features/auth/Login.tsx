import { useEffect } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { AccountAccess, authKitClientId, serverSession, serverSignInUrl, useSession } from "./Auth";

// Why the server turned a sign-in away (docs/sign-in.md), in words.
const refusals: Record<string, string> = {
  company: "That was a personal address. Personal accounts such as Gmail, Outlook.com or iCloud can’t sign in; use the account your firm gives you.",
  hosting: "A workspace is set up by sign-in only for a firm whose mail runs on Google Workspace or Microsoft 365, and your firm’s doesn’t. Talk to us and we’ll set your firm up.",
  unverified: "Your email address isn’t verified yet. Verify it with Google or Microsoft, then sign in again.",
  removed: "Your firm’s administrator has removed your access. Ask them to restore it.",
  suspended: "Your firm’s workspace is paused. Talk to us to restore it.",
  unavailable: "Sign-in couldn’t finish just now. Please try again in a moment.",
};

export function Login() {
  return (
    <main id="main" className="k-col k-page">
      <div className="k-page-head">
        <p className="k-label">Log in</p>
        <h1>Your workspace.</h1>
        <p className="k-lede">Sign in with your firm’s Google Workspace or Microsoft 365 account.</p>
      </div>
      <div className="k-signin">
        {!authKitClientId ? (
          <>
            <p>Sign-in is not configured in this environment.</p>
            <Link to="/dashboard">Open the local workspace →</Link>
          </>
        ) : serverSession ? (
          <ProviderSignIn />
        ) : (
          <AccountAccess />
        )}
        <p className="k-fine">
          New to Krith? <Link to="/contact">Talk to us</Link>
        </p>
      </div>
    </main>
  );
}

// The server's sign-in: one button per provider, each going straight to it. A signed-in visitor goes to the workspace.
function ProviderSignIn() {
  const { user, isLoading, unavailable } = useSession();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const refusal = refusals[params.get("error") ?? ""];
  useEffect(() => {
    if (user) navigate("/dashboard", { replace: true });
  }, [user, navigate]);
  if (isLoading || user) return <p role="status">Checking your session…</p>;
  return (
    <>
      {refusal && <p className="k-alert" role="alert">{refusal}</p>}
      {unavailable && <p className="k-alert" role="alert">{refusals.unavailable}</p>}
      <div className="k-providers">
        <a className="k-provider" href={serverSignInUrl("google")}>
          <GoogleMark /> Continue with Google
        </a>
        <a className="k-provider" href={serverSignInUrl("microsoft")}>
          <MicrosoftMark /> Continue with Microsoft
        </a>
      </div>
      <p className="k-fine">
        The first person from a firm sets up its workspace. Colleagues who sign in later with the same company domain
        join it, and the firm’s administrator decides what each can open.
      </p>
    </>
  );
}

function GoogleMark() {
  return (
    <svg aria-hidden="true" width="18" height="18" viewBox="0 0 48 48">
      <path fill="#EA4335" d="M24 9.5c3.54 0 6.71 1.22 9.21 3.6l6.85-6.85C35.9 2.38 30.47 0 24 0 14.62 0 6.51 5.38 2.56 13.22l7.98 6.19C12.43 13.72 17.74 9.5 24 9.5z" />
      <path fill="#4285F4" d="M46.98 24.55c0-1.57-.15-3.09-.38-4.55H24v9.02h12.94c-.58 2.96-2.26 5.48-4.78 7.18l7.73 6c4.51-4.18 7.09-10.36 7.09-17.65z" />
      <path fill="#FBBC05" d="M10.53 28.59c-.48-1.45-.76-2.99-.76-4.59s.27-3.14.76-4.59l-7.98-6.19C.92 16.46 0 20.12 0 24c0 3.88.92 7.54 2.56 10.78l7.97-6.19z" />
      <path fill="#34A853" d="M24 48c6.48 0 11.93-2.13 15.89-5.81l-7.73-6c-2.15 1.45-4.92 2.3-8.16 2.3-6.26 0-11.57-4.22-13.47-9.91l-7.98 6.19C6.51 42.62 14.62 48 24 48z" />
    </svg>
  );
}

function MicrosoftMark() {
  return (
    <svg aria-hidden="true" width="18" height="18" viewBox="0 0 21 21">
      <rect x="1" y="1" width="9" height="9" fill="#F25022" />
      <rect x="11" y="1" width="9" height="9" fill="#7FBA00" />
      <rect x="1" y="11" width="9" height="9" fill="#00A4EF" />
      <rect x="11" y="11" width="9" height="9" fill="#FFB900" />
    </svg>
  );
}
