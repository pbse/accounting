import { useResource, when } from "../api";

type Heartbeat = { loop: string; instance: string; beat_at: string; stale: boolean; panic: string | null; panicked_at: string | null };
type Overview = {
  heartbeats: Heartbeat[];
  runs: Record<string, number>;
  oldest_queued_at: string | null;
  unknown_effects: number;
  firms: number;
  suspended_firms: number;
  reconnect_required: number;
  failed_preparation_24h: number;
};

export function Overview() {
  const overview = useResource<Overview>("/v1/admin/overview");
  const o = overview.data;
  return (
    <>
      <div className="page-title">
        <div>
          <div>
            <h1>Overview</h1>
            <p>The platform's health across every firm.</p>
          </div>
        </div>
        <div className="title-actions">
          <button onClick={overview.reload} disabled={overview.loading}>Refresh</button>
        </div>
      </div>
      {overview.error && <p role="alert">{overview.error}</p>}
      {o && (
        <>
          <div className="stats">
            <Stat label="Firms" value={o.firms} note={o.suspended_firms ? `${o.suspended_firms} suspended` : "none suspended"} />
            <Stat label="Workflow runs" value={(o.runs.running ?? 0) + (o.runs.waiting ?? 0) + (o.runs.queued ?? 0)} note={`${o.runs.failed ?? 0} failed in 24 hours`} tone={o.runs.failed ? "warn" : "ok"} />
            <Stat label="Oldest queued run" value={o.oldest_queued_at ? when(o.oldest_queued_at) : "none"} />
            <Stat label="Uncertain provider writes" value={o.unknown_effects} note="waiting for reconciliation" tone={o.unknown_effects ? "danger" : "ok"} />
            <Stat label="Connections to reconnect" value={o.reconnect_required} tone={o.reconnect_required ? "warn" : "ok"} />
            <Stat label="Preparation failed" value={o.failed_preparation_24h} note="in 24 hours" tone={o.failed_preparation_24h ? "warn" : "ok"} />
          </div>
          <section>
            <h2>Background loops</h2>
            {o.heartbeats.length === 0 ? (
              <p>No loop has reported yet. The worker records a heartbeat every 30 seconds once it runs.</p>
            ) : (
              <table>
                <thead>
                  <tr><th>Loop</th><th>Instance</th><th>Last heartbeat</th><th>State</th></tr>
                </thead>
                <tbody>
                  {o.heartbeats.map((h) => (
                    <tr key={h.loop}>
                      <td>{h.loop}</td>
                      <td>{h.instance || "—"}</td>
                      <td>{when(h.beat_at)}</td>
                      <td>
                        <span className={`status ${h.stale ? "danger" : "ok"}`}>{h.stale ? "Silent for 5 minutes" : "Alive"}</span>
                        {h.panic && h.panicked_at && <div><span className="status warn">Last panic {when(h.panicked_at)}, restarted: {h.panic}</span></div>}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </section>
        </>
      )}
    </>
  );
}

export function Stat({ label, value, note, tone }: { label: string; value: number | string; note?: string; tone?: "ok" | "warn" | "danger" }) {
  return (
    <div className="stat">
      <span className="stat-label">{label}</span>
      <strong>{value}</strong>
      {note && <span className={tone ? `status ${tone}` : "stat-note"}>{note}</span>}
    </div>
  );
}
