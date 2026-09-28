import { AuthKitProvider, useAuth } from "@workos-inc/authkit-react";
import { useEffect, useLayoutEffect, useState, type ReactNode } from "react";

// Staff sign in to the WorkOS organization that holds platform staff; without WorkOS (local
// development only) the server's workspace token stands in, kept in this tab's memory. With
// VITE_AUTH_SESSION=server the backend holds the staff session in its own cookie and requests
// carry no token.
const clientId = import.meta.env.VITE_WORKOS_CLIENT_ID as string | undefined;
const staffOrganization = import.meta.env.VITE_PLATFORM_ORGANIZATION_ID as string | undefined;
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
  return (
    <AuthKitProvider clientId={clientId} apiHostname={import.meta.env.VITE_WORKOS_API_HOSTNAME || undefined} redirectUri={`${window.location.origin}/admin/`}>
      <StaffSession>{children}</StaffSession>
    </AuthKitProvider>
  );
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
      <p>This console is for platform staff. Firm members use the workspace instead.</p>
      <button className="primary" onClick={onSignIn}>Sign in as platform staff</button>
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

function StaffSession({ children }: { children: ReactNode }) {
  const auth = useAuth();
  useLayoutEffect(() => {
    token = auth.getAccessToken;
    signOutStaff = () => auth.signOut({ returnTo: `${window.location.origin}/admin/` });
  }, [auth]);
  if (auth.isLoading) return <p role="status">Checking your session…</p>;
  if (!auth.user) return <SignIn onSignIn={() => auth.signIn({ organizationId: staffOrganization })} />;
  if (!staffOrganization) {
    return <Gate><h1>Staff sign-in is not configured</h1><p>The admin panel needs VITE_PLATFORM_ORGANIZATION_ID at build time.</p></Gate>;
  }
  if (auth.organizationId !== staffOrganization) {
    return (
      <Gate>
        <h1>You are signed in to a firm</h1>
        <p>Switch to the platform staff organization to use this console. Your firm session stays available in the workspace.</p>
        <button className="primary" onClick={() => auth.switchToOrganization({ organizationId: staffOrganization })}>Switch to platform staff</button>
      </Gate>
    );
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
