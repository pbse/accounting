import { AuthKitProvider, useAuth } from "@workos-inc/authkit-react";
import { createContext, useContext, useLayoutEffect, useState, type ReactNode } from "react";
import { apiBaseUrl } from "../../lib/api";
import { useQuery } from "@tanstack/react-query";

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

// The server's sign-in, straight to Google when it is named (docs/sign-in.md).
export function serverSignInUrl(provider?: "google", back = "/dashboard") {
  return `${apiBaseUrl}/v1/auth/login?${new URLSearchParams({ ...(provider ? { provider } : {}), return: back })}`;
}
// Signing in from anywhere goes through the login page, with its Google button, and back here after.
function startServerSignIn() {
  window.location.assign(`/login?${new URLSearchParams({ return: window.location.pathname + window.location.search })}`);
}
async function endServerSession() {
  const response = await fetch(`${apiBaseUrl}/v1/auth/logout`, { method: "POST" });
  if (!response.ok) throw new Error("Sign-out could not finish.");
  window.location.assign((await response.json()).logout_url);
}
const signedOut: Session = { user: null, isLoading: false, signIn: startServerSignIn, signOut: endServerSession };
const ServerSessionContext = createContext<Session>({ ...signedOut, isLoading: true });
// Who is signed in, as the server session says: a query, so a page rendered on the server arrives with it.
export type SessionState = { user: { email: string } | null; unavailable?: boolean };
export const sessionKey = ["session"] as const;
// Only a 401 means signed out; any other failure is shown as unavailable, not as a prompt to sign in.
export const readSession = (): Promise<SessionState> =>
  fetch(`${apiBaseUrl}/v1/auth/session`)
    .then(async (response) => (response.ok ? { user: (await response.json()).user } : response.status === 401 ? { user: null } : { user: null, unavailable: true }))
    .catch(() => ({ user: null, unavailable: true }));
// In the browser the session is asked for as soon as this script runs, not after the app first renders, unless the
// page came rendered from the server with it.
let firstSession =
  typeof window !== "undefined" && serverSession && window.location.pathname !== "/request" && !document.getElementById("krith-query") ? readSession() : undefined;
function ServerSession({ children }: { children: ReactNode }) {
  const read = useQuery({
    queryKey: sessionKey,
    queryFn: () => {
      const asked = firstSession ?? readSession();
      firstSession = undefined;
      return asked;
    },
    staleTime: Infinity,
  });
  const session: Session = read.data ? { ...signedOut, ...read.data } : { ...signedOut, isLoading: true };
  return <ServerSessionContext.Provider value={session}>{children}</ServerSessionContext.Provider>;
}

function useClientSession(): Session {
  const { user, isLoading, signIn, signOut } = useAuth();
  return {
    user: user ? { email: user.email } : null,
    isLoading,
    signIn: () => {
      // From the login page, return to the workspace page that sent the visitor there; elsewhere, to this page.
      const asked = new URLSearchParams(window.location.search).get("return") ?? "";
      const back = window.location.pathname === "/login" && asked.startsWith("/dashboard") ? asked : window.location.pathname + window.location.search;
      sessionStorage.setItem("returnTo", back);
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
