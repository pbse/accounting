import { useEffect, useState, type ReactNode } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { apiBaseUrl } from "../../lib/api";
import { AccountAccess, authKitClientId, serverSession, serverSignInUrl, useSession } from "./Auth";

// Why the server turned a sign-in away (docs/sign-in.md), in words.
const refusals: Record<string, string> = {
  company: "That was a personal address. Personal accounts such as Gmail, Outlook.com or iCloud can’t sign in; use your firm’s Google Workspace account.",
  hosting: "A workspace is set up by sign-in only for a firm whose mail runs on Google Workspace or Microsoft 365, and your firm’s doesn’t. Talk to us and we’ll set your firm up.",
  ask: "Your firm’s workspace is set up under another email domain. Ask its administrator to invite you.",
  invited: "Your firm has invited you. Open the invitation in your email to join, then sign in here.",
  unverified: "Your email address isn’t verified yet. Verify it with Google, then sign in again.",
  removed: "Your firm’s administrator has removed your access. Ask them to restore it.",
  suspended: "Your firm’s workspace is paused. Talk to us to restore it.",
  staff: "The staff console is only for Krith staff, signed in with their Krith Google account. Firm members use the workspace.",
  unavailable: "Sign-in couldn’t finish just now. Please try again in a moment.",
  expired: "That sign-in expired. Continue with Google to start again.",
};
// WorkOS sends a sign-in it began itself (an accepted invitation, or a tab left from before) to this page with a code
// that only a sign-in started here can use, so the page asks for one instead of sitting silent.
const unfinished = "Your sign-in isn’t finished yet. Continue with Google to finish it.";

export function Login() {
  const configured = Boolean(authKitClientId);
  const [params] = useSearchParams();
  return (
    <main id="main" className="k-col k-page">
      <div className="k-page-head">
        <h1>Log in.</h1>
        <p className="k-lede">With your firm’s Google Workspace account.</p>
      </div>
      <div className="k-signin">
        {!configured ? (
          <>
            <p>Sign-in is not configured in this environment.</p>
            <Link to="/dashboard">Open the local workspace →</Link>
          </>
        ) : serverSession && params.get("choose") ? (
          <FirmChoice />
        ) : (
          <SignedInGoesToWork>{serverSession ? <ProviderSignIn /> : <AccountAccess />}</SignedInGoesToWork>
        )}
        <p className="k-fine">
          New to Krith, or on Microsoft 365? <Link to="/contact">Talk to us</Link>.
        </p>
      </div>
    </main>
  );
}

// The workspace page that sent the visitor to sign in, if any; otherwise the workspace's front page.
function workspaceReturn(params: URLSearchParams) {
  const asked = params.get("return") ?? "";
  return asked.startsWith("/dashboard") ? asked : "/dashboard";
}

// Someone already signed in goes straight to the workspace page they asked for, whichever session the environment uses.
function SignedInGoesToWork({ children }: { children: ReactNode }) {
  const { user } = useSession();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const back = workspaceReturn(params);
  useEffect(() => {
    if (user) navigate(back, { replace: true });
  }, [user, navigate, back]);
  return user ? <p role="status">Opening your workspace…</p> : children;
}

// The server's sign-in: straight to Google, and back to where sign-in began. Firms on Microsoft 365 cannot sign in
// yet (docs/sign-in.md); the page's last line asks them to talk to us.
function ProviderSignIn() {
  const { isLoading, unavailable } = useSession();
  const [params, setParams] = useSearchParams();
  const [leftover] = useState(() => params.has("code"));
  useEffect(() => {
    // The code is no use here, so it leaves the address; the page still says what to do.
    if (!params.has("code") && !params.has("state")) return;
    const kept = new URLSearchParams(params);
    kept.delete("code");
    kept.delete("state");
    setParams(kept, { replace: true });
  }, [params, setParams]);
  const refusal = refusals[params.get("error") ?? ""] ?? (leftover ? unfinished : undefined);
  const back = workspaceReturn(params);
  if (isLoading) return <p role="status">Checking your session…</p>;
  return (
    <>
      {refusal && <p className="k-alert" role="alert">{refusal}</p>}
      {unavailable && <p className="k-alert" role="alert">{refusals.unavailable}</p>}
      <div className="k-providers">
        <a className="k-provider" href={serverSignInUrl("google", back)}>
          <GoogleMark /> Continue with Google
        </a>
      </div>
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

// Someone who works in several firms picks the one to open; their answer finishes the sign-in.
function FirmChoice() {
  const [choice, setChoice] = useState<{ firms: { id: string; name: string }[]; email: string } | "expired">();
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    fetch(`${apiBaseUrl}/v1/auth/choices`)
      .then(async (response) => setChoice(response.ok ? await response.json() : "expired"))
      .catch(() => setChoice("expired"));
  }, []);
  async function choose(organization: string) {
    setBusy(true);
    try {
      const response = await fetch(`${apiBaseUrl}/v1/auth/choose`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ organization }) });
      if (!response.ok) throw new Error();
      window.location.assign((await response.json()).location);
    } catch {
      setChoice("expired");
      setBusy(false);
    }
  }
  if (!choice) return <p role="status">Loading your firms…</p>;
  if (choice === "expired")
    return (
      <>
        <p className="k-alert" role="alert">This choice expired. Sign in again.</p>
        <Link to="/login">Back to sign-in →</Link>
      </>
    );
  return (
    <>
      <p>You work in more than one firm{choice.email ? ` as ${choice.email}` : ""}. Which would you like to open?</p>
      <div className="k-providers">
        {choice.firms.map((firm) => (
          <button key={firm.id} type="button" className="k-provider" disabled={busy} onClick={() => void choose(firm.id)}>
            {firm.name}
          </button>
        ))}
      </div>
    </>
  );
}
