import { useEffect, useState, type ReactNode } from "react";

// Staff are the people with a verified Google address at the staff domain (docs/admin-panel.md). Only the backend's
// own session knows that address, so with WorkOS the console needs VITE_AUTH_SESSION=server: the backend holds the
// staff session in its own cookie and requests carry no token. Without WorkOS (local development only) the server's
// workspace token stands in, kept in this tab's memory.
const clientId = import.meta.env.VITE_WORKOS_CLIENT_ID as string | undefined;
const serverSession = import.meta.env.VITE_AUTH_SESSION === "server";

let token: () => Promise<string> = async () => "";
let signOutStaff: () => Promise<void> | void = serverSession ? endServerSession : () => {};

export async function authorization(): Promise<Record<string, string>> {
  const value = await token();
  return value ? { Authorization: `Bearer ${value}` } : {};
}

export async function signOut() {
  await signOutStaff();
}

async function endServerSession() {
  const response = await fetch("/v1/auth/logout?staff=1", { method: "POST" });
  if (!response.ok) throw new Error("Sign-out could not finish. Try again.");
  window.location.assign((await response.json()).logout_url);
}

export function StaffAccess({ children }: { children: ReactNode }) {
  if (!clientId) return <DevelopmentUnlock>{children}</DevelopmentUnlock>;
  if (serverSession) return <ServerSession>{children}</ServerSession>;
  return <Gate><h1>The staff console needs server sessions</h1><p>Staff are recognised by their verified address, which only the server-held session carries. Build the console with VITE_AUTH_SESSION=server.</p></Gate>;
}

function Gate({ children }: { children: ReactNode }) {
  return (
    <div className="app-shell">
      <main id="main" className="app-main">
        <section className="gate">{children}</section>
      </main>
    </div>
  );
}

function SignIn({ onSignIn }: { onSignIn: () => void }) {
  return (
    <Gate>
      <h1>Close · Staff</h1>
      <p>This console is for Krith staff, with their Krith Google account. Firm members use the workspace instead.</p>
      <button className="primary" onClick={onSignIn}>Sign in with Google</button>
    </Gate>
  );
}

function ServerSession({ children }: { children: ReactNode }) {
  const [state, setState] = useState<"checking" | "signed-in" | "signed-out" | "unavailable">("checking");
  useEffect(() => {
    fetch("/v1/auth/session?staff=1")
      .then((response) => setState(response.ok ? "signed-in" : response.status === 401 ? "signed-out" : "unavailable"))
      .catch(() => setState("unavailable"));
  }, []);
  if (state === "checking") return <p role="status">Checking your session…</p>;
  if (state === "unavailable") return <Gate><h1>Your session could not be checked</h1><p>Reload the page to try again.</p></Gate>;
  if (state === "signed-out") {
    return <SignIn onSignIn={() => window.location.assign(`/v1/auth/login?staff=1&return=${encodeURIComponent(window.location.pathname + window.location.search)}`)} />;
  }
  return children;
}

function DevelopmentUnlock({ children }: { children: ReactNode }) {
  const [unlocked, setUnlocked] = useState(false);
  if (unlocked) return children;
  return (
    <Gate>
      <h1>Unlock the staff console</h1>
      <p>Local development uses the private workspace token configured on the server. It stays in this tab's memory.</p>
      <form
        onSubmit={(event) => {
          event.preventDefault();
          const value = String(new FormData(event.currentTarget).get("token") ?? "");
          token = async () => value;
          signOutStaff = () => {
            token = async () => "";
            setUnlocked(false);
          };
          setUnlocked(true);
        }}
      >
        <label>
          Private workspace token
          <input name="token" type="password" autoComplete="off" required minLength={32} />
        </label>
        <button className="primary">Unlock</button>
      </form>
    </Gate>
  );
}
