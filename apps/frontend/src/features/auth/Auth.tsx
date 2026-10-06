import { AuthKitProvider, useAuth } from "@workos-inc/authkit-react";
import { createContext, useContext, useEffect, useLayoutEffect, useState, type ReactNode } from "react";
import { apiBaseUrl } from "../../lib/api";

export const authKitClientId = import.meta.env.VITE_WORKOS_CLIENT_ID as
  string | undefined;
// AUTH_SESSION=server keeps WorkOS's tokens on our API behind an HttpOnly cookie. Unset or "client" uses WorkOS's
// browser session, which needs a custom WorkOS authentication domain (VITE_WORKOS_API_HOSTNAME) in production.
export const serverSession = import.meta.env.VITE_AUTH_SESSION === "server";

type Session = { user: { email: string } | null; isLoading: boolean; unavailable?: boolean; signIn: () => Promise<void> | void; signOut: () => Promise<void> | void };

let accessToken: (() => Promise<string>) | undefined;
export async function authenticationHeaders(): Promise<Record<string, string>> {
  // The server session's cookie goes with every same-origin request on its own.
  if (!authKitClientId || serverSession) return {};
  if (!accessToken) throw new Error("Sign in before opening client work.");
  return { Authorization: `Bearer ${await accessToken()}` };
}
function ClientSession({ children }: { children: ReactNode }) {
  const { getAccessToken } = useAuth();
  useLayoutEffect(() => {
    accessToken = getAccessToken;
    return () => {
      accessToken = undefined;
    };
  }, [getAccessToken]);
  return children;
}

// The server's sign-in, straight to Google or Microsoft when a provider is named (docs/sign-in.md).
export function serverSignInUrl(provider?: "google" | "microsoft", back = "/dashboard") {
  return `${apiBaseUrl}/v1/auth/login?${new URLSearchParams({ ...(provider ? { provider } : {}), return: back })}`;
}
function startServerSignIn() {
  window.location.assign(serverSignInUrl(undefined, window.location.pathname + window.location.search));
}
async function endServerSession() {
  const response = await fetch(`${apiBaseUrl}/v1/auth/logout`, { method: "POST" });
  if (!response.ok) throw new Error("Sign-out could not finish.");
  window.location.assign((await response.json()).logout_url);
}
const signedOut: Session = { user: null, isLoading: false, signIn: startServerSignIn, signOut: endServerSession };
const ServerSessionContext = createContext<Session>({ ...signedOut, isLoading: true });
function ServerSession({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<Session>({ ...signedOut, isLoading: true });
  useEffect(() => {
    let live = true;
    // Only a 401 means signed out; any other failure is shown as unavailable, not as a prompt to sign in.
    fetch(`${apiBaseUrl}/v1/auth/session`)
      .then(async (response) => (response.ok ? { user: (await response.json()).user } : response.status === 401 ? { user: null } : { user: null, unavailable: true }))
      .catch(() => ({ user: null, unavailable: true }))
      .then((state) => live && setSession({ ...signedOut, ...state }));
    return () => {
      live = false;
    };
  }, []);
  return <ServerSessionContext.Provider value={session}>{children}</ServerSessionContext.Provider>;
}

function useClientSession(): Session {
  const { user, isLoading, signIn, signOut } = useAuth();
  return {
    user: user ? { email: user.email } : null,
    isLoading,
    signIn: () => {
      sessionStorage.setItem("returnTo", window.location.pathname + window.location.search);
      return signIn();
    },
    signOut: () => signOut({ returnTo: window.location.origin }),
  };
}
// Chosen once, so every render calls the same hooks.
export const useSession: () => Session = serverSession ? () => useContext(ServerSessionContext) : useClientSession;

export function Authentication({ children }: { children: ReactNode }) {
  if (!authKitClientId) return children;
  if (serverSession) return <ServerSession>{children}</ServerSession>;
  return (
    <AuthKitProvider
      clientId={authKitClientId}
      redirectUri={`${window.location.origin}/login`}
      apiHostname={import.meta.env.VITE_WORKOS_API_HOSTNAME || undefined}
      onRedirectCallback={() => {
        const next = sessionStorage.getItem("returnTo") ?? "";
        sessionStorage.removeItem("returnTo");
        window.location.replace(next.startsWith("/dashboard") ? next : "/dashboard");
      }}
    >
      <ClientSession>{children}</ClientSession>
    </AuthKitProvider>
  );
}
export function AccountAccess() {
  const { user, isLoading, unavailable, signIn, signOut } = useSession();
  const [error, setError] = useState("");
  if (isLoading) return <p role="status">Checking your session…</p>;
  if (unavailable) return <p role="alert">Your session could not be checked. Reload the page to try again.</p>;
  return (
    <>
      <p>
        {user
          ? `Signed in as ${user.email}`
          : "Sign in to open your firm’s workspace."}
      </p>
      <button
        onClick={() =>
          void Promise.resolve(user ? signOut() : signIn()).catch(() =>
            setError(user ? "Sign-out could not finish. Please try again." : "Sign-in could not start. Please try again."),
          )
        }
      >
        {user ? "Sign out" : "Sign in"}
      </button>
      {error && <p role="alert">{error}</p>}
    </>
  );
}
