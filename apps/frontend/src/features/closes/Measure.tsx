import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { Avatar, Icon } from "../dashboard/Icon";
import { minutes } from "./Effort";
import { workspaceRequest } from "./workspace";

type Row = { close_id: string; entity: string; tracked_seconds: number; recorded_minutes: number; approved_drafts: number; measured: boolean; total_minutes?: number };
type Report = { period: string; items: Row[]; measured: number; median_minutes?: number; has_more: boolean };

export default function Measure() {
  const [params, setParams] = useSearchParams();
  const [report, setReport] = useState<Report>();
  const [error, setError] = useState("");
  const period = params.get("period") ?? "";
  useEffect(() => {
    let live = true;
    setError("");
    workspaceRequest(`/effort${period ? `?period=${period}` : ""}`)
      .then((data) => live && setReport(data))
      .catch((e) => live && setError(e instanceof Error ? e.message : "Effort is unavailable."));
    return () => {
      live = false;
    };
  }, [period]);
  return (
    <>
      <div className="page-title">
        <div>
          <span className="kpi-icon" aria-hidden="true">
            <Icon name="measure" />
          </span>
          <span>
            <h1>Human effort</h1>
            <p>The PRD north star: total human minutes per client close, kept separate from waiting on clients.</p>
          </span>
        </div>
        <label className="title-actions">
          Close month
          <input type="month" value={report?.period ?? period} onChange={(e) => setParams(e.target.value ? { period: e.target.value } : {})} />
        </label>
      </div>
      {error && <p role="alert">{error}</p>}
      {report && (
        <>
          <div className="kpis">
            <div className="kpi">
              <span className="kpi-head">
                <span className="kpi-icon">
                  <Icon name="measure" size={16} />
                </span>
                Median human minutes
              </span>
              <span className="kpi-body">
                <span className="kpi-value">{report.median_minutes === undefined ? "–" : minutes(report.median_minutes)}</span>
                <span className="kpi-caption">Across measured closes only</span>
              </span>
            </div>
            <div className="kpi">
              <span className="kpi-head">
                <span className="kpi-icon green">
                  <Icon name="clients" size={16} />
                </span>
                Measured closes
              </span>
              <span className="kpi-body">
                <span className="kpi-value">{report.measured}</span>
                <span className="kpi-caption">Closes with tracked or recorded time; others are not counted as zero</span>
              </span>
            </div>
          </div>
          <p className="note">
            Tracked activity counts visible, interactive workspace time; off-screen minutes are whatever the team records on each close. Neither includes
            our own team's assistance, which is recorded separately.
          </p>
          <div className="rows">
            <div className="rows-head">
              <span>Client</span>
              <span>Human minutes</span>
              <span>Tracked · recorded</span>
              <span>Approved drafts</span>
            </div>
            {report.items.length === 0 && <p className="empty">No closes in {report.period || "this month"}.</p>}
            {report.items.map((row) => (
              <Link className="row" key={row.close_id} to={`/dashboard/closes/${row.close_id}?tab=signoff`}>
                <span className="row-main">
                  <Avatar label={row.entity} />
                  <strong>{row.entity}</strong>
                </span>
                <span>{row.total_minutes === undefined ? <span className="status">Not measured</span> : <strong>{minutes(row.total_minutes)}</strong>}</span>
                <small>{row.measured ? `${minutes(row.tracked_seconds / 60)} · ${minutes(row.recorded_minutes)}` : "—"}</small>
                <span className="chip">{row.approved_drafts}</span>
              </Link>
            ))}
          </div>
          {report.has_more && <p className="note">Showing the first 200 closes this month.</p>}
        </>
      )}
    </>
  );
}
