import type { ReactNode } from "react";
import { Link } from "react-router";
import { AccountAccess, authKitClientId, useSession } from "../auth/Auth";
import { unlock, useUnlocked } from "../closes/workspace";
import { Avatar } from "@close/ui/components/Avatar";

// Every workspace page waits for access; there is nothing to try without signing in.
export function WorkspaceGate({ children }: { children: ReactNode }) {
  return authKitClientId ? <SignedIn>{children}</SignedIn> : <Unlocked>{children}</Unlocked>;
}

function Unlocked({ children }: { children: ReactNode }) {
  if (useUnlocked()) return children;
  return (
    <section className="gate">
      <h1>Unlock your workspace</h1>
      <p>
        Local development uses the private token configured on the server. It
        stays in this tab's memory; refreshing locks the workspace again.
      </p>
      <form
        onSubmit={(event) => {
          event.preventDefault();
          unlock(String(new FormData(event.currentTarget).get("token") ?? ""));
        }}
      >
        <label>
          Private workspace token
          <input name="token" type="password" autoComplete="off" required minLength={32} />
        </label>
        <button className="primary">Unlock workspace</button>
      </form>
    </section>
  );
}

function SignedIn({ children }: { children: ReactNode }) {
  const { user, isLoading } = useSession();
  if (isLoading) return <p role="status">Checking your session…</p>;
  if (user) return children;
  return (
    <section className="gate">
      <h1>Sign in to your firm</h1>
      <AccountAccess />
    </section>
  );
}

// The account menu in the top bar: the person's initial, and under it their session, their profile and the way out.
export function AccountMenu() {
  const unlocked = useUnlocked();
  const session = authKitClientId ? useSession() : undefined;
  const label = session?.user?.email ?? (unlocked ? "Local development" : "Locked");
  return (
    <details className="topbar-account">
      <summary>
        <span className="visually-hidden">Account: </span>
        <Avatar label={label} />
        <span className="topbar-account-name">{session?.user ? session.user.email.split("@")[0] : label}</span>
      </summary>
      <div>
        {(session?.user || unlocked) && (
          <Link to="/dashboard/settings" onClick={(event) => event.currentTarget.closest("details")?.removeAttribute("open")}>
            Your profile
          </Link>
        )}
        {authKitClientId ? (
          <AccountAccess />
        ) : (
          <>
            <p>{unlocked ? "Local development access" : "Workspace locked"}</p>
            {unlocked && (
              <button type="button" onClick={() => unlock("")}>
                Lock workspace
              </button>
            )}
          </>
        )}
      </div>
    </details>
  );
}
