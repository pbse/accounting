import { useEffect, useState, type FormEvent } from "react";
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
  useEffect(() => {
    void load();
  }, []);
  return (
    <>
      <p className="note">
        Invite the person first and give them only the roles their work needs, without clients:all. Then assign their exact WorkOS user ID to a client here. Firm-wide roles
        override client assignments and must be removed separately. Local development decisions do not identify a real person.
      </p>
      {error && <p role="alert">{error}</p>}
      {message && <p role="status">{message}</p>}
      {loaded && (
        <>
          <details open={history.length === 0}>
          <summary>Record a client assignment decision</summary>
          <form
            key={current?.id ?? "new"}
            onSubmit={(event) => void save(event)}
          >
            <fieldset disabled={busy}>
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
              <label className="checkbox-label">
                <input name="granted" type="checkbox" />
                Grant access (leave unchecked to revoke)
              </label>
              <label>
                Reason and authorization reference
                <textarea required name="reason" maxLength={2000} />
              </label>
              <label className="checkbox-label">
                <input required type="checkbox" name="confirmed" />I verified this user, client and intended access.
              </label>
              <button disabled={!entity}>Save access decision</button>
            </fieldset>
          </form>
          </details>
          {history.length > 0 && <details>
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
          </details>}
        </>
      )}
    </>
  );
}
