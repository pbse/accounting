import { useEffect, useState } from "react";
import { Link } from "react-router";
import { workspaceRequest } from "./workspace";

type Alias = { alias: string; confirmed_by: string; confirmed_at: string; retired_by?: string; retired_at?: string; retire_reason?: string };
type Grant = { id: string; close_id: string; period: string; workpaper_id: string; kind: string; granted_by: string; reason: string; created_at: string; in_force: boolean };
type Value = { value: string; count: number; last_period: string; close_id: string; workpaper_id: string; approved_by: string; approved_at: string };
type Memory = { entity: string; aliases: Alias[]; standing_approvals: Grant[]; accounts: { kind: string; field: string; values: Value[] }[] };

const day = (at: string) => new Date(at).toLocaleDateString();

// What the system remembers about this client and who decided it; each can be retired or revoked here.
export function Memory({ closeId }: { closeId: string }) {
  const [memory, setMemory] = useState<Memory>();
  const [reasons, setReasons] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [version, setVersion] = useState(0);
  useEffect(() => {
    workspaceRequest(`/${closeId}/memory`)
      .then(setMemory)
      .catch((e) => setError(e instanceof Error ? e.message : "Client memory is unavailable."));
  }, [closeId, version]);
  async function act(path: string, body: unknown) {
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(path, body);
      setVersion((v) => v + 1);
    } catch (e) {
      setError(e instanceof Error ? e.message : "The change could not be saved.");
    } finally {
      setBusy(false);
    }
  }
  if (!memory) return error ? <p role="alert">{error}</p> : null;
  return (
    <section aria-label="Client memory">
      <h2>What we remember about {memory.entity}</h2>
      {error && <p role="alert">{error}</p>}
      <h3>Other names its documents use</h3>
      {memory.aliases.length === 0 ? (
        <p className="empty">None confirmed. A reviewer confirms one from Work when a document names the client differently.</p>
      ) : (
        <ul>
          {memory.aliases.map((a) => (
            <li key={a.alias + a.confirmed_at}>
              <strong>{a.alias}</strong> · confirmed by {a.confirmed_by} on {day(a.confirmed_at)}
              {a.retired_at ? (
                <>
                  {" "}
                  · retired by {a.retired_by} on {day(a.retired_at)}: {a.retire_reason}
                </>
              ) : (
                <form
                  onSubmit={(e) => {
                    e.preventDefault();
                    void act(`/${closeId}/memory`, { action: "retire_alias", alias: a.alias, reason: reasons[a.alias] ?? "", confirmed: true });
                  }}
                >
                  <label>
                    Why retire it
                    <input required maxLength={2000} value={reasons[a.alias] ?? ""} onChange={(e) => setReasons({ ...reasons, [a.alias]: e.target.value })} />
                  </label>
                  <button disabled={busy}>Retire this name</button>
                  <small> Later documents using it stop for a person again; work already done is unchanged.</small>
                </form>
              )}
            </li>
          ))}
        </ul>
      )}
      <h3>Standing approvals</h3>
      {memory.standing_approvals.length === 0 ? (
        <p className="empty">None. Later months of a schedule are reviewed as usual.</p>
      ) : (
        <ul>
          {memory.standing_approvals.map((g) => (
            <li key={g.id}>
              <Link to={`/dashboard/closes/${g.close_id}/workpapers/${g.workpaper_id}`}>
                Draft #{g.workpaper_id} ({g.kind}, {g.period})
              </Link>{" "}
              · granted by {g.granted_by} on {day(g.created_at)}: {g.reason} ·{" "}
              {g.in_force ? "in force" : "not in force: its approval changed or the granter left the firm"}. Revoke it from the draft.
            </li>
          ))}
        </ul>
      )}
      <h3>Accounts approved before (12 months to this close)</h3>
      <p className="note">
        A record of past approvals, not a rule. <strong>Use</strong> while confirming a reading fills in that reading only. To make an account the rule for future preparation,
        change the client's procedure below; it shows the rule's scope, who approved it, when, and the version this close uses.
      </p>
      {memory.accounts.length === 0 ? (
        <p className="empty">No approved drafts name accounts yet.</p>
      ) : (
        <ul>
          {memory.accounts.map((a) => (
            <li key={a.kind + a.field}>
              {a.kind} {a.field.replaceAll("_", " ")}:{" "}
              {a.values.map((v) => (
                <span key={v.value}>
                  {v.value} ({v.count}×, last {v.last_period},{" "}
                  <Link to={`/dashboard/closes/${v.close_id}/workpapers/${v.workpaper_id}`}>approved by {v.approved_by} on {day(v.approved_at)}</Link>){" "}
                </span>
              ))}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
