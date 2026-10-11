import { useState } from "react";
import { Link, useLocation, useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Icon } from "@close/ui/components/Icon";
import { Segmented } from "@close/ui/components/Segmented";
import { workspaceQuery } from "../../data/queries";
import { useDates } from "../../data/time";
import { Evidence, PreparationJobs, type EvidenceFile } from "./Evidence";
import type { Status } from "./Goal";
import { HistoricalExports } from "./HistoricalExports";
import { AgentPanel, Glance, MoreLinks, NeedsYou, type Figure, type Need } from "./Overview";
import type { Brief } from "./Stands";
import { Stripe } from "./Stripe";
import type { Close } from "./WorkpaperView";

// A close's Documents (docs/design-system.md, Close page): how many of the client's documents are read and how many
// wait for the person, then the decisions on this tab and the documents as one table, newest first, each with how it
// arrived and where its preparation stands; the rail counts what is found nowhere else and opens the tab's other views
// (adding, forwarding and comparing documents, historical exports, Stripe reports, saved preparation instructions).

const words = ["no", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten", "eleven", "twelve"];
const capital = (text: string) => text[0].toUpperCase() + text.slice(1);
const w = (n: number) => words[n] ?? String(n);

// Read again on returning to the table (an upload in the add view saves its own copy) and every few seconds while a
// document is still being read or prepared.
const preparingStates = ["waiting_evidence", "interpreting", "queued"];
export const useFiles = (closeId: string) =>
  useQuery({
    ...workspaceQuery<EvidenceFile[]>(`/${closeId}/evidence?checks=1`),
    refetchOnMount: "always",
    refetchOnWindowFocus: true,
    refetchInterval: (query) => ((query.state.data ?? []).some((f) => f.extraction_state === "queued" || f.extraction_state === "processing" || preparingStates.includes(f.job_state ?? "")) ? 5000 : 30_000),
  });
type Forwarding = { configured: boolean; links: { digest: string; expires_at: string; revoked_at: string | null }[] };
const useForwarding = (closeId: string) => useQuery(workspaceQuery<Forwarding>(`/${closeId}/forwarding`));
// The address still open longest, if any: the forwarding address itself is shown only once, when it is issued.
const openUntil = (forwarding: Forwarding | undefined) =>
  forwarding?.links
    .filter((link) => !link.revoked_at && new Date(link.expires_at) > new Date())
    .map((link) => link.expires_at)
    .sort()
    .at(-1);

// The client's documents: current, not a procedure, not a ledger capture (those are Books').
const documentsOf = (files: EvidenceFile[]) => files.filter((f) => !f.superseded_by && f.purpose !== "procedure" && f.arrived !== "quickbooks");

// DocumentsLede says how many documents are read and how many wait for the person; under it whether a forwarding address
// is open, and where documents are added.
export function DocumentsLede({ closeId, base, waiting }: { closeId: string; base: string; waiting: number }) {
  const dates = useDates();
  const read = useFiles(closeId);
  const docs = documentsOf(read.data ?? []);
  const done = docs.filter(agentRead).length;
  const until = openUntil(useForwarding(closeId).data);
  const sentence = !read.data
    ? read.isError
      ? "The documents could not be read."
      : "The client's documents."
    : !docs.length
      ? "No documents yet."
      : `${done === docs.length ? `All ${w(docs.length)}` : `${capital(w(done))} of ${w(docs.length)}`} documents are read${waiting ? `; ${w(waiting)} wait${waiting === 1 ? "s" : ""} for you` : ""}.`;
  return (
    <div className="ks-lede">
      <h1>{sentence}</h1>
      <p>
        {until ? `A forwarding address is open until ${dates.dayMonth(until)}` : "Upload, forward or request the client's documents"}
        {" · "}
        <Link className="ks-link" to={`${base}?tab=evidence&view=add`}>
          {until ? "Forward or upload" : "Add documents"}
        </Link>
      </p>
    </div>
  );
}

const arrivals: Record<string, string> = { upload: "Upload", email: "Email", request: "Client request", reply: "Client reply", answer: "Preparer answer", stripe: "Stripe", procedure: "Procedure" };
// Where a document's preparation stands, in the close's words, and its tone.
function documentStatus(f: EvidenceFile): [string, string] {
  if (f.extraction_state === "failed") return ["Couldn't read", "danger"];
  if (f.extraction_state === "queued" || f.extraction_state === "processing") return ["Preparing · reading", "live"];
  // A reply whose work still waits on the client was received, not yet read: the rest has to arrive first.
  if (f.unread_reply) return ["Received · waiting for the rest", "neutral"];
  switch (f.job_state) {
    case "waiting_evidence":
    case "interpreting":
    case "queued":
      return ["Preparing", "live"];
    case "needs_confirmation":
      return ["Needs your decision", "muted"];
    case "waiting_client":
      return ["Waiting for client", "muted"];
    case "failed":
      return ["Preparation failed", "danger"];
    case "completed":
      return ["Read", ""];
  }
  // Saved but never prepared (a reply recorded beside its request, or work stopped): nothing says the agent read it.
  return ["No preparation yet", "neutral"];
}
// Read means the agent has read it: what waits for a person or the client was read; what is still being read or
// prepared, or could not be, was not.
function agentRead(f: EvidenceFile) {
  return !["Preparing · reading", "Preparing", "Couldn't read", "Preparation failed", "No preparation yet", "Received · waiting for the rest"].includes(documentStatus(f)[0]);
}
// The filter's groups, by the work each document was read as.
const types: [string, string, (kind: string | null | undefined) => boolean][] = [
  ["invoices", "Invoices", (k) => k === "prepaid" || k === "accrual" || k === "deferred_revenue" || k === "depreciation" || k === "owner"],
  ["statements", "Statements", (k) => k === "reconciliation" || k === "loan" || k === "settlement"],
  ["payroll", "Payroll", (k) => k === "payroll"],
  ["other", "Other", (k) => !!k && !["prepaid", "accrual", "deferred_revenue", "depreciation", "owner", "reconciliation", "loan", "settlement", "payroll"].includes(k)],
  ["unread", "Not read yet", (k) => !k],
];

// DocumentsTable is the tab's one table: the client's documents, newest first, with how each arrived and where its
// preparation stands, filtered by what it was read as. Beyond the first few, the rest fold to a line.
function DocumentsTable({ files, base }: { files: EvidenceFile[]; base: string }) {
  const dates = useDates();
  const [filter, setFilter] = useState("all");
  const [all, setAll] = useState(false);
  const docs = documentsOf(files);
  const counts = new Map(types.map(([key, , test]) => [key, docs.filter((f) => test(f.kind)).length]));
  const active = counts.get(filter) ? filter : "all";
  const chosen = active === "all" ? docs : docs.filter((f) => types.find(([key]) => key === active)![2](f.kind));
  const shown = all ? chosen : chosen.slice(0, 6);
  const replaced = files.filter((f) => f.superseded_by).length;
  const captures = files.filter((f) => !f.superseded_by && f.arrived === "quickbooks").length;
  return (
    <section className="bare ks-set" aria-labelledby="documents-title">
      <div className="ks-sec">
        <h2 id="documents-title">Documents</h2>
        <small>newest first</small>
        <div className="ks-end">
          {docs.length > 0 && (
            <Segmented label="Filter documents" items={[{ key: "all", label: "All", count: docs.length }, ...types.filter(([key]) => counts.get(key)).map(([key, label]) => ({ key, label, count: counts.get(key) }))]} value={active} onChange={setFilter} />
          )}
          <Link className="button" to={`${base}?tab=evidence&view=add`}>
            Upload
          </Link>
        </div>
      </div>
      <div className="ks-table ks-docs" role="table" aria-labelledby="documents-title">
        <div className="ks-tr head" role="row">
          <span role="columnheader">Document</span>
          <span role="columnheader">From</span>
          <span role="columnheader">Arrived</span>
          <span role="columnheader">Status</span>
          <span role="columnheader">
            <span className="visually-hidden">Open</span>
          </span>
        </div>
        {shown.map((f) => {
          const [label, tone] = documentStatus(f);
          return (
            <div className="ks-tr linked" role="row" key={f.id}>
              <span className="ks-who" role="cell">
                <Link className="ks-rowlink" to={f.job_id ? `${base}?job=${f.job_id}` : `${base}?tab=evidence&view=add#source-${f.id}`}>
                  {f.name}
                  {f.checks?.pages ? <small> · {f.checks.pages} page{f.checks.pages === 1 ? "" : "s"}</small> : null}
                </Link>
              </span>
              <span className="ks-mut" role="cell">
                {arrivals[f.arrived ?? "upload"]}
              </span>
              <span className="ks-mut" role="cell">
                {f.created_at ? dates.dayMonth(f.created_at) : "—"}
              </span>
              <span role="cell">
                <span className={`pill ${tone}`}>{label}</span>
              </span>
              <span role="cell">
                <Icon name="arrow" className="ks-go" />
              </span>
            </div>
          );
        })}
        {docs.length === 0 && <p className="ks-empty-row">No documents yet. Upload them, forward them to the close's address, or request them from the client.</p>}
      </div>
      {chosen.length > 6 && (
        <p className="ks-table-foot">
          {all ? "" : `${chosen.length - 6} more document${chosen.length - 6 === 1 ? "" : "s"} · `}
          <button type="button" className="ks-link" onClick={() => setAll(!all)}>
            {all ? "Show fewer" : `Show all ${chosen.length}`}
          </button>
        </p>
      )}
      {replaced > 0 && (
        <Link className="ks-fold" to={`${base}?tab=evidence&view=add`}>
          <Icon name="history" />
          {replaced} replaced document{replaced === 1 ? "" : "s"}, kept with what replaced them
          <Icon name="chevron" className="ks-chev" size={14} />
        </Link>
      )}
      {captures > 0 && (
        <Link className="ks-fold" to={`${base}?tab=books&view=ledger`}>
          <Icon name="book" />
          {captures} ledger capture{captures === 1 ? "" : "s"}, on Books
          <Icon name="chevron" className="ks-chev" size={14} />
        </Link>
      )}
    </section>
  );
}

const views: Record<string, string> = {
  add: "Add, forward and compare documents",
  exports: "Historical exports",
  stripe: "Stripe reports",
  instructions: "Saved preparation instructions",
};
// The view a link asks for: by name, or by the anchor of a section inside it.
const documentsView = (asked: string | null, hash: string) => (asked && asked in views ? asked : hash.startsWith("#source-") || hash.startsWith("#forwarded") ? "add" : hash === "#historical-exports" ? "exports" : "");

type Matching = { state: string; documents_not_in_capture?: unknown[] };
type Connection = { data?: { connection: { state: string } | null } };
type StripeRead = { connection: { state: string } | null };
type Drive = { configured: boolean; connected?: boolean };

// Documents is the tab's body: on the left what needs the person here and the documents, or one of the tab's views; on
// the right a few figures, the agent and the tab's other views with each connection's state.
export function Documents({ close, needs, brief, briefRead, decisionsError, status, base, onChange }: { close: Close; needs: Need[]; brief: Brief | undefined; briefRead: { dataUpdatedAt: number; isFetching: boolean; refetch: () => unknown }; decisionsError: string; status: Status | undefined; base: string; onChange: () => void }) {
  const dates = useDates();
  const [params] = useSearchParams();
  const location = useLocation();
  const view = documentsView(params.get("view"), location.hash);
  const files = useFiles(close.id);
  const until = openUntil(useForwarding(close.id).data);
  const matching = useQuery(workspaceQuery<Matching>(`/${close.id}/matching`)).data;
  const quickbooks = useQuery(workspaceQuery<Connection>(`/${close.id}/quickbooks`)).data;
  const stripe = useQuery(workspaceQuery<StripeRead>(`/${close.id}/stripe`)).data;
  const drive = useQuery(workspaceQuery<Drive>(`/me/drive`)).data;
  const instructions = useQuery(workspaceQuery<unknown[]>(`/${close.id}/preparations`)).data;
  const docs = documentsOf(files.data ?? []);
  const pages = docs.reduce((n, f) => n + (f.checks?.pages ?? 0), 0);
  const missing = matching?.state === "ready" ? (matching.documents_not_in_capture?.length ?? 0) : undefined;
  const figures: Figure[] = [
    ...(docs.length ? ([[`${docs.filter(agentRead).length} of ${docs.length}`, "documents read"]] as Figure[]) : []),
    ...(pages ? ([[String(pages), pages === 1 ? "page" : "pages"]] as Figure[]) : []),
    ...(missing !== undefined ? ([[String(missing), missing === 1 ? "invoice not in the ledger yet" : "invoices not in the ledger yet", missing ? "att" : ""]] as Figure[]) : []),
    ...(until ? ([[dates.dayMonth(until), "the forwarding address closes"]] as Figure[]) : []),
  ];
  const state = (value: string | undefined | null) => (value === "active" ? "Connected" : value ? value.replaceAll("_", " ") : "Not connected");
  const to = (name: string) => `${base}?tab=evidence&view=${name}`;
  const lines = brief ? { ...brief, lines: brief.lines.filter((line) => line.tab === "evidence") } : undefined;
  return (
    <div className="ks-close-grid">
      <div className="ks-col">
        {view ? (
          <>
            <p className="ks-view-crumbs">
              <Link to={`${base}?tab=evidence`}>Documents</Link>
              <span>/</span>
              <b>{views[view]}</b>
            </p>
            <div className="stack">
              {view === "add" && <Evidence key={`evidence-${close.id}`} closeId={close.id} onReplace={onChange} />}
              {view === "exports" && <HistoricalExports key={`history-${close.id}`} closeId={close.id} open />}
              {view === "stripe" && <Stripe key={`stripe-${close.id}`} closeId={close.id} entity={close.entity} open />}
              {view === "instructions" && <PreparationJobs key={`jobs-${close.id}`} closeId={close.id} onComplete={onChange} />}
            </div>
          </>
        ) : (
          <>
            <NeedsYou key={close.id} closeId={close.id} papers={[]} needs={needs} teammates={0} brief={lines} briefRead={briefRead} decisionsError={decisionsError} base={base} preparing={false} />
            {files.isError && !files.data ? <p role="alert">{files.error.message || "The documents could not be read."}</p> : <DocumentsTable files={files.data ?? []} base={base} />}
          </>
        )}
      </div>
      <aside className="ks-rail" aria-label="About the documents">
        <Glance figures={figures} />
        <AgentPanel close={close} status={status} />
        <MoreLinks
          title="More on Documents"
          links={[
            { label: views.add, note: until ? `open until ${dates.dayMonth(until)}` : undefined, to: to("add") },
            { label: "QuickBooks", note: quickbooks ? state(quickbooks.data?.connection?.state) : undefined, to: `${base}?tab=books&view=ledger` },
            { label: views.stripe, note: stripe ? state(stripe.connection?.state) : undefined, to: to("stripe") },
            ...(drive?.configured ? [{ label: "Google Drive", note: drive.connected ? "Connected" : "Not connected", to: to("add") }] : []),
            { label: views.instructions, note: instructions?.length ? String(instructions.length) : undefined, to: to("instructions") },
            { label: views.exports, to: to("exports") },
          ]}
        />
      </aside>
    </div>
  );
}
