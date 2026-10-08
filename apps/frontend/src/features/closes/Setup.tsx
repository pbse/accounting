import { useEffect, useState } from "react";
import { Link } from "react-router";
import { isEnrollmentError } from "./ClosesList";
import { FirmEnrollment } from "./Enterprise";
import { workspaceRequest } from "./workspace";

type Setup = {
  steps: { key: string; title: string; done: boolean; detail: string; href: string }[];
  permissions: string[];
  reconnect: { provider: string; client: string; error: string; href: string }[];
  can_reconnect: boolean;
};

export function Setup({ onEnrolled }: { onEnrolled: () => void }) {
  const [setup, setSetup] = useState<Setup>();
  const [error, setError] = useState("");
  const [version, setVersion] = useState(0);
  useEffect(() => {
    let live = true;
    workspaceRequest("/setup")
      .then((next: Setup) => live && (setSetup(next), setError("")))
      .catch((e) => live && setError(e instanceof Error ? e.message : "Setup is unavailable."));
    return () => {
      live = false;
    };
  }, [version]);
  if (error) return isEnrollmentError(error) ? <FirmEnrollment onEnrolled={() => (setVersion((v) => v + 1), onEnrolled())} /> : <p role="alert">Get started is unavailable: {error}</p>;
  if (!setup) return null;
  const left = setup.steps.filter((s) => !s.done).length;
  return (
    <details className="agent-card" open={left > 0 || setup.reconnect.length > 0}>
      <summary>Get started · {left ? `${left} of ${setup.steps.length} steps left` : "every step done"}{setup.reconnect.length > 0 && ` · ${setup.reconnect.length} connection${setup.reconnect.length === 1 ? "" : "s"} to reconnect`}</summary>
      {setup.reconnect.length > 0 && (
        <section aria-label="Connections to reconnect">
          <h3>Reconnect</h3>
          <ul>
            {setup.reconnect.map((c) => (
              <li key={c.provider + c.client}>
                {c.provider} for {c.client}: {c.error || "the connection needs to be authorized again"}. <Link to={c.href}>{setup.can_reconnect ? "Disconnect, connect and confirm it again →" : "Open it →"}</Link>
              </li>
            ))}
          </ul>
          <p>
            Reconnecting restores scheduled captures for this and last month's open closes. Existing jobs and blockers stay as they are.
            {!setup.can_reconnect && " Someone who manages the firm's connections reconnects it."}
          </p>
        </section>
      )}
      <ol>
        {setup.steps.map((s) => (
          <li key={s.key}>
            <strong>{s.done ? "✓ " : ""}{s.title}</strong> · {s.detail} {!s.done && <Link to={s.href}>Open →</Link>}
          </li>
        ))}
      </ol>
      <details>
        <summary>What you can do here</summary>
        <ul>
          {setup.permissions.map((p) => (
            <li key={p}>{p}</li>
          ))}
        </ul>
      </details>
    </details>
  );
}
