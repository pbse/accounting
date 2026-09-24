import { AuthKitProvider, useAuth } from "@workos-inc/authkit-react";
import { useLayoutEffect, useState, type ReactNode } from "react";

export const authKitClientId = import.meta.env.VITE_WORKOS_CLIENT_ID as
  string | undefined;
let accessToken: (() => Promise<string>) | undefined;
export async function authenticationHeaders(): Promise<Record<string, string>> {
  if (!authKitClientId) return {};
  if (!accessToken) throw new Error("Sign in before opening client work.");
  return { Authorization: `Bearer ${await accessToken()}` };
}
function Session({ children }: { children: ReactNode }) {
  const { getAccessToken } = useAuth();
  useLayoutEffect(() => {
    accessToken = getAccessToken;
    return () => {
      accessToken = undefined;
    };
  }, [getAccessToken]);
  return children;
}
export function Authentication({ children }: { children: ReactNode }) {
  if (!authKitClientId) return children;
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
      <Session>{children}</Session>
    </AuthKitProvider>
  );
}
export function AccountAccess() {
  const { user, isLoading, signIn, signOut } = useAuth();
  const [error, setError] = useState("");
  if (isLoading) return <p role="status">Checking your session…</p>;
  return (
    <>
      <p>
        {user
          ? `Signed in as ${user.email}`
          : "Sign in to open your firm’s workspace."}
      </p>
      <button
        onClick={() => {
          if (user) {
            signOut({ returnTo: window.location.origin });
            return;
          }
          sessionStorage.setItem("returnTo", window.location.pathname + window.location.search);
          void signIn().catch(() =>
            setError("Sign-in could not start. Please try again."),
          );
        }}
      >
        {user ? "Sign out" : "Sign in with AuthKit"}
      </button>
      {error && <p role="alert">{error}</p>}
    </>
  );
}
