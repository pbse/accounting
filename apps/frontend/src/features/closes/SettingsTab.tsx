import { Link, useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Icon } from "@close/ui/components/Icon";
import { clientPage, clientPath } from "../../data/clients";
import { workspaceQuery } from "../../data/queries";
import { monthName, monthShort, useDates } from "../../data/time";
import { RetentionControls } from "./Enterprise";
import { dueRule, type Status } from "./Goal";
import { Memory } from "./Memory";
import { AgentPanel, Glance, MoreLinks, NeedsYou, type Figure, type Need } from "./Overview";
import { Procedures } from "./Procedures";
import { Replay } from "./Replay";
import type { Brief } from "./Stands";
import type { Close, Paper } from "./WorkpaperView";
import { amount } from "./workspace";

// A close's Settings (docs/design-system.md, Close page): what is remembered and set for the client, then the decisions
// on this tab and every setting as one table, each with what it is now and what it applies to; the rail counts the
// client's own methods and authority, and opens the tab's views (procedures, what is remembered, the repeatability
// check, retention and deletion).

type Remembered = {
  aliases: { alias: string; confirmed_by: string; retired_at?: string }[];
  standing_approvals: { id: string; kind: string; period: string; granted_by: string; in_force: boolean }[];
  accounts: { kind: string; field: string }[];
};
type Version = { id: string; client_entity: string; kind: string; effective_from?: string };
type Retention = { retain_until: string; legal_hold: boolean; deletion_blockers: string[] };
type Grant = { id: string; connection_id: string; company?: string; realm?: string; tenant_id?: string; kinds: string[]; max_amount: string; expires_at: string; granted_by: string };
// The client as its page reads it (GET /client): its connections, posting grants and roster entry.
type Client = {
  roster: { since: string } | null;
  quickbooks: { id: string; environment: string; state: string; company_name: string; confirmed_at?: string | null }[];
  xero: { id: string; state: string; tenant_name: string; confirmed_at?: string | null }[];
  production_grants?: Grant[];
  xero_grants?: Grant[];
};

const basisNames: Record<string, string> = { cash: "Cash basis", modified_cash: "Modified cash basis", accrual: "Accrual basis" };
const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

// SettingsLede names the client and how its books are kept: the first month a close is owed, the basis, its currency
// and fiscal year once settled (an unsettled profile is the chip below the lede).
export function SettingsLede({ close }: { close: Close }) {
  const since = useQuery(workspaceQuery<Client>(clientPath(close.entity))).data?.roster?.since;
  const profile = close.profile;
  const settled = profile && (profile.state === "confirmed" || profile.state === "detected");
  const facts = [
    since ? `Monthly closes from ${monthName(since)}` : "",
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

type Row = { key: string; label: string; now: string; scope: string; status: string; tone: string; to: string };

const views: Record<string, string> = { procedures: "Procedures", memory: "What Krith remembers", replay: "Repeatability check", retention: "Retention and deletion" };

// SettingsTab is the tab's body: on the left what needs the person here and every setting as one table, or one of the
// tab's views; on the right the client's own methods and authority.
export function SettingsTab({ close, papers, needs, brief, briefRead, decisionsError, status, base, onApplied, onDeleted }: { close: Close; papers: Paper[]; needs: Need[]; brief: Brief | undefined; briefRead: { dataUpdatedAt: number; isFetching: boolean; refetch: () => unknown }; decisionsError: string; status: Status | undefined; base: string; onApplied: () => void; onDeleted: (receipt: string) => void }) {
  const dates = useDates();
  const [params] = useSearchParams();
  const asked = params.get("view");
  const view = asked && asked in views ? asked : "";
  const memoryRead = useQuery(workspaceQuery<Remembered>(`/${close.id}/memory`));
  const memory = memoryRead.data;
  const procedures = useQuery(workspaceQuery<{ versions: Version[] }>("/procedures"));
  const retention = useQuery(workspaceQuery<Retention>(`/${close.id}/retention`));
  const clientRead = useQuery(workspaceQuery<Client>(clientPath(close.entity)));
  const client = clientRead.data;
  const to = (name: string) => `${base}?tab=settings&view=${name}`;
  const currency = close.profile?.currency ?? "USD";
  // The client's own versions in force this month, newest first per kind, each from when it applies; every other kind
  // follows the firm's default.
  const overrides = [
    ...new Map(
      (procedures.data?.versions ?? [])
        .filter((v) => v.client_entity.toLowerCase() === close.entity.toLowerCase() && (!v.effective_from || v.effective_from.slice(0, 7) <= close.period))
        .map((v) => [v.kind, v] as const)
        .reverse(),
    ).values(),
  ];
  const firmDefaults = (procedures.data?.versions ?? []).some((v) => v.client_entity === "");
  const names = (memory?.aliases ?? []).filter((a) => !a.retired_at);
  const standing = memory?.standing_approvals ?? [];
  const inForce = standing.filter((g) => g.in_force);
  const ledger = brief?.lines.find((line) => line.area === "ledger");
  const routine = status?.routine;
  // The connection is who the books are read from; the grant is what may be written, a separate authority.
  const connections = [
    ...(client?.quickbooks ?? []).filter((c) => c.state !== "revoked").map((c) => ({ ledger: "QuickBooks", id: c.id, company: c.company_name, name: `QuickBooks · ${c.company_name || "company not read yet"}${c.environment === "sandbox" ? " (sandbox)" : ""}`, state: c.state, at: c.confirmed_at })),
    ...(client?.xero ?? []).filter((c) => c.state !== "revoked").map((c) => ({ ledger: "Xero", id: c.id, company: c.tenant_name, name: `Xero · ${c.tenant_name || "organisation not chosen yet"}`, state: c.state, at: c.confirmed_at })),
  ];
  // A grant can post only while it is unexpired and its connection is active; the server also checks, at each posting,
  // that its granter is still a member, which this page cannot see.
  const now = Date.now();
  const grants = [...(client?.production_grants ?? []).map((g) => ({ ...g, ledger: "QuickBooks" })), ...(client?.xero_grants ?? []).map((g) => ({ ...g, ledger: "Xero" }))].map((g) => {
    // Each ledger numbers its own connections: a grant is matched within its own.
    const connection = connections.find((c) => c.ledger === g.ledger && c.id === g.connection_id);
    const identity = g.realm ? `realm ${g.realm}` : g.tenant_id ? `organisation ${g.tenant_id}` : "an unnamed company";
    return { ...g, company: g.company || connection?.company || identity, usable: new Date(g.expires_at).getTime() > now && connection?.state === "active" };
  });
  const live = grants.filter((g) => g.usable);
  const unread = (read: { isError: boolean; error: Error | null }) => read.error?.message || "Could not be read.";
  const rows: Row[] = [
    {
      key: "procedures",
      label: "Procedures",
      now: procedures.isError
        ? unread(procedures)
        : !procedures.data
          ? "Reading…"
          : overrides.length
            ? `The client's own for ${overrides.map((v) => `${v.kind}${v.effective_from ? ` from ${monthShort(v.effective_from)}` : ""}`).join(", ")}${firmDefaults ? "; the firm's defaults for the rest" : ""}`
            : firmDefaults
              ? "The firm's defaults, where approved"
              : "No approved procedure yet: the agent works from the facts each draft cites",
      scope: overrides.length ? "This client, over the firm's" : "The firm",
      status: !procedures.data ? "—" : overrides.length ? plural(overrides.length, "override") : firmDefaults ? "Firm defaults" : "None",
      tone: "neutral",
      to: to("procedures"),
    },
    {
      key: "connection",
      label: "Ledger connection",
      now: clientRead.isError ? unread(clientRead) : !client ? "Reading…" : connections.length ? connections.map((c) => `${c.name}${c.at ? `, confirmed ${dates.date(c.at)}` : ""}`).join("; ") : "No ledger connected: the books come from the client's files or captures",
      scope: "This client",
      status: !client ? "—" : !connections.length ? "None" : connections.every((c) => c.state === "active") ? "Connected" : "Needs you",
      tone: !client || !connections.length ? "neutral" : connections.every((c) => c.state === "active") ? "" : "danger",
      to: clientPage(close.entity),
    },
    {
      key: "books",
      label: "Books this month",
      now: ledger?.text ?? "Not read yet",
      scope: "This close",
      status: ledger?.state === "done" ? "Read" : ledger?.state === "attention" ? "Needs you" : ledger?.state === "waiting" ? "Waiting" : "Unknown",
      tone: ledger?.state === "done" ? "" : ledger?.state === "attention" ? "danger" : "neutral",
      to: `${base}?tab=books&view=ledger`,
    },
    {
      key: "posting",
      label: "Posting authority",
      now: clientRead.isError
        ? unread(clientRead)
        : !client
          ? "Reading…"
          : grants.length
            ? `${grants.map((g) => `${g.ledger} · ${g.company}: up to ${amount(g.max_amount, currency)} for ${g.kinds.join(", ")} until ${dates.date(g.expires_at)}, granted by ${g.granted_by}${g.usable ? "" : " (cannot post: expired, or its connection is not active)"}`).join("; ")}. Each posting still needs its own approval.`
            : "None: the agent prepares entries; nothing is written to the ledger",
      scope: "This client's ledger",
      status: !client ? "—" : !grants.length ? "None" : live.length === grants.length ? "Granted" : live.length ? `${live.length} of ${grants.length} usable` : "Not usable",
      tone: "neutral",
      to: clientPage(close.entity),
    },
    {
      key: "names",
      label: "Other names",
      now: memoryRead.isError ? unread(memoryRead) : names.length ? names.map((a) => `${a.alias} (confirmed by ${a.confirmed_by})`).join(", ") : "Only its own name",
      scope: "This client",
      status: names.length ? plural(names.length, "name") : "None",
      tone: "neutral",
      to: to("memory"),
    },
    {
      key: "standing",
      label: "Standing approvals",
      now: memoryRead.isError
        ? unread(memoryRead)
        : standing.length
          ? `${standing.map((g) => `${g.kind} from ${monthShort(g.period)}, granted by ${g.granted_by}${g.in_force ? "" : " (not in force: its approval or granter changed)"}`).join("; ")}. Later months on the same terms are approved automatically; changed terms or a revoked approval end it.`
          : "None: every draft is approved one by one",
      scope: "This client, each for its own schedule",
      status: standing.length ? `${inForce.length} in force` : "None",
      tone: standing.length > inForce.length ? "danger" : "neutral",
      to: to("memory"),
    },
    {
      key: "accounts",
      label: "Past account choices",
      now: memoryRead.isError ? unread(memoryRead) : memory?.accounts.length ? `${plural(memory.accounts.length, "account field")} from approved drafts, offered as suggestions, never applied as rules` : "None yet",
      scope: "This client, the last 12 months",
      status: memory?.accounts.length ? "Suggestions" : "None",
      tone: "neutral",
      to: to("memory"),
    },
    {
      key: "handover",
      label: "Monthly hand-over",
      now: !status ? "Not read yet" : routine ? `Each month is handed to the agent, ${routine.finish === "signoff" ? "to be signed off" : "to be ready for review"}, ${dueRule(routine.due_days)}. Next: ${monthName(routine.next_period)}` : "Off: each month is handed over by hand",
      scope: "Every month of this client",
      status: routine ? "On" : "Off",
      tone: routine ? "" : "neutral",
      to: `${base}#agent-details`,
    },
    {
      key: "retention",
      label: "Retention",
      now: retention.isError ? unread(retention) : !retention.data ? "Reading…" : `${retention.data.retain_until ? `Kept until ${dates.calendarDate(retention.data.retain_until)}` : "No retention date recorded"}${retention.data.legal_hold ? " · legal hold" : " · no legal hold"}`,
      scope: "This close; no firm-wide policy",
      status: !retention.data ? "—" : retention.data.legal_hold ? "Legal hold" : retention.data.deletion_blockers.length ? "Kept" : "No hold",
      tone: retention.data?.legal_hold ? "info" : "neutral",
      to: to("retention"),
    },
  ];
  const figures: Figure[] = [
    ...(procedures.data ? ([[String(overrides.length), overrides.length === 1 ? "client procedure override" : "client procedure overrides"]] as Figure[]) : []),
    ...(memory ? ([[String(inForce.length), inForce.length === 1 ? "standing approval in force" : "standing approvals in force"]] as Figure[]) : []),
    ...(client ? ([[String(live.length), live.length === 1 ? "posting grant that can post" : "posting grants that can post"]] as Figure[]) : []),
    ...(client?.roster?.since ? ([[monthShort(client.roster.since), "monthly closes from"]] as Figure[]) : []),
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
              {view === "replay" && (
                <>
                  <p className="note">Replaying confirmed readings shows whether the current agent reads them the same way again. It checks repeatability, not accuracy: a reading can repeat and still be wrong.</p>
                  <Replay key={`replay-${close.id}`} closeId={close.id} />
                </>
              )}
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
                  <span role="columnheader">Applies to</span>
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
                    <span className="ks-mut" role="cell">
                      {row.scope}
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
            { label: views.replay, to: to("replay") },
            { label: views.retention, to: to("retention") },
            { label: "Connections and posting grants", to: clientPage(close.entity) },
          ]}
        />
      </aside>
    </div>
  );
}
