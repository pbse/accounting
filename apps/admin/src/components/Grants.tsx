import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { post, useResource, when } from "../api";
import { ActionForm } from "./ActionForm";

// A content grant: the staff member's time-boxed access to one firm's client content. While it lasts,
// that firm's screens reveal without asking for a reason; each reveal is still recorded.
export type Grant = { id: number; organization: string; firm_name: string; reason: string; expires_at: string };

const GrantsContext = createContext<{ grants: Grant[]; reload: () => void }>({ grants: [], reload: () => {} });

// GrantsProvider loads the staff member's open grants once for the console and keeps them current.
export function GrantsProvider({ enabled, children }: { enabled: boolean; children: ReactNode }) {
  const grants = useResource<{ grants: Grant[] }>(enabled ? "/v1/admin/grants" : null);
  const [, tick] = useState(0);
  // Re-read every minute, so an expired grant stops revealing without a reload.
  useEffect(() => {
    if (!enabled) return;
    const timer = setInterval(() => {
      tick((n) => n + 1);
      grants.reload();
    }, 60_000);
    return () => clearInterval(timer);
  }, [enabled, grants.reload]);
  const live = (grants.data?.grants ?? []).filter((g) => new Date(g.expires_at).getTime() > Date.now());
  return <GrantsContext.Provider value={{ grants: live, reload: grants.reload }}>{children}</GrantsContext.Provider>;
}

export const useGrants = () => useContext(GrantsContext);

// useGrantFor is the staff member's open grant for a firm, if any.
export function useGrantFor(firm?: string | null): Grant | undefined {
  const { grants } = useGrants();
  return firm ? grants.find((g) => g.organization === firm) : undefined;
}

// OpenGrant opens a grant for a firm.
export function OpenGrant({ firm }: { firm: string }) {
  const { reload } = useGrants();
  const [minutes, setMinutes] = useState(60);
  return (
    <details>
      <summary>Open a content grant for this firm</summary>
      <p>For a stretch of work on this firm: its screens show client content without asking each time, and every view is still recorded.</p>
      <ActionForm label="Open grant" minLength={10} onSubmit={async (reason) => {
        await post(`/v1/admin/firms/${encodeURIComponent(firm)}/grant`, { minutes, reason });
        reload();
      }}>
        <label>For
          <select value={minutes} onChange={(event) => setMinutes(Number(event.target.value))}>
            {[15, 30, 60, 120, 240, 480].map((m) => <option key={m} value={m}>{m < 60 ? `${m} minutes` : `${m / 60} hour${m > 60 ? "s" : ""}`}</option>)}
          </select>
        </label>
      </ActionForm>
    </details>
  );
}

// GrantList shows the staff member's open grants, each with a way to end it early.
export function GrantList() {
  const { grants, reload } = useGrants();
  const [error, setError] = useState("");
  if (grants.length === 0) return null;
  return (
    <div className="grants">
      <strong>Content grants</strong>
      {grants.map((g) => (
        <p key={g.id}>
          {g.firm_name} until {when(g.expires_at)}{" "}
          <button className="link" onClick={() => post(`/v1/admin/grants/${g.id}/end`, {}).then(reload, (e: Error) => setError(e.message))}>End</button>
        </p>
      ))}
      {error && <p role="alert">{error}</p>}
    </div>
  );
}
