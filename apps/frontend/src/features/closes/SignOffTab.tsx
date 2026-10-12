import { useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Icon } from "@close/ui/components/Icon";
import { clientPage } from "../../data/clients";
import { workspaceQuery } from "../../data/queries";
import { daysBetween, monthName, useDates, useToday, utcDay } from "../../data/time";
import { EffortCard } from "./Effort";
import type { Status } from "./Goal";
import { OpeningBaseline } from "./OpeningBaseline";
import { AgentPanel, Glance, MoreLinks, NeedsYou, type Figure, type Need } from "./Overview";
import { ProposedStatePreview } from "./Posting";
import { Readiness } from "./Readiness";
import type { Brief } from "./Stands";
import { TakeoverSetup } from "./TakeoverSetup";
import type { Close, Paper } from "./WorkpaperView";
import { workspaceRequest } from "./workspace";

// A close's Sign-off (docs/design-system.md, Close page): what sign-off waits on in one sentence, then the decisions on
// this tab and what sign-off checks as one table, each check with what it found; the rail counts the checks, the drafts,
// the time spent and the due date, and opens the tab's other views (readiness with the scope and the sign-off itself,
// the takeover setup, the ledger rehearsal, time on the close, the review package).

// Readiness as GET /{id}/readiness gives it: the scope, its state and every blocker in words, newest decision first.
type ReadinessRead = {
  scope: { description: string; required_papers: string[]; external_steps: { name: string; complete: boolean; reference: string }[] } | null;
  state: "open" | "signed_off" | "reopen_required";
  blockers: string[];
  history: { id: string; decision: string; note: string; actor: string; created_at: string }[];
  snapshot?: { scope?: { actor: string; created_at: string } | null };
};
const useReadiness = (closeId: string) => useQuery(workspaceQuery<ReadinessRead>(`/${closeId}/readiness`));

const words = ["no", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten", "eleven", "twelve"];
const w = (n: number) => words[n] ?? String(n);
const plural = (n: number, one: string, many = `${one}s`) => `${w(n)} ${n === 1 ? one : many}`;

type Check = { key: string; label: string; detail: string; open: number; phrase: string; to: string };

// The checks sign-off makes, each from the readiness blockers that name it in readiness's own words, and each opening
// where it is settled; a blocker no check names stays under "Other checks", so none is dropped.
function checksOf(r: ReadinessRead, base: string, entity: string): Check[] {
  const taken = new Set<string>();
  const of = (test: (blocker: string) => boolean) => r.blockers.filter((b) => !taken.has(b) && test(b) && taken.add(b));
  const scoped = of((b) => b.startsWith("Define the supported scope"));
  const drafts = of((b) => /^(Required )?[Dd]raft #/.test(b));
  const tie = of((b) => b.startsWith("Tie-out:"));
  const coverage = of((b) => b.startsWith("Coverage for "));
  const steps = of((b) => b.startsWith("External step:"));
  const work = of((b) => b.startsWith("Work #"));
  const takeover = of((b) => /^(Confirm the takeover setup|Review the opening baseline|Resolve \d+ inherited)/.test(b));
  const attachments = of((b) => / client attachments still require /.test(b));
  const profile = of((b) => b.startsWith("Client profile:"));
  // A journal exported but not recorded is recorded in Books; every other file check, a newer ledger export among them,
  // is settled on the client's page.
  const journals = of((b) => b.startsWith("Books from files:") && b.includes(" is exported but not recorded "));
  const files = of((b) => b.startsWith("Books from files:"));
  const capture = of((b) => /^Capture (the QuickBooks month-end|Xero) again/.test(b));
  const other = r.blockers.filter((b) => !taken.has(b));
  const required = r.scope?.required_papers.length ?? 0;
  const external = r.scope?.external_steps ?? [];
  const names = (list: string[], pattern: RegExp) => list.map((b) => b.match(pattern)?.[1] ?? b).join(", ");
  const readiness = `${base}?tab=signoff&view=readiness`;
  return [
    { key: "scope", label: "Scope defined", detail: scoped.length || !r.scope?.description ? "Not defined yet: which drafts and external steps sign-off covers" : r.scope.description, open: scoped.length, phrase: "a scope", to: readiness },
    { key: "drafts", label: "Drafts approved", detail: required ? `${required - drafts.length} of ${required} required drafts approved` : "No drafts in scope yet", open: drafts.length, phrase: plural(drafts.length, "draft"), to: `${base}?tab=workpapers` },
    { key: "tie", label: "Balance sheet ties", detail: tie.length ? names(tie, /^Tie-out: (.+?) is /) : "Every account ties, or its difference is noted on the opening baseline", open: tie.length, phrase: plural(tie.length, "account that differs", "accounts that differ"), to: `${base}?tab=books&view=tie-out` },
    ...(coverage.length ? [{ key: "coverage", label: "Accounts covered", detail: names(coverage, /^Coverage for (.+?)\.?$/), open: coverage.length, phrase: plural(coverage.length, "account without coverage", "accounts without coverage"), to: `${base}#coverage-work` }] : []),
    { key: "steps", label: "External steps", detail: external.length ? external.map((s) => `${s.name}${s.complete && s.reference ? " · done" : ""}`).join(", ") : "None in scope", open: steps.length, phrase: plural(steps.length, "external step"), to: readiness },
    ...(work.length ? [{ key: "work", label: "Work finished", detail: work.join(" "), open: work.length, phrase: plural(work.length, "piece of work", "pieces of work"), to: `${base}?tab=evidence` }] : []),
    // A later month's inherited cleanup is settled on the client's first close, which its page lists.
    ...(takeover.length ? [{ key: "takeover", label: "Takeover setup", detail: takeover.join(" "), open: takeover.length, phrase: "the takeover setup", to: takeover.some((b) => /^(Review the opening baseline on |Resolve \d+ inherited)/.test(b)) ? clientPage(entity) : `${base}?tab=signoff&view=setup` }] : []),
    ...(attachments.length ? [{ key: "attachments", label: "Client attachments", detail: attachments.join(" "), open: attachments.length, phrase: "the client's attachments", to: `${base}#coverage-work` }] : []),
    ...(profile.length ? [{ key: "profile", label: "Client profile", detail: profile.join(" "), open: profile.length, phrase: "the client's profile", to: clientPage(entity) }] : []),
    ...(journals.length ? [{ key: "journals", label: "Journals entered", detail: journals.join(" "), open: journals.length, phrase: plural(journals.length, "journal to record", "journals to record"), to: `${base}?tab=books&view=journals` }] : []),
    ...(files.length ? [{ key: "files", label: "Books from files", detail: files.join(" "), open: files.length, phrase: "the month's files", to: clientPage(entity) }] : []),
    ...(capture.length ? [{ key: "capture", label: "Ledger capture", detail: capture.join(" "), open: capture.length, phrase: "a fresh capture", to: `${base}?tab=books&view=ledger` }] : []),
    ...(other.length ? [{ key: "other", label: "Other checks", detail: other[0] + (other.length > 1 ? ` And ${other.length - 1} more.` : ""), open: other.length, phrase: plural(other.length, "other item"), to: readiness }] : []),
  ];
}

const listed = (phrases: string[]) => (phrases.length < 2 ? phrases.join("") : `${phrases.slice(0, -1).join(", ")} and ${phrases.at(-1)}`);

// SignOffLede is the tab's sentence: signed off, changed since, ready, or what sign-off waits on. Under it: who signed
// off, the scope, how much it covers and whether the reader may sign off.
export function SignOffLede({ close }: { close: Close }) {
  const dates = useDates();
  const read = useReadiness(close.id);
  const r = read.data;
  if (!r)
    return (
      <div className="ks-lede">
        <h1>{read.isError ? "Sign-off readiness could not be read." : "What sign-off checks."}</h1>
      </div>
    );
  const open = checksOf(r, "", close.entity).filter((check) => check.open);
  const signed = r.history[0]?.decision === "signed_off" ? r.history[0] : undefined;
  const sentence =
    r.state === "signed_off"
      ? `${monthName(close.period)} is signed off.`
      : r.state === "reopen_required"
        ? "Work changed after sign-off: reopen the close to sign it off again."
        : open.length
          ? `Sign-off waits on ${listed(open.map((check) => check.phrase))}.`
          : close.can_sign_off
            ? "Ready to sign off."
            : "Ready for a reviewer to sign off.";
  const required = r.scope?.required_papers.length ?? 0;
  const steps = r.scope?.external_steps.length ?? 0;
  const facts = [
    signed ? `signed off by ${signed.actor} ${dates.date(signed.created_at)}` : "",
    r.snapshot?.scope ? `scope set ${dates.date(r.snapshot.scope.created_at)} by ${r.snapshot.scope.actor}` : "no scope yet",
    r.scope ? `${plural(required, "draft")} and ${plural(steps, "external step")}` : "",
    close.can_sign_off ? "you can sign off" : "your role cannot sign off; a reviewer can",
    read.isError ? `not refreshed since ${dates.time(read.dataUpdatedAt)}` : "",
  ].filter(Boolean);
  const line = facts.join(" · ");
  return (
    <div className="ks-lede">
      <h1>{sentence}</h1>
      <p>{line[0].toUpperCase() + line.slice(1)}</p>
    </div>
  );
}

// Checks is the tab's one table: each check, what it found and whether it passes; the scope is changed, and the close
// signed off, in the readiness view.
function Checks({ r, close, base }: { r: ReadinessRead; close: Close; base: string }) {
  const dates = useDates();
  const checks = checksOf(r, base, close.entity);
  const readiness = `${base}?tab=signoff&view=readiness`;
  return (
    <section className="bare ks-set" aria-labelledby="checks-title">
      <div className="ks-sec">
        <h2 id="checks-title">What sign-off checks</h2>
        {r.snapshot?.scope && <small>scope set {dates.dayMonth(r.snapshot.scope.created_at)}</small>}
        <div className="ks-end">
          <Link className="button" to={readiness}>
            {r.state !== "open" ? "Reopen" : r.scope?.description ? "Change the scope" : "Define the scope"}
          </Link>
          {r.state === "open" && r.blockers.length === 0 && close.can_sign_off && (
            <Link className="button primary" to={`${readiness}#signoff-form`}>
              Sign off
            </Link>
          )}
        </div>
      </div>
      <div className="ks-table ks-checks" role="table" aria-labelledby="checks-title">
        <div className="ks-tr head" role="row">
          <span role="columnheader">Check</span>
          <span role="columnheader">Detail</span>
          <span role="columnheader">Status</span>
          <span role="columnheader">
            <span className="visually-hidden">Open</span>
          </span>
        </div>
        {checks.map((check) => (
          <div className="ks-tr linked" role="row" key={check.key}>
            <span className="ks-who" role="cell">
              <Link className="ks-rowlink" to={check.to}>
                {check.label}
              </Link>
            </span>
            <span className="ks-mut" role="cell">
              {check.detail}
            </span>
            <span role="cell">
              <span className={`pill${check.open ? " danger" : ""}`}>{check.open ? `${check.open} open` : "Passes"}</span>
            </span>
            <span role="cell">
              <Icon name="arrow" className="ks-go" />
            </span>
          </div>
        ))}
      </div>
    </section>
  );
}

// Package downloads the close for review: every saved version as a package, the signed binder, and the year-end handoff.
function Package({ close }: { close: Close }) {
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState("");
  async function download(kind: "export" | "binder" | "year-end") {
    setBusy(true);
    setStatus("");
    try {
      const blob = await workspaceRequest(`/${close.id}/${kind}`, undefined, true);
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      const year = close.period.slice(0, 4);
      link.download = kind === "year-end" ? `year-end-handoff-${close.id}-${year}.zip` : `${kind === "binder" ? "accountant-binder" : "close-review"}-${close.id}-${close.period}.zip`;
      link.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
      setStatus(
        kind === "year-end"
          ? `Year-end handoff ready for January through ${close.period}. Open handoff.html for the summary and open-questions.csv for what still needs an answer.`
          : "Package ready. Open review.html for the summary and checksums.sha256 to verify its files.",
      );
    } catch (e) {
      setStatus(e instanceof Error ? e.message : "Export failed. Saved work has not changed.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section>
      <h2>Take the close into review</h2>
      <p>Download every saved version, CSV tables, source references, unresolved items and stored sandbox receipts. The package records the supported scope and sign-off history; unlisted work is not certified.</p>
      <div className="title-actions">
        <button disabled={busy} onClick={() => void download("export")}>
          {busy ? "Preparing package…" : "Download close review package"}
        </button>
        <button disabled={busy} onClick={() => void download("binder")}>
          Download signed accountant binder
        </button>
        <button disabled={busy} onClick={() => void download("year-end")}>
          Download year-end handoff
        </button>
      </div>
      <p>The binder is available only while the current supported scope remains signed off with no blockers. A new source or unresolved action requires fresh review.</p>
      <p>The year-end handoff collects this client's approved schedules, roll-forwards, reconciled balances, adjustments, posting records and open questions from January through this month, for the firm's tax and audit work.</p>
      <p role="status">{status}</p>
    </section>
  );
}

const views: Record<string, string> = { readiness: "Readiness and sign-off", setup: "Takeover setup", rehearsal: "Ledger rehearsal", effort: "Time on this close", package: "Review package and binder" };

// SignOffTab is the tab's body: on the left what needs the person here and what sign-off checks, or one of the tab's
// views; on the right the checks, drafts, time and due date, the agent and the tab's other views.
export function SignOffTab({ close, papers, needs, brief, briefRead, decisionsError, status, base }: { close: Close; papers: Paper[]; needs: Need[]; brief: Brief | undefined; briefRead: { dataUpdatedAt: number; isFetching: boolean; refetch: () => unknown }; decisionsError: string; status: Status | undefined; base: string }) {
  const [params] = useSearchParams();
  const asked = params.get("view");
  const view = asked && asked in views ? asked : "";
  const today = useToday();
  const read = useReadiness(close.id);
  const r = read.data;
  const takeover = useQuery(workspaceQuery<{ takeover: boolean }>(`/${close.id}/takeover-setup`)).data?.takeover;
  const effort = useQuery(workspaceQuery<{ tracked_seconds: number; recorded_minutes: number }>(`/${close.id}/effort`)).data;
  const minutes = effort ? Math.round(effort.tracked_seconds / 60) + effort.recorded_minutes : undefined;
  const checks = r ? checksOf(r, base, close.entity) : [];
  const required = r?.scope?.required_papers.length ?? 0;
  const draftsOpen = checks.find((check) => check.key === "drafts")?.open ?? 0;
  const days = close.due_on ? daysBetween(today, close.due_on) : undefined;
  const figures: Figure[] = [
    ...(r ? ([[`${checks.filter((check) => !check.open).length} of ${checks.length}`, "checks pass"]] as Figure[]) : []),
    ...(required ? ([[`${required - draftsOpen} of ${required}`, "required drafts approved"]] as Figure[]) : []),
    ...(minutes !== undefined ? ([[`${minutes} min`, "time on this close"]] as Figure[]) : []),
    ...(close.due_on && days !== undefined ? ([[utcDay(close.due_on), days < 0 ? `due, ${plural(-days, "day")} overdue` : days === 0 ? "due today" : `due, in ${plural(days, "day")}`, days < 0 ? "att" : undefined]] as Figure[]) : []),
  ];
  const to = (name: string) => `${base}?tab=signoff&view=${name}`;
  const lines = brief ? { ...brief, lines: brief.lines.filter((line) => line.tab === "signoff") } : undefined;
  return (
    <div className="ks-close-grid">
      <div className="ks-col">
        {view ? (
          <>
            <p className="ks-view-crumbs">
              <Link to={`${base}?tab=signoff`}>Sign-off</Link>
              <span>/</span>
              <b>{views[view]}</b>
            </p>
            <div className="stack">
              {view === "readiness" && <Readiness key={`readiness-${close.id}`} closeId={close.id} papers={papers} />}
              {view === "setup" && (
                <>
                  <TakeoverSetup key={`setup-${close.id}`} closeId={close.id} />
                  <OpeningBaseline key={`opening-${close.id}`} closeId={close.id} />
                </>
              )}
              {view === "rehearsal" && <ProposedStatePreview closeId={close.id} />}
              {view === "effort" && <EffortCard closeId={close.id} />}
              {view === "package" && <Package close={close} />}
            </div>
          </>
        ) : (
          <>
            <NeedsYou key={close.id} closeId={close.id} papers={papers} needs={needs} teammates={0} brief={lines} briefRead={briefRead} decisionsError={decisionsError} base={base} preparing={false} />
            {!r ? read.isError ? <p role="alert">{read.error.message || "Sign-off readiness is unavailable."}</p> : <p role="status">Reading what sign-off checks…</p> : <Checks r={r} close={close} base={base} />}
          </>
        )}
      </div>
      <aside className="ks-rail" aria-label="About the sign-off">
        <Glance figures={figures} />
        <AgentPanel close={close} status={status} />
        <MoreLinks
          title="More on Sign-off"
          links={[
            { label: views.readiness, note: r?.history.length ? plural(r.history.length, "decision") : undefined, to: to("readiness") },
            ...(takeover ? [{ label: views.setup, to: to("setup") }] : []),
            { label: views.rehearsal, to: to("rehearsal") },
            { label: views.effort, note: minutes !== undefined ? `${minutes} min` : undefined, to: to("effort") },
            { label: views.package, to: to("package") },
          ]}
        />
      </aside>
    </div>
  );
}
