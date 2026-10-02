import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { PageHeader } from "@close/ui/components/PageHeader";
import { EntityCell, RowList } from "@close/ui/components/RowList";
import { SectionHeader } from "@close/ui/components/SectionHeader";
import { DotChart } from "@close/ui/components/DotChart";
import { HexMap } from "@close/ui/components/HexMap";
import { StatGroup, StatTile } from "@close/ui/components/StatTile";
import { minutes } from "./Effort";
import { workspaceRequest } from "./workspace";

type Row = {
  close_id: string;
  entity: string;
  tracked_seconds: number;
  recorded_minutes: number;
  approved_drafts: number;
  measured: boolean;
  total_minutes?: number;
  reviews: number;
  reviews_after_approval: number;
  review_minutes: number;
};
type Report = {
  period: string;
  items: Row[];
  measured: number;
  median_minutes?: number;
  has_more: boolean;
  reviews: number;
  reviews_after_approval: number;
  trend: { period: string; measured: number; median_minutes?: number }[];
};

const monthLabel = (period: string) => new Date(`${period}-01T00:00:00`).toLocaleDateString(undefined, { month: "short", year: "numeric" });

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
      <PageHeader
        icon="measure"
        title="Human effort"
        actions={
          <label className="title-actions">
            Close month
            <input type="month" value={report?.period ?? period} onChange={(e) => setParams(e.target.value ? { period: e.target.value } : {})} />
          </label>
        }
      >
        The PRD north star: total human minutes per client close, kept separate from waiting on clients.
      </PageHeader>
      {error && <p role="alert">{error}</p>}
      {report && (
        <>
          <StatGroup>
            <StatTile
              icon="measure"
              label="Median human minutes"
              value={report.median_minutes === undefined ? undefined : minutes(report.median_minutes)}
              caption="Across measured closes only"
            />
            <StatTile icon="clients" tone="teal" label="Measured closes" value={report.measured} caption="Closes with tracked or recorded time; others are not counted as zero" />
          </StatGroup>
          {report.trend.some((month) => month.measured > 0) && (
            <section aria-labelledby="effort-trend">
              <h2 id="effort-trend">Median human minutes per close, by month</h2>
              <DotChart
                label="Median human minutes per measured close, by month"
                unit="min"
                points={report.trend.map((month) => ({
                  label: monthLabel(month.period),
                  value: month.median_minutes ?? null,
                  note: month.measured ? `${month.measured} measured close${month.measured === 1 ? "" : "s"}` : "no measured closes",
                }))}
              />
            </section>
          )}
          <p className="note">
            Tracked activity counts visible, interactive workspace time; off-screen minutes are whatever the team records on each close. Neither includes
            our own team's assistance, which is recorded separately.
          </p>
          <p className="note">
            Reviews this month: {report.reviews}, of which {report.reviews_after_approval} came after an earlier version was approved (any reason, not only a
            changed source). Review minutes are estimates: tracked time counts each person's minute once under its latest activity, and recorded time
            under what was entered.
          </p>
          <RowList columns={["Client", "Human minutes", "Tracked · recorded", "Approved · reviews"]}>
            {report.items.length === 0 && <p className="empty">No closes in {report.period || "this month"}.</p>}
            {report.items.map((row) => (
              <Link className="row" key={row.close_id} to={`/dashboard/closes/${row.close_id}?tab=signoff`}>
                <EntityCell name={row.entity} title={row.entity} />
                <span>{row.total_minutes === undefined ? <span className="status">Not measured</span> : <strong>{minutes(row.total_minutes)}</strong>}</span>
                <small>{row.measured ? `${minutes(row.tracked_seconds / 60)} · ${minutes(row.recorded_minutes)}` : "—"}</small>
                <span>
                  <span className="chip">{row.approved_drafts}</span>
                  {row.reviews > 0 && (
                    <small>
                      {" "}
                      {row.reviews} reviewed, {row.reviews_after_approval} after approval · {minutes(row.review_minutes)} review
                    </small>
                  )}
                </span>
              </Link>
            ))}
          </RowList>
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
  // How people treated the agent's work across the listed clients (every client unless the list stops at 200): each
  // whole splits into two parts that never overlap.
  const sum = (pick: (c: TrackRow) => number) => data.clients.reduce((total, c) => total + pick(c), 0);
  const scope = data.has_more ? ", first 200 clients" : "";
  const proposals = { confirmed: sum((c) => c.proposals_confirmed), kept: sum((c) => c.proposals_kept) };
  const drafts = { judged: sum((c) => c.drafts_reviewed), approved: sum((c) => c.drafts_approved_unrevised) };
  return (
    <section aria-label="Agent track record by client">
      <SectionHeader title="Agent track record by client" />
      <p className="note">
        Closes from {data.window_from} on. Proposals are the model's readings of client documents that a person confirmed; kept means the person
        confirmed every value the model gave. Drafts count once a person has judged them, by a recorded review decision or a revision. Rule-based readings,
        carried-forward and recurring work, and {data.unlinked_human_drafts} drafts people saved directly are not counted.
      </p>
      {data.has_more && (proposals.confirmed > 0 || drafts.judged > 0) && <p className="note">The charts cover the first 200 clients, the ones listed below.</p>}
      {(proposals.confirmed > 0 || drafts.judged > 0) && (
        <div className="track-split">
          {proposals.confirmed > 0 && (
            <section aria-label="Model proposals people confirmed">
              <h3>Model proposals people confirmed</h3>
              <HexMap
                label={`Model proposals people confirmed${scope}`}
                parts={[
                  { label: "Kept as proposed", value: proposals.kept, tone: "blue" },
                  { label: "Changed by a person", value: proposals.confirmed - proposals.kept, tone: "teal" },
                ]}
              />
            </section>
          )}
          {drafts.judged > 0 && (
            <section aria-label="Agent drafts people judged">
              <h3>Agent drafts people judged</h3>
              <HexMap
                label={`Agent drafts people judged${scope}`}
                parts={[
                  { label: "Approved as prepared", value: drafts.approved, tone: "blue" },
                  { label: "Revised or sent back", value: drafts.judged - drafts.approved, tone: "teal" },
                ]}
              />
            </section>
          )}
        </div>
      )}
      {data.assistant && data.assistant.asked > 0 && (
        <p>
          Written assistant answers in the last 30 days, on closes you can open: {data.assistant.asked} asked, {data.assistant.kept} kept
          {data.assistant.median_seconds !== null && `, typically ${data.assistant.median_seconds.toFixed(1)} s to write`}.
        </p>
      )}
      <RowList columns={["Client", "Proposals kept as proposed", "Drafts approved without revision", "Most changed"]}>
        {data.clients.length === 0 && <p className="empty">No confirmed model proposals {data.has_more ? "among the first 200 clients" : "in this window"} yet.</p>}
        {data.clients.map((c) => (
          <div className="row" key={c.entity}>
            <EntityCell name={c.entity} title={c.entity} />
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
      </RowList>
      {data.has_more && <p className="note">Showing the first 200 clients.</p>}
    </section>
  );
}
