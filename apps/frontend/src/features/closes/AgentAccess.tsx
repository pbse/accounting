import { useEffect, useState } from "react";
import { workspaceRequest } from "./workspace";

type Grant = { id: number; subject: string; state: "requested" | "approved" | "declined" | "revoked"; scope: "read" | "prepare"; all_clients: boolean; reason: string; actor: string; created_at: string; effective_scope?: "read" | "prepare" | null; effective_all_clients?: boolean | null };
type Access = { mine: Grant | null; members: Grant[]; firm_wide: boolean };

const describe = (scope?: string | null, all?: boolean | null) =>
  scope ? `${scope === "prepare" ? "Read and prepare" : "Read only"} · ${all ? "all clients" : "your assigned clients"}` : "No agent access";

// Members ask from their own session; firm administrators approve, narrow, decline or revoke.
export function AgentAccess() {
  const [access, setAccess] = useState<Access>();
  const [local, setLocal] = useState("");
  const [form, setForm] = useState({ scope: "read", all_clients: false, reason: "" });
  const [decision, setDecision] = useState<Record<number, { scope: string; all_clients: boolean; reason: string }>>({});
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [refresh, setRefresh] = useState(0);
  useEffect(() => {
    let live = true;
    workspaceRequest("/agent-access")
      .then((data) => live && setAccess(data))
      .catch((e) => live && setLocal(e instanceof Error ? e.message : "Agent access is unavailable."));
    return () => {
      live = false;
    };
  }, [refresh]);
  async function send(body: Record<string, unknown>) {
    setBusy(true);
    setMessage("");
    try {
      await workspaceRequest("/agent-access", { ...body, confirmed: true });
      setForm({ scope: "read", all_clients: false, reason: "" });
      setRefresh((n) => n + 1);
    } catch (e) {
      setMessage(e instanceof Error ? e.message : "Agent access change failed.");
    } finally {
      setBusy(false);
    }
  }
  const mine = access?.mine;
  return (
    <section aria-label="Agent access">
      <h2>Agent access (MCP)</h2>
      <p>AI assistants such as Claude or ChatGPT can work on closes for you over MCP. They never approve, sign off or post, and see no more clients than you do. You ask for access here; a firm administrator approves it.</p>
      {local && <p className="note">{local}</p>}
      {access && (
        <>
          <p>
            Your agent access: <strong>{describe(mine?.effective_scope, mine?.effective_all_clients)}</strong>
            {mine?.state === "requested" && ` · request pending: ${describe(mine.scope, mine.all_clients)}`}
          </p>
          <div className="form-grid">
            <label>
              Ask for
              <select value={form.scope} onChange={(e) => setForm({ ...form, scope: e.target.value })}>
                <option value="read">Read only</option>
                <option value="prepare">Read and prepare (drafts, client questions, approval requests)</option>
              </select>
            </label>
            {access.firm_wide && (
              <label>
                <input type="checkbox" checked={form.all_clients} onChange={(e) => setForm({ ...form, all_clients: e.target.checked })} /> All clients (you have firm-wide access)
              </label>
            )}
          </div>
          <label>
            Why
            <input value={form.reason} maxLength={2000} placeholder="For example: my assistant prepares Monday's closes" onChange={(e) => setForm({ ...form, reason: e.target.value })} />
          </label>
          <button type="button" disabled={busy || form.reason.trim().length < 10} onClick={() => void send({ action: "request", ...form, previous_id: mine ? String(mine.id) : "" })}>
            Request agent access
          </button>
          {mine && mine.state !== "revoked" && (
            <button type="button" disabled={busy || form.reason.trim().length < 10} onClick={() => void send({ action: "revoke", subject: mine.subject, reason: form.reason, previous_id: String(mine.id) })}>
              Revoke my agent access
            </button>
          )}
          {access.members.length > 0 && (
            <>
              <h3>Members</h3>
              <div className="rows">
                {access.members.map((grant) => {
                  const choice = decision[grant.id] ?? { scope: grant.scope, all_clients: grant.all_clients, reason: "" };
                  const set = (patch: Partial<typeof choice>) => setDecision({ ...decision, [grant.id]: { ...choice, ...patch } });
                  return (
                    <div className="row" key={grant.id}>
                      <span>
                        <strong>{grant.subject}</strong>
                        <small>
                          In force: {describe(grant.effective_scope, grant.effective_all_clients)}
                          {grant.state === "requested" ? ` · asks for ${describe(grant.scope, grant.all_clients)}: “${grant.reason}”` : ` · last ${grant.state}`}
                        </small>
                      </span>
                      {grant.state === "requested" ? (
                        <span>
                          <select aria-label="Approve as" value={choice.scope} onChange={(e) => set({ scope: e.target.value })}>
                            {grant.scope === "prepare" && <option value="prepare">Read and prepare</option>}
                            <option value="read">Read only</option>
                          </select>
                          {grant.all_clients && (
                            <label>
                              <input type="checkbox" checked={choice.all_clients} onChange={(e) => set({ all_clients: e.target.checked })} /> All clients
                            </label>
                          )}
                        </span>
                      ) : (
                        <span />
                      )}
                      <input aria-label="Reason" placeholder="Reason" value={choice.reason} maxLength={2000} onChange={(e) => set({ reason: e.target.value })} />
                      <span>
                        {grant.state === "requested" && (
                          <>
                            <button type="button" className="primary" disabled={busy || choice.reason.trim().length < 10} onClick={() => void send({ action: "approve", subject: grant.subject, scope: choice.scope, all_clients: choice.all_clients, reason: choice.reason, previous_id: String(grant.id) })}>
                              Approve
                            </button>
                            <button type="button" disabled={busy || choice.reason.trim().length < 10} onClick={() => void send({ action: "decline", subject: grant.subject, scope: grant.scope, reason: choice.reason, previous_id: String(grant.id) })}>
                              Decline
                            </button>
                          </>
                        )}
                        {grant.state !== "revoked" && (
                          <button type="button" disabled={busy || choice.reason.trim().length < 10} onClick={() => void send({ action: "revoke", subject: grant.subject, reason: choice.reason, previous_id: String(grant.id) })}>
                            Revoke
                          </button>
                        )}
                      </span>
                    </div>
                  );
                })}
              </div>
            </>
          )}
        </>
      )}
      {message && <p role="alert">{message}</p>}
    </section>
  );
}
