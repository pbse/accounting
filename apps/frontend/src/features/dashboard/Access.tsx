import type { ReactNode } from "react";
import { AccountAccess, authKitClientId, useSession } from "../auth/Auth";
import { unlock, useUnlocked } from "../closes/workspace";

// Client data routes wait for access; the calculators stay usable without it.
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

export function SidebarAccount() {
  const unlocked = useUnlocked();
  if (authKitClientId) return <AccountAccess />;
  return (
    <>
      <p>{unlocked ? "Local development access" : "Workspace locked"}</p>
      {unlocked && (
        <button type="button" onClick={() => unlock("")}>
          Lock workspace
        </button>
      )}
    </>
  );
}
