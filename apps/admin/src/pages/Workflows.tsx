import { Link } from "react-router";
import { useResource } from "../api";

type Definition = {
  name: string;
  title: string;
  description: string;
  triggers: { Kind: string; Value: string }[];
  active_runs: number;
  completed_7d: number;
  failed_7d: number;
};
type Activity = { Name: string; Title: string; Description: string; Permission: string; Effect: string };

function trigger(t: { Kind: string; Value: string }): string {
  switch (t.Kind) {
    case "manual": return "manual";
    case "schedule": return `platform schedule ${t.Value}`;
    case "schedule_each_firm": return `each firm ${t.Value}`;
    case "event": return `on ${t.Value}`;
  }
  return t.Kind;
}

export function Workflows() {
  const workflows = useResource<{ workflows: Definition[] }>("/v1/admin/workflows/definitions");
  const activities = useResource<{ activities: Activity[] }>("/v1/admin/workflows/activities");
  return (
    <>
      <div className="page-title">
        <div><div><h1>Workflows</h1><p>What this build can run, and the building blocks plans use.</p></div></div>
        <div className="title-actions"><Link className="button primary" to="/runs/new">Start a run</Link></div>
      </div>
      {workflows.error && <p role="alert">{workflows.error}</p>}
      {workflows.data && (
        <section>
          <table>
            <thead><tr><th>Workflow</th><th>Starts</th><th>Active</th><th>Completed (7 days)</th><th>Failed (7 days)</th><th></th></tr></thead>
            <tbody>
              {workflows.data.workflows.map((w) => (
                <tr key={w.name}>
                  <td><strong>{w.title}</strong> <small>{w.name}</small><br /><small>{w.description}</small></td>
                  <td>{w.triggers.map(trigger).join(", ")}</td>
                  <td><Link to={`/runs?workflow=${w.name}&state=waiting`}>{w.active_runs}</Link></td>
                  <td>{w.completed_7d}</td>
                  <td>{w.failed_7d ? <Link to={`/runs?workflow=${w.name}&state=failed`}>{w.failed_7d}</Link> : 0}</td>
                  <td><Link to={`/runs/new?workflow=${w.name}`}>Start</Link></td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}
      {activities.data && (
        <section>
          <h2>Activities</h2>
          <table>
            <thead><tr><th>Activity</th><th>Does</th><th>Approver needs</th></tr></thead>
            <tbody>
              {activities.data.activities.map((a) => (
                <tr key={a.Name}>
                  <td><strong>{a.Title}</strong> <small>{a.Name}</small><br /><small>{a.Description}</small></td>
                  <td>{a.Effect === "external" ? "acts outside our records" : a.Effect === "internal" ? "writes our records" : "reads only"}</td>
                  <td>{a.Permission || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      )}
    </>
  );
}
