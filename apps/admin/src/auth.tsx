import { AuthKitProvider, useAuth } from "@workos-inc/authkit-react";
import { useLayoutEffect, useState, type ReactNode } from "react";

// Staff sign in to the WorkOS organization that holds platform staff; without WorkOS (local
// development only) the server's workspace token stands in, kept in this tab's memory.
const clientId = import.meta.env.VITE_WORKOS_CLIENT_ID as string | undefined;
const staffOrganization = import.meta.env.VITE_PLATFORM_ORGANIZATION_ID as string | undefined;

let token: () => Promise<string> = async () => "";
let signOutStaff: () => void = () => {};

export async function authorization(): Promise<Record<string, string>> {
  const value = await token();
  return value ? { Authorization: `Bearer ${value}` } : {};
}

export function signOut() {
  signOutStaff();
}

export function StaffAccess({ children }: { children: ReactNode }) {
  if (!clientId) return <DevelopmentUnlock>{children}</DevelopmentUnlock>;
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

function StaffSession({ children }: { children: ReactNode }) {
  const auth = useAuth();
  useLayoutEffect(() => {
    token = auth.getAccessToken;
    signOutStaff = () => auth.signOut({ returnTo: `${window.location.origin}/admin/` });
  }, [auth]);
  if (auth.isLoading) return <p role="status">Checking your session…</p>;
  if (!auth.user) {
    return (
      <Gate>
        <h1>Close · Staff</h1>
        <p>This console is for platform staff. Firm members use the workspace instead.</p>
        <button className="primary" onClick={() => auth.signIn({ organizationId: staffOrganization })}>Sign in as platform staff</button>
      </Gate>
    );
  }
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
