import { useEffect, useState } from "react";
import { Link } from "react-router";
import { workspaceRequest } from "./workspace";
import { AgentMark } from "@close/ui/components/AgentMark";
import { SectionHeader } from "@close/ui/components/SectionHeader";

type Morning = {
  overnight: { month_end_snapshots: number; drafts_prepared: number; standing_approvals: number };
  closes: { close_id: string; entity: string; period: string; needs: string[] }[];
  needing: number;
  quiet: number;
  model_check: { model: string; reasoning_effort?: string | null; close_id: string; kept: number; scored: number; checked_at: string } | null;
};

const count = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

export function MorningBrief({ refresh }: { refresh: number }) {
  const [brief, setBrief] = useState<Morning>();
  const [error, setError] = useState("");
  useEffect(() => {
    let live = true;
    workspaceRequest("/morning")
      .then((data) => {
        if (!live) return;
        setBrief(data);
        setError("");
      })
      .catch((e) => {
        if (!live) return;
        setBrief(undefined);
        setError(e instanceof Error ? e.message : "The morning brief is unavailable.");
      });
    return () => {
      live = false;
    };
  }, [refresh]);
  if (!brief) return error ? <p role="alert">{error}</p> : null;
  const done = brief.overnight;
  return (
    <section aria-label="Morning brief">
      <SectionHeader title={<AgentMark size="sm">Good morning</AgentMark>} />
      <p>
        In the past 24 hours the agent captured {count(done.month_end_snapshots, "month-end snapshot", "month-end snapshots")}, prepared{" "}
        {count(done.drafts_prepared, "draft", "drafts")} and approved {count(done.standing_approvals, "later-month draft", "later-month drafts")} under standing
        approvals.
      </p>
      {brief.model_check && (
        <p className={brief.model_check.kept === brief.model_check.scored ? "note" : undefined} role={brief.model_check.kept === brief.model_check.scored ? undefined : "status"}>
          Using {brief.model_check.model} ({brief.model_check.reasoning_effort == null ? "reasoning effort not recorded" : `${brief.model_check.reasoning_effort || "provider default"} reasoning effort`}), {brief.model_check.kept} of {brief.model_check.scored} past confirmed readings came out the same
          ({new Date(brief.model_check.checked_at).toLocaleDateString()}). <Link to={`/dashboard/closes/${brief.model_check.close_id}`}>See the readings</Link>.
        </p>
      )}
      {brief.needing === 0 ? (
        <p className="empty">No open close has drafts, confirmations, failed documents, missing snapshots or new client questions waiting. Each close still shows its own tie-out and changes.</p>
      ) : (
        <>
          <p>
            <strong>{count(brief.needing, "close needs", "closes need")} a person today</strong>
            {brief.closes.length < brief.needing ? ` (showing ${brief.closes.length})` : ""}:
          </p>
          <ul>
            {brief.closes.map((c) => (
              <li key={c.close_id}>
                <Link to={`/dashboard/closes/${c.close_id}`}>
                  {c.entity} · {c.period}
                </Link>
                : {c.needs.join(", ")}
              </li>
            ))}
          </ul>
          {brief.quiet > 0 && (
            <p className="note">
              {count(brief.quiet, "other open close has", "other open closes have")} none of these waiting; its tie-out and changes are on the close page.
            </p>
          )}
        </>
      )}
    </section>
  );
}
