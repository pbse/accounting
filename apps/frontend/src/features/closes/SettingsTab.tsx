import { Link, useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Icon } from "@close/ui/components/Icon";
import { workspaceQuery } from "../../data/queries";
import { monthShort, useDates } from "../../data/time";
import { RetentionControls } from "./Enterprise";
import { dueRule, type Status } from "./Goal";
import { Memory } from "./Memory";
import { AgentPanel, Glance, MoreLinks, NeedsYou, type Figure, type Need } from "./Overview";
import { Procedures } from "./Procedures";
import { Replay } from "./Replay";
import type { Brief } from "./Stands";
import type { Close, Paper } from "./WorkpaperView";

// A close's Settings (docs/design-system.md, Close page): what is remembered and set for the client, then the decisions
// on this tab and every setting as one table, each with what it is now; the rail counts what was learned and the agent's
// last check, and opens the tab's views (procedures, what is remembered, the agent check, retention and deletion).

type Remembered = {
  aliases: { alias: string; retired_at?: string }[];
  standing_approvals: { id: string; kind: string; period: string; in_force: boolean }[];
  accounts: { kind: string; field: string }[];
};
type Version = { id: string; client_entity: string; kind: string; effective_from?: string };
type Run = { items: { kept: boolean; error?: string }[]; created_at?: string } | null;
type Retention = { retain_until: string; legal_hold: boolean; deletion_blockers: string[] };

const basisNames: Record<string, string> = { cash: "Cash basis", modified_cash: "Modified cash basis", accrual: "Accrual basis" };
const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

// SettingsLede names the client and how its books are kept: the basis, its currency and fiscal year once settled (an
// unsettled profile is the chip below the lede).
export function SettingsLede({ close }: { close: Close }) {
  const profile = close.profile;
  const settled = profile && (profile.state === "confirmed" || profile.state === "detected");
  const facts = [
    close.basis ? basisNames[close.basis] ?? close.basis : "",
    settled ? `${profile.currency} · fiscal year starts ${new Date(2000, profile.fiscal_year_start - 1).toLocaleString("en-US", { month: "long" })}` : "",
  ].filter(Boolean);
  return (
    <div className="ks-lede">
      <h1>What Krith remembers about {close.entity}.</h1>
      {facts.length > 0 && <p>{facts.join(" · ")}</p>}
    </div>
  );
}

type Row = { key: string; label: string; now: string; status: string; tone: string; to: string };

const views: Record<string, string> = { procedures: "Procedures", memory: "What Krith remembers", replay: "Check the current agent", retention: "Retention and deletion" };

// SettingsTab is the tab's body: on the left what needs the person here and every setting as one table, or one of the
// tab's views; on the right what was learned and the agent's last check.
export function SettingsTab({ close, papers, needs, brief, briefRead, decisionsError, status, base, onApplied, onDeleted }: { close: Close; papers: Paper[]; needs: Need[]; brief: Brief | undefined; briefRead: { dataUpdatedAt: number; isFetching: boolean; refetch: () => unknown }; decisionsError: string; status: Status | undefined; base: string; onApplied: () => void; onDeleted: (receipt: string) => void }) {
  const dates = useDates();
  const [params] = useSearchParams();
  const asked = params.get("view");
  const view = asked && asked in views ? asked : "";
  const memoryRead = useQuery(workspaceQuery<Remembered>(`/${close.id}/memory`));
  const memory = memoryRead.data;
  const replay = useQuery(workspaceQuery<{ run: Run }>(`/${close.id}/replay`));
  const run = replay.data?.run;
  const procedures = useQuery(workspaceQuery<{ versions: Version[] }>("/procedures"));
  const retention = useQuery(workspaceQuery<Retention>(`/${close.id}/retention`));
  const to = (name: string) => `${base}?tab=settings&view=${name}`;
  // The client's own versions in force this month, newest first per kind; every other kind follows the firm's default.
  const overrides = [
    ...new Map(
      (procedures.data?.versions ?? [])
        .filter((v) => v.client_entity.toLowerCase() === close.entity.toLowerCase() && (!v.effective_from || v.effective_from.slice(0, 7) <= close.period))
        .map((v) => [v.kind, v] as const)
        .reverse(),
    ).keys(),
  ];
  const names = (memory?.aliases ?? []).filter((a) => !a.retired_at);
  const standing = (memory?.standing_approvals ?? []).filter((g) => g.in_force);
  const ledger = brief?.lines.find((line) => line.area === "ledger");
  const routine = status?.routine;
  // A reading the agent could not read again was not compared: it is neither the same nor different.
  const items = run?.items ?? [];
  const compared = items.filter((item) => !item.error);
  const same = compared.filter((item) => item.kept).length;
  const failed = items.length - compared.length;
  // The firm's own versions: without any, and without the client's, no approved procedure applies at all.
  const firmDefaults = (procedures.data?.versions ?? []).some((v) => v.client_entity === "");
  const unread = (read: { isError: boolean; error: Error | null }) => read.error?.message || "Could not be read.";
  const rows: Row[] = [
    {
      key: "procedures",
      label: "Procedures",
      now: procedures.isError ? unread(procedures) : !procedures.data ? "Reading…" : overrides.length ? `Client overrides for ${overrides.join(", ")}; the firm's defaults, where approved, for the rest` : firmDefaults ? "The firm's defaults, where approved" : "No approved procedure yet: the agent works from the facts each draft cites",
      status: !procedures.data ? "—" : overrides.length ? plural(overrides.length, "override") : firmDefaults ? "Firm defaults" : "None",
      tone: "neutral",
      to: to("procedures"),
    },
    {
      key: "ledger",
      label: "Ledger",
      now: ledger?.text ?? "Not read yet",
      status: ledger?.state === "done" ? "Read" : ledger?.state === "attention" ? "Needs you" : ledger?.state === "waiting" ? "Waiting" : "Unknown",
      tone: ledger?.state === "done" ? "" : ledger?.state === "attention" ? "danger" : "neutral",
      to: `${base}?tab=books&view=ledger`,
    },
    {
      key: "names",
      label: "Other names",
      now: memoryRead.isError ? unread(memoryRead) : names.length ? names.map((a) => a.alias).join(", ") : "Only its own name",
      status: names.length ? plural(names.length, "name") : "None",
      tone: "neutral",
      to: to("memory"),
    },
    {
      key: "standing",
      label: "Standing approvals",
      now: memoryRead.isError ? unread(memoryRead) : standing.length ? standing.map((g) => `${g.kind}, from ${monthShort(g.period)}`).join("; ") : "None: every draft is approved one by one",
      status: standing.length ? `${standing.length} in force` : "None",
      tone: "neutral",
      to: to("memory"),
    },
    {
      key: "handover",
      label: "Monthly hand-over",
      now: !status ? "Not read yet" : routine ? `Each month is handed to the agent, ${routine.finish === "signoff" ? "to be signed off" : "to be ready for review"}, ${dueRule(routine.due_days)}` : "Off: each month is handed over by hand",
      status: routine ? "On" : "Off",
      tone: routine ? "" : "neutral",
      to: `${base}#agent-details`,
    },
    {
      key: "retention",
      label: "Retention",
      now: retention.isError ? unread(retention) : !retention.data ? "Reading…" : `${retention.data.retain_until ? `Kept until ${dates.calendarDate(retention.data.retain_until)}` : "No retention date recorded"}${retention.data.legal_hold ? " · legal hold" : ""}`,
      status: !retention.data ? "—" : retention.data.legal_hold ? "Legal hold" : retention.data.deletion_blockers.length ? "Kept" : "No hold",
      tone: retention.data?.legal_hold ? "info" : "neutral",
      to: to("retention"),
    },
    {
      key: "replay",
      label: "Agent check",
      now: replay.isError
        ? unread(replay)
        : items.length
          ? `${same} of ${compared.length} confirmed readings read the same${failed ? `; ${failed} could not be read again` : ""}${run?.created_at ? `, ${dates.date(run.created_at)}` : ""}`
          : "Not checked yet: replay the confirmed readings with the current agent",
      status: !items.length ? "—" : same < compared.length ? "Differs" : failed ? "Not all read" : "Same",
      tone: !items.length ? "neutral" : same < compared.length ? "danger" : failed ? "neutral" : "",
      to: to("replay"),
    },
  ];
  const figures: Figure[] = [
    ...(procedures.data ? ([[String(overrides.length), overrides.length === 1 ? "client procedure override" : "client procedure overrides"]] as Figure[]) : []),
    ...(memory ? ([[String(memory.accounts.length), "accounts approved before"]] as Figure[]) : []),
    ...(memory ? ([[String(standing.length), standing.length === 1 ? "standing approval" : "standing approvals"]] as Figure[]) : []),
    ...(items.length ? ([[`${same} of ${items.length}`, failed ? `readings the same on replay; ${failed} not read again` : "readings the same on replay", same === items.length ? undefined : "att"]] as Figure[]) : []),
  ];
  const lines = brief ? { ...brief, lines: brief.lines.filter((line) => line.tab === "settings") } : undefined;
  return (
    <div className="ks-close-grid">
      <div className="ks-col">
        {view ? (
          <>
            <p className="ks-view-crumbs">
              <Link to={`${base}?tab=settings`}>Settings</Link>
              <span>/</span>
              <b>{views[view]}</b>
            </p>
            <div className="stack">
              {view === "procedures" && <Procedures key={`procedures-${close.id}`} closeId={close.id} entity={close.entity} period={close.period} onApplied={onApplied} open />}
              {view === "memory" && <Memory key={`memory-${close.id}`} closeId={close.id} />}
              {view === "replay" && <Replay key={`replay-${close.id}`} closeId={close.id} />}
              {view === "retention" && <RetentionControls close={close} onDeleted={onDeleted} open />}
            </div>
          </>
        ) : (
          <>
            <NeedsYou key={close.id} closeId={close.id} papers={papers} needs={needs} teammates={0} brief={lines} briefRead={briefRead} decisionsError={decisionsError} base={base} preparing={false} />
            <section className="bare ks-set" aria-labelledby="settings-title">
              <div className="ks-sec">
                <h2 id="settings-title">Settings for {close.entity}</h2>
              </div>
              <div className="ks-table ks-settings" role="table" aria-labelledby="settings-title">
                <div className="ks-tr head" role="row">
                  <span role="columnheader">Setting</span>
                  <span role="columnheader">Now</span>
                  <span role="columnheader">Status</span>
                  <span role="columnheader">
                    <span className="visually-hidden">Open</span>
                  </span>
                </div>
                {rows.map((row) => (
                  <div className="ks-tr linked" role="row" key={row.key}>
                    <span className="ks-who" role="cell">
                      <Link className="ks-rowlink" to={row.to}>
                        {row.label}
                      </Link>
                    </span>
                    <span className="ks-mut" role="cell">
                      {row.now}
                    </span>
                    <span role="cell">
                      <span className={`pill ${row.tone}`}>{row.status}</span>
                    </span>
                    <span role="cell">
                      <Icon name="arrow" className="ks-go" />
                    </span>
                  </div>
                ))}
              </div>
            </section>
          </>
        )}
      </div>
      <aside className="ks-rail" aria-label="About the settings">
        <Glance figures={figures} />
        <AgentPanel close={close} status={status} />
        <MoreLinks
          title="More on Settings"
          links={[
            { label: views.procedures, note: overrides.length ? plural(overrides.length, "override") : undefined, to: to("procedures") },
            { label: views.memory, note: memory ? (names.length ? plural(names.length, "name") : plural(memory.accounts.length, "account")) : undefined, to: to("memory") },
            { label: views.replay, note: items.length ? `${same} of ${items.length}` : undefined, to: to("replay") },
            { label: views.retention, to: to("retention") },
          ]}
        />
      </aside>
    </div>
  );
}
