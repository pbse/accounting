import { useState, type FormEvent } from "react";
import { workspaceRequest } from "./workspace";

type Grant = {
  id: number;
  subject: string;
  client_entity: string;
  granted: boolean;
  reason: string;
  actor: string;
  created_at: string;
};
export function ClientAccess({ entities }: { entities: string[] }) {
  const [history, setHistory] = useState<Grant[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [user, setUser] = useState("");
  const [entity, setEntity] = useState(entities[0] ?? "");
  const [message, setMessage] = useState("");
  async function load() {
    setBusy(true);
    setError("");
    try {
      setHistory(await workspaceRequest("/access"));
      setLoaded(true);
    } catch (e) {
      setError(
        e instanceof Error ? e.message : "Access administration unavailable.",
      );
    } finally {
      setBusy(false);
    }
  }
  const current = history.find(
    (g) =>
      g.subject === user.trim() &&
      g.client_entity.toLowerCase() === entity.toLowerCase(),
  );
  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget;
    const data = new FormData(form);
    setBusy(true);
    setError("");
    setMessage("");
    try {
      await workspaceRequest("/access", {
        subject: user.trim(),
        entity,
        previous_id: current ? String(current.id) : "",
        granted: data.get("granted") === "on",
        reason: data.get("reason"),
        confirmed: data.get("confirmed") === "on",
      });
      setMessage(
        "Assignment decision saved. It applies to subsequent requests; it does not recall already downloaded data or change WorkOS roles.",
      );
      await load();
    } catch (e) {
      setError(
        e instanceof Error
          ? e.message
          : "Access change could not be confirmed.",
      );
    } finally {
      setBusy(false);
    }
  }
  return (
    <details
      onToggle={(event) => {
        if (event.currentTarget.open && !loaded && !busy) void load();
      }}
    >
      <summary>Client access administration</summary>
      <p>
        Invite the accounting professional to your WorkOS organization first.
        Give them only the needed review/procedure/sign-off roles, without
        clients:all. Then assign their exact WorkOS user ID to a client here.
        Assignments cover all periods of that client.
      </p>
      <p>
        Only firm administrators with clients:all and members:manage can change
        assignments. This does not invite users or grant action permissions.
        Local development decisions do not identify a real person.
      </p>
      {error && <p role="alert">{error}</p>}
      {message && <p role="status">{message}</p>}
      <button disabled={busy} onClick={() => void load()}>
        Refresh access history
      </button>
      {loaded && (
        <>
          <form
            key={current?.id ?? "new"}
            onSubmit={(event) => void save(event)}
          >
            <fieldset disabled={busy}>
              <legend>Record a client assignment decision</legend>
              <label>
                Exact WorkOS user ID
                <input
                  required
                  value={user}
                  onChange={(e) => setUser(e.target.value)}
                  pattern="user_.+"
                  maxLength={200}
                  placeholder="user_…"
                  autoComplete="off"
                />
              </label>
              <label>
                Client
                <select
                  required
                  value={entity}
                  onChange={(e) => setEntity(e.target.value)}
                >
                  {entities.map((name) => (
                    <option key={name} value={name}>
                      {name}
                    </option>
                  ))}
                </select>
              </label>
              <p>
                Current assignment:{" "}
                {current?.granted ? "Granted" : "Not granted"}
              </p>
              <label>
                <input name="granted" type="checkbox" />
                Grant access (leave unchecked to revoke)
              </label>
              <label>
                Reason and authorization reference
                <textarea required name="reason" maxLength={2000} />
              </label>
              <label>
                <input required type="checkbox" name="confirmed" />I verified
                this user, client and intended access. Firm-wide WorkOS roles
                override client assignments and must be removed separately when
                needed.
              </label>
              <button disabled={!entity}>Save access decision</button>
            </fieldset>
          </form>
          <details>
            <summary>Grant and revocation history ({history.length})</summary>
            <ol>
              {history.map((g) => (
                <li key={g.id}>
                  {g.granted ? "Granted" : "Revoked"} · {g.subject} ·{" "}
                  {g.client_entity}
                  <p>
                    {g.reason} · {g.actor} ·{" "}
                    {new Date(g.created_at).toLocaleString()}
                  </p>
                </li>
              ))}
            </ol>
          </details>
        </>
      )}
    </details>
  );
}
