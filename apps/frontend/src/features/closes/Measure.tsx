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
      <TrackRecord />
    </>
  );
}

type TrackRow = {
  entity: string;
  proposals_confirmed: number;
  proposals_kept: number;
  filled_by_person: number;
  changed_fields: { field: string; count: number }[];
  drafts_reviewed: number;
  drafts_approved_unrevised: number;
  drafts_revised: number;
  drafts_sent_back: number;
};

const share = (part: number, whole: number) => (whole === 0 ? "—" : `${part} of ${whole} (${Math.round((100 * part) / whole)}%)`);

function TrackRecord() {
  const [data, setData] = useState<{ window_from: string; clients: TrackRow[]; unlinked_human_drafts: number; has_more: boolean; assistant?: { asked: number; kept: number; median_seconds: number | null } }>();
  const [error, setError] = useState("");
  useEffect(() => {
    let live = true;
    workspaceRequest("/track-record")
      .then((result) => live && setData(result))
      .catch((e) => live && setError(e instanceof Error ? e.message : "The track record is unavailable."));
    return () => {
      live = false;
    };
  }, []);
  if (!data) return error ? <p role="alert">{error}</p> : null;
  return (
    <section aria-label="Agent track record by client">
      <div className="section-title">
        <h2>Agent track record by client</h2>
      </div>
      <p className="note">
        Closes from {data.window_from} on. Proposals are the model's readings of client documents that a person confirmed; kept means the person
        confirmed every value the model gave. Drafts count once a person has judged them, by a recorded review decision or a revision. Rule-based readings,
        carried-forward and recurring work, and {data.unlinked_human_drafts} drafts people saved directly are not counted.
      </p>
      {data.assistant && data.assistant.asked > 0 && (
        <p>
          Written assistant answers in the last 30 days, on closes you can open: {data.assistant.asked} asked, {data.assistant.kept} kept
          {data.assistant.median_seconds !== null && `, typically ${data.assistant.median_seconds.toFixed(1)} s to write`}.
        </p>
      )}
      <div className="rows">
        <div className="rows-head">
          <span>Client</span>
          <span>Proposals kept as proposed</span>
          <span>Drafts approved without revision</span>
          <span>Most changed</span>
        </div>
        {data.clients.length === 0 && <p className="empty">No confirmed model proposals {data.has_more ? "among the first 200 clients" : "in this window"} yet.</p>}
        {data.clients.map((c) => (
          <div className="row" key={c.entity}>
            <span className="row-main">
              <Avatar label={c.entity} />
              <strong>{c.entity}</strong>
            </span>
            <span>
              {share(c.proposals_kept, c.proposals_confirmed)}
              {c.filled_by_person > 0 && <small> · {c.filled_by_person} blank values filled by a person</small>}
            </span>
            <span>
              {share(c.drafts_approved_unrevised, c.drafts_reviewed)}
              {c.drafts_reviewed > 0 && (
                <small>
                  {" "}
                  · {c.drafts_revised} revised, {c.drafts_sent_back} sent back (a draft can be both)
                </small>
              )}
            </span>
            <small>{c.changed_fields.map((f) => `${f.field.replaceAll("_", " ")} ×${f.count}`).join(", ") || "—"}</small>
          </div>
        ))}
      </div>
      {data.has_more && <p className="note">Showing the first 200 clients.</p>}
    </section>
  );
}
