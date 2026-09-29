import { type ReactNode, useEffect, useId, useRef, useState } from "react";
import { Link } from "react-router";
import { askAssistant } from "../dashboard/assistantStore";
import { Avatar, Icon } from "../dashboard/Icon";
import { workspaceRequest } from "./workspace";

type Item = { job_id: string; name: string; status: "prepared" | "needs_you" | "client" | "working" | "unsupported" | "stopped"; text: string; drafts: string[]; handable?: boolean };
type Handoff = { id: string; job_id: string; account_id?: string; name: string; note: string; by: string; at: string; mine: boolean; handed: string };
type Account = { account_id: string; name: string; state: string };
type Person = { subject: string; name: string };
type Finish = "review" | "signoff";
type Goal = { id: string; author: string; due_on: string; state: "active" | "reached" | "stopped"; finish: Finish; created_at: string; reached_at: string | null; ready_at: string | null; finished_at: string | null; events: { actor: string; text: string; at: string }[] };
type Signoff = { state: "open" | "signed_off" | "reopen_required"; signer: string; needs: string[]; entries: { draft: string; text: string }[]; next_close: string };
type Routine = { author: string; finish: Finish; due_days: number | null; next_period: string; waiting: string };
type Step = { key: string; title: string; state: "done" | "agent" | "you" | "client" | "waits" | "none" | "unknown"; text: string; lines?: string[]; link?: string };
type Status = {
  goal: Goal | null;
  progress: { items: Item[]; needs: string[]; ready: boolean; signoff?: Signoff; coverage?: { total: number; open: number }; plan: Step[] };
  activity: { at: string; actor: string; text: string }[];
  activity_total: number;
  signed_off: boolean;
  routine: Routine | null;
  close: { entity: string; period: string };
  handoffs: Handoff[];
};

const sections: [Item["status"], string][] = [
  ["needs_you", "Needs you"],
  ["unsupported", "I can't prepare"],
  ["client", "Waiting on the client"],
  ["working", "I'm working on"],
  ["prepared", "Ready for review"],
  ["stopped", "Left out"],
];
const handOver = "Get this close ready for review";
const stepWords: Record<Step["state"], [string, string]> = {
  done: ["status ok", "Done"],
  agent: ["status info", "I'm on it"],
  you: ["status warn", "Needs you"],
  client: ["status info", "Waiting on the client"],
  waits: ["status info", "Waits"],
  none: ["status info", "Note"],
  unknown: ["status danger", "Not checked"],
};

// Plan lists, in order, what getting the close to its finish takes and who each step is with.
function Plan({ plan }: { plan: Step[] }) {
  return (
    <ol aria-label="Plan">
      {plan.map((s) => (
        <li key={s.key}>
          <span className={stepWords[s.state][0]}>{stepWords[s.state][1]}</span> <strong>{s.title}</strong>: {s.text} {s.link && <Link to={s.link}>Open →</Link>}
          {!!s.lines?.length && (
            <ul>
              {s.lines.map((l) => (
                <li key={l}>{l}</li>
              ))}
            </ul>
          )}
        </li>
      ))}
    </ol>
  );
}
const finishes: Record<Finish, string> = { review: "ready for review", signoff: "signed off" };

export function localDate(d = new Date()) {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

function due(on: string) {
  const days = Math.round((Date.parse(on) - Date.parse(localDate())) / 86_400_000);
  const day = new Date(`${on}T00:00:00`).toLocaleDateString(undefined, { weekday: "short", day: "numeric", month: "short" });
  if (days < 0) return `Due ${day}, ${-days} day${days === -1 ? "" : "s"} overdue`;
  return days === 0 ? "Due today" : days === 1 ? "Due tomorrow" : `Due ${day}, in ${days} days`;
}

function monthName(period: string) {
  return new Date(`${period}-01T00:00:00`).toLocaleDateString(undefined, { month: "long", year: "numeric" });
}

// dueRule says how a monthly hand-over dates each month.
function dueRule(days: number | null) {
  return days ? `due ${days} day${days === 1 ? "" : "s"} after each month ends` : "with no due date";
}

// HandOff hands a document or an account that needs a person to a teammate who can review the client's work, or says
// who has it.
function HandOff({ closeId, target, handable, handoff, refresh }: { closeId: string; target: { job_id: string } | { account_id: string }; handable: boolean; handoff?: Handoff; refresh: () => void }) {
  const [people, setPeople] = useState<Person[]>();
  const [to, setTo] = useState("");
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function send(path: string, body: object) {
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(path, body);
      setNote("");
      refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : "That did not go through. Refresh and try again.");
    } finally {
      setBusy(false);
    }
  }
  if (handoff)
    return (
      <>
        {" "}
        · With {handoff.mine ? "you" : handoff.name} since {new Date(handoff.at).toLocaleDateString()}
        {handoff.note && <> · “{handoff.note}”</>}{" "}
        <button type="button" disabled={busy} onClick={() => void send(`/${closeId}/goal/handoffs/${handoff.id}/take-back`, {})}>
          {handoff.mine ? "Hand it back" : "Take it back"}
        </button>
        {error && <span role="alert"> {error}</span>}
      </>
    );
  if (!handable) return null;
  const document = "job_id" in target;
  function load(open: boolean) {
    if (!open || people) return;
    workspaceRequest(`/${closeId}/people`)
      .then((data: { people: Person[] }) => setPeople(data.people))
      .catch((e) => setError(e instanceof Error ? e.message : "Teammates are unavailable right now."));
  }
  return (
    <details onToggle={(e) => load(e.currentTarget.open)}>
      <summary>Hand to a teammate</summary>
      {people?.length === 0 && <p className="assistant-note">Nobody else here can review this client's work.</p>}
      {!!people?.length && (
        <>
          <label>
            Teammate{" "}
            <select value={to} onChange={(e) => setTo(e.target.value)}>
              <option value="">Choose…</option>
              {people.map((p) => (
                <option key={p.subject} value={p.subject}>
                  {p.name}
                </option>
              ))}
            </select>
          </label>
          <label>
            What should they look into?
            <textarea value={note} maxLength={500} onChange={(e) => setNote(e.target.value)} />
          </label>
          <button type="button" disabled={busy || !to} onClick={() => void send(`/${closeId}/goal/handoffs`, { ...target, assignee: to, note: note.trim() })}>
            {busy ? "Handing over…" : "Hand it over"}
          </button>
          <p className="assistant-note">
            They'll find it in their attention list and act on it in {document ? "Work" : "Coverage"}. I'll close the hand-off once {document ? "the document" : "the account's coverage"} no longer needs anyone.
          </p>
        </>
      )}
      {error && <p role="alert">{error}</p>}
    </details>
  );
}

const accountStates: Record<string, string> = { missing_evidence: "needs evidence", awaiting_work: "waits on its work", needs_review: "needs review", source_changed: "recapture first" };

// AccountHandOffs lists the capture's accounts whose coverage still needs a person, read each time it is opened, so one
// can be handed to a teammate; a stale capture's accounts are recaptured first. Accounts already handed stay listed,
// so they can be taken back, even when coverage cannot be read.
function AccountHandOffs({ closeId, status, refresh }: { closeId: string; status: Status; refresh: () => void }) {
  const [accounts, setAccounts] = useState<Account[]>();
  const [search, setSearch] = useState("");
  const [error, setError] = useState("");
  function load() {
    setError("");
    workspaceRequest(`/${closeId}/coverage`)
      .then((data: { coverage: { items: Account[] } }) => setAccounts(data.coverage.items.filter((a) => a.state !== "resolved" && a.state !== "excluded")))
      .catch((e) => setError(e instanceof Error ? e.message : "Coverage is unavailable."));
  }
  const handed = status.handoffs.filter((h) => h.account_id);
  const matching = accounts?.filter((a) => !handed.some((h) => h.account_id === a.account_id) && `${a.name} ${a.account_id}`.toLowerCase().includes(search.toLowerCase().trim())) ?? [];
  return (
    <details onToggle={(e) => e.currentTarget.open && load()}>
      <summary>
        Hand an account to a teammate{handed.length > 0 && ` · ${handed.length} with teammates`}
      </summary>
      <button type="button" onClick={load}>
        Refresh
      </button>
      {accounts && accounts.length > 50 && (
        <label>
          Find an account <input type="search" value={search} onChange={(e) => setSearch(e.target.value)} />
        </label>
      )}
      <ul>
        {handed.map((h) => (
          <li key={h.id}>
            {h.handed}
            <HandOff closeId={closeId} target={{ account_id: h.account_id ?? "" }} handable={false} handoff={h} refresh={refresh} />
          </li>
        ))}
        {matching.slice(0, 50).map((a) => (
          <li key={a.account_id}>
            {a.name} · {accountStates[a.state] ?? a.state.replaceAll("_", " ")}
            <HandOff closeId={closeId} target={{ account_id: a.account_id }} handable={a.state !== "source_changed"} refresh={refresh} />
          </li>
        ))}
      </ul>
      {matching.length > 50 && <p className="assistant-note">Showing 50 of {matching.length}; search to find the rest.</p>}
      {error && <p role="alert">{error}</p>}
    </details>
  );
}

// RoutineLine shows the client's monthly hand-over, why it is waiting, and a way to stop it.
function RoutineLine({ closeId, status, refresh }: { closeId: string; status: Status; refresh: () => void }) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const routine = status.routine;
  if (!routine) return null;
  async function stop() {
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(`/${closeId}/routine/stop`, { reason: reason.trim() });
      setReason("");
      refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The monthly hand-over could not be stopped.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <div>
      <p className="assistant-note">
        Every new month of {status.close.entity} is handed to the agent, {finishes[routine.finish]} {dueRule(routine.due_days)}, set up by {routine.author}. Next: {monthName(routine.next_period)}, once it ends.
      </p>
      {routine.waiting && <p role="status">{routine.waiting}</p>}
      <details>
        <summary>Stop the monthly hand-over</summary>
        <label>
          Reason
          <textarea value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
        </label>
        <button type="button" disabled={busy || !reason.trim()} onClick={() => void stop()}>
          {busy ? "Stopping…" : "Stop"}
        </button>
        <p className="assistant-note">Months already handed over keep their goals.</p>
      </details>
      {error && <p role="alert">{error}</p>}
    </div>
  );
}

// headline says, first, where the goal stands, from the live progress where the stored goal may lag a sweep behind.
function headline(goal: Goal, progress: Status["progress"], needs: number) {
  const signoff = progress.signoff;
  if (goal.state === "stopped") return goal.events[goal.events.length - 1]?.text;
  if (signoff?.state === "reopen_required") return "Something changed after the sign-off. Reopen the close on its sign-off tab to continue.";
  if (signoff?.state === "signed_off") return `Signed off by ${signoff.signer}.`;
  if (goal.finish === "review" && goal.state === "reached") return "Every uploaded document has a draft ready for your review.";
  if (needs) return `${needs} thing${needs === 1 ? " needs" : "s need"} you before this is ${finishes[goal.finish]}.`;
  if (signoff?.state === "open" && progress.ready) return "Ready to sign off.";
  return `I'm getting this close ${finishes[goal.finish]}.`;
}

// useGoal reads the close's goal and live progress, polling while shown. The plan's checks are heavier, so they are read
// about once a minute and kept between polls.
function useGoal(closeId: string, live: boolean, every: number) {
  const [status, setStatus] = useState<Status>();
  const [error, setError] = useState("");
  const [asked, setAsked] = useState(0);
  const checked = useRef<{ close: string; at: number; step?: Step }>({ close: "", at: 0 });
  useEffect(() => {
    if (!live) return;
    let on = true;
    let timer: ReturnType<typeof setTimeout>;
    const poll = () => {
      const verify = checked.current.close !== closeId || Date.now() - checked.current.at > 60_000;
      return workspaceRequest(`/${closeId}/goal${verify ? "?verify=1" : ""}`)
        .then((data: Status) => {
          if (!on) return;
          const step = data.progress.plan.find((s) => s.key === "verify");
          if (verify) checked.current = { close: closeId, at: Date.now(), step };
          else if (checked.current.step) data.progress.plan = data.progress.plan.map((s) => (s.key === "verify" ? (checked.current.step as Step) : s));
          setStatus(data);
          setError("");
        })
        .catch((e) => on && setError(e instanceof Error ? e.message : "The agent's progress is unavailable."))
        .finally(() => on && (timer = setTimeout(poll, every)));
    };
    void poll();
    return () => {
      on = false;
      clearTimeout(timer);
    };
  }, [closeId, live, asked]);
  return { status, error, refresh: () => setAsked((n) => n + 1) };
}

function Progress({ closeId, status, refresh }: { closeId: string; status: Status; refresh: () => void }) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [all, setAll] = useState<{ goal: string; activity: Status["activity"] }>();
  const { goal, progress, activity, activity_total: total } = status;
  if (!goal) return null;
  // The whole history is read on request and shown only while it is still the whole, so the newest steps always show.
  const full = all?.goal === goal.id && all.activity.length === total ? all.activity : undefined;
  const stopped = goal.state === "stopped";
  const signoff = goal.finish === "signoff" ? progress.signoff : undefined;
  const coverage = progress.coverage?.total ? progress.coverage : undefined;
  // Sign-off needs every captured account covered or excluded, one decision the card counts once.
  const needs = progress.items.filter((i) => i.status === "needs_you" || i.status === "unsupported").length + progress.needs.length + (signoff?.needs.length ?? 0) + (goal.finish === "signoff" && coverage?.open ? 1 : 0);
  const signoffTab = `/dashboard/closes/${closeId}?tab=signoff`;
  async function stop() {
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(`/${closeId}/goal/stop`, { reason: reason.trim() });
      setReason("");
      refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The agent could not be stopped.");
    } finally {
      setBusy(false);
    }
  }
  const link = (item: Item) => (item.drafts[0] ? `/dashboard/closes/${closeId}/workpapers/${item.drafts[0]}` : `/dashboard/closes/${closeId}?job=${item.job_id}`);
  return (
    <>
      <p>
        <strong>{headline(goal, progress, needs)}</strong>
      </p>
      <p className="assistant-note">
        Handed over by {goal.author} · {new Date(goal.created_at).toLocaleDateString()}
        {goal.due_on && !stopped ? ` · ${due(goal.due_on)}` : ""}
      </p>
      {!stopped && (
        <>
          <Plan plan={progress.plan} />
          {(!!coverage?.open || status.handoffs.some((h) => h.account_id)) && <AccountHandOffs closeId={closeId} status={status} refresh={refresh} />}
          {sections.map(([key, title]) => {
            const items = progress.items.filter((i) => i.status === key);
            return (
              items.length > 0 && (
                <div key={key}>
                  <strong>
                    {title} · {items.length}
                  </strong>
                  <ul>
                    {items.map((i) => (
                      <li key={i.job_id}>
                        <Link to={link(i)}>{i.name}</Link>: {i.text}
                        <HandOff closeId={closeId} target={{ job_id: i.job_id }} handable={!!i.handable} handoff={status.handoffs.find((h) => h.job_id === i.job_id)} refresh={refresh} />
                      </li>
                    ))}
                  </ul>
                </div>
              )
            );
          })}
          {signoff && (
            <div>
              <strong>Sign-off</strong>
              <ul>
                {signoff.needs.map((n) => (
                  <li key={n}>
                    {n} <Link to={signoffTab}>Open sign-off →</Link>
                  </li>
                ))}
                {signoff.entries.map((e, n) => (
                  <li key={n}>
                    <Link to={`/dashboard/closes/${closeId}/workpapers/${e.draft}`}>Entry for draft #{e.draft}</Link>: {e.text}
                  </li>
                ))}
                {signoff.state === "signed_off" && (
                  <li>
                    <Link to={signoffTab}>Download the signed binder →</Link>
                  </li>
                )}
                {!needs && signoff.state === "open" && progress.ready && (
                  <li>
                    <Link to={signoffTab}>Sign off the close →</Link> (needs close:signoff)
                  </li>
                )}
                {signoff.next_close && (
                  <li>
                    <Link to={`/dashboard/closes/${signoff.next_close}`}>Next month's close →</Link>
                  </li>
                )}
              </ul>
            </div>
          )}
        </>
      )}
      <RoutineLine closeId={closeId} status={status} refresh={refresh} />
      {total > 0 && (
        <details>
          <summary>What happened · {total}</summary>
          <ol>
            {(full ?? activity).map((a, n) => (
              <li key={n}>
                {new Date(a.at).toLocaleString()} · {a.text}
              </li>
            ))}
          </ol>
          {!full && total > activity.length && (
            <button type="button" onClick={() => void workspaceRequest(`/${closeId}/goal?activity=all`).then((d: Status) => d.goal && setAll({ goal: d.goal.id, activity: d.activity }), () => setError("The full history could not be read."))}>
              Show all {total}
            </button>
          )}
        </details>
      )}
      {stopped ? (
        !status.signed_off && (
          <button type="button" onClick={() => askAssistant(handOver, closeId)}>
            Hand it over again
          </button>
        )
      ) : (
        <details>
          <summary>Stop the agent</summary>
          <label>
            Reason
            <textarea value={reason} maxLength={500} onChange={(e) => setReason(e.target.value)} />
          </label>
          <button type="button" disabled={busy || !reason.trim()} onClick={() => void stop()}>
            {busy ? "Stopping…" : "Stop"}
          </button>
          <p className="assistant-note">Work already started keeps running; stopping only takes the close back from the agent.</p>
        </details>
      )}
      {error && <p role="alert">{error}</p>}
    </>
  );
}

function Proposal({ status, dueOn, proposed, everyMonth, onConfirm }: { status: Status; dueOn: string; proposed: string; everyMonth: boolean; onConfirm: (body: { due_on: string; finish: Finish; every_month: boolean }) => Promise<void> }) {
  const [date, setDate] = useState(dueOn);
  // A request that asked for every month starts with the monthly hand-over ticked; the person still confirms it.
  const [monthly, setMonthly] = useState(everyMonth && !status.routine);
  const [finish, setFinish] = useState<Finish>(proposed === "signoff" ? "signoff" : "review");
  const group = useId();
  // A monthly hand-over keeps the due date's distance after this month's end; one inside the month has no date.
  // It starts with the month after this close, or with the current month when this close's month ended earlier.
  const [year, month] = status.close.period.split("-").map(Number);
  const monthlyDays = date ? Math.round((Date.parse(date) - Date.UTC(year, month, 0)) / 86_400_000) : 0;
  const after = new Date(Date.UTC(year, month, 1)).toISOString().slice(0, 7);
  const current = new Date().toISOString().slice(0, 7);
  const nextPeriod = after > current ? after : current;
  const [agreed, setAgreed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function confirm() {
    setBusy(true);
    setError("");
    try {
      await onConfirm({ due_on: date, finish, every_month: monthly && !status.routine });
    } catch (e) {
      setError(e instanceof Error ? e.message : "The close could not be handed over.");
      setAgreed(false);
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <fieldset>
        <legend>Finish line</legend>
        <label>
          <input type="radio" name={group} checked={finish === "review"} onChange={() => setFinish("review")} /> Every document has a draft ready for review
        </label>
        <label>
          <input type="radio" name={group} checked={finish === "signoff"} onChange={() => setFinish("signoff")} /> The close is signed off
        </label>
      </fieldset>
      <p>Here's my plan to get this close {finishes[finish]}, and where each step stands now. I keep it moving, bring new documents and client replies in as they arrive, and tell you exactly what I need from you.</p>
      <Plan plan={finish === "signoff" ? [...status.progress.plan, { key: "signoff", title: "Sign off", state: "waits", text: "Then I line up what sign-off needs: approvals, its scope and each entry to post." }] : status.progress.plan} />
      <p>I never approve, send, post or sign off anything, and you can stop me at any time.</p>
      <label>
        {finish === "signoff" ? "Signed off by" : "Ready for review by"} <input type="date" value={date} min={localDate()} onChange={(e) => setDate(e.target.value)} />
      </label>
      {status.routine ? (
        <p className="assistant-note">Every new month of {status.close.entity} is already handed over, set up by {status.routine.author}.</p>
      ) : (
        <>
          <label>
            <input type="checkbox" checked={monthly} onChange={(e) => setMonthly(e.target.checked)} /> Do this every month for {status.close.entity}
          </label>
          {monthly && (
            <p className={monthlyDays > 60 ? undefined : "assistant-note"} role={monthlyDays > 60 ? "alert" : undefined}>
              {monthlyDays > 60
                ? "For a monthly hand-over, choose a due date at most 60 days after the month ends."
                : `Starting with ${monthName(nextPeriod)}, when each month ends I'll open that month's close for ${status.close.entity} and hand it over the same way, ${finishes[finish]} ${dueRule(monthlyDays > 0 ? monthlyDays : null)}.`}
            </p>
          )}
        </>
      )}
      <label>
        <input type="checkbox" checked={agreed} onChange={(e) => setAgreed(e.target.checked)} /> Let the agent work on this close until it's {finishes[finish]}.
      </label>
      <button type="button" className="primary" disabled={busy || !agreed || (monthly && monthlyDays > 60)} onClick={() => void confirm()}>
        {busy ? "Handing over…" : "Hand it over"}
      </button>
      <p className="assistant-note">Handing over needs the workpapers:review permission.</p>
      {error && <p role="alert">{error}</p>}
    </>
  );
}

type Delegated = {
  id: string;
  close_id: string;
  entity: string;
  period: string;
  finish: Finish;
  due_on: string;
  state: Goal["state"];
  author: string;
  handed: number;
  summary: { needs?: number; ready?: boolean; signoff?: string; as_of?: string; books?: { state: string; text: string; read_at?: string }; coverage?: { total: number; open: number } | null; client?: number };
};

// delegatedState words where a delegated close stands, from what the agent last found, as its card does.
function delegatedState(g: Delegated): [string, string] {
  const s = g.summary;
  if (!s.as_of) return ["status info", "Updating…"];
  if (s.signoff === "reopen_required") return ["status warn", "Needs reopening"];
  if (s.signoff === "signed_off") return ["status ok", "Signed off"];
  if (g.finish === "review" && g.state === "reached") return ["status ok", "Ready for review"];
  if (s.needs) return ["status warn", `${s.needs} thing${s.needs === 1 ? " needs" : "s need"} a person`];
  if (s.books?.state === "you") return ["status warn", "The books need a person"];
  if (s.signoff === "open" && s.ready) return ["status warn", "Ready to sign off"];
  if (s.client) return ["status info", "Waiting on the client"];
  return ["status info", "The agent is working"];
}

// delegatedFacts says when the books were last read (or what reading them waits on), the accounts' coverage and what
// waits on the client, from what the agent last found.
function delegatedFacts(s: Delegated["summary"]) {
  const facts: string[] = [];
  if (s.books?.state === "done" && s.books.read_at) facts.push(`Books read ${new Date(s.books.read_at).toLocaleDateString(undefined, { day: "numeric", month: "short" })}`);
  else if (s.books?.state === "none") facts.push("No ledger connected: uploaded documents only");
  else if (s.books) facts.push(s.books.text);
  if (s.coverage) facts.push(s.coverage.open ? `${s.coverage.open} of ${s.coverage.total} accounts need coverage` : `All ${s.coverage.total} accounts covered`);
  if (s.client) facts.push(`${s.client} waiting on the client`);
  return facts.join(" · ");
}

// DelegatedCloses is Work's list of every close handed to the agent that you can see, those needing a person first.
export function DelegatedCloses({ refresh, client }: { refresh: number; client: string }) {
  const [list, setList] = useState<{ goals: Delegated[]; more: boolean }>();
  const [error, setError] = useState("");
  // The agent updates each close about once a minute, so the list reads it again as often.
  useEffect(() => {
    let live = true;
    let timer: ReturnType<typeof setTimeout>;
    const load = () =>
      workspaceRequest(`/goals?client=${encodeURIComponent(client)}`)
        .then((data: { goals: Delegated[]; more: boolean }) => {
          if (!live) return;
          setList(data);
          setError("");
        })
        .catch((e) => live && setError(e instanceof Error ? e.message : "Delegated closes are unavailable."))
        .finally(() => live && (timer = setTimeout(load, 60_000)));
    void load();
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [refresh, client]);
  if (error) return <p role="alert">{error}</p>;
  if (!list?.goals.length) return null;
  return (
    <section aria-label="Handed to the agent">
      <div className="section-title">
        <h2>Handed to the agent</h2>
        {list.more && <small>Showing {list.goals.length}. Filter by client or month to see the rest.</small>}
      </div>
      <div className="rows">
        {list.goals.map((g) => {
          const [tone, state] = delegatedState(g);
          // Progress older than ten minutes says when the agent last looked, so it is not taken as current.
          const asOf = g.summary.as_of ? new Date(g.summary.as_of) : undefined;
          const stale = asOf && Date.now() - asOf.getTime() > 600_000 ? asOf : undefined;
          return (
            <Link className="row" key={g.id} to={`/dashboard/closes/${g.close_id}`}>
              <span className="row-main">
                <Avatar label={g.entity} />
                <span>
                  <strong>
                    {g.entity} · {monthName(g.period)}
                  </strong>
                  <small>
                    To get it {finishes[g.finish]}
                    {g.due_on && ` by ${new Date(`${g.due_on}T00:00:00`).toLocaleDateString(undefined, { weekday: "short", day: "numeric", month: "short" })}`} · handed over by {g.author}
                    {g.handed > 0 && ` · ${g.handed} with teammates`}
                  </small>
                  {delegatedFacts(g.summary) && <small>{delegatedFacts(g.summary)}</small>}
                </span>
              </span>
              <span className={tone}>
                {state}
                {stale && ` · as of ${stale.toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" })}`}
              </span>
              <Icon name="arrow" size={16} />
            </Link>
          );
        })}
      </div>
    </section>
  );
}

// GoalCard is the assistant's card for handing its close to the agent: the proposal, then the goal's live progress.
export function GoalCard({ closeId, dueOn, finish, everyMonth = false, outcome, live, onConfirm }: { closeId: string; dueOn: string; finish: string; everyMonth?: boolean; outcome?: { at: string } | null; live: boolean; onConfirm: (body: { due_on: string; finish: Finish; every_month: boolean }) => Promise<void> }) {
  const { status, error, refresh } = useGoal(closeId, live, 5000);
  const card = (body: ReactNode) => (
    <div className="assistant-card">
      <strong>Hand this close to the agent</strong>
      {body}
      {error && <p role="alert">{error}</p>}
    </div>
  );
  if (!live) return card(<p>{outcome ? `Handed over ${new Date(outcome.at).toLocaleString()}. The newest card shows its progress.` : "A newer card replaced this one."}</p>);
  if (!status) return card(!error && <p role="status">Loading the close…</p>);
  if (outcome || (status.goal && status.goal.state !== "stopped")) return card(<Progress closeId={closeId} status={status} refresh={refresh} />);
  if (status.signed_off) return card(<p>This close is signed off, so there is nothing left for me to prepare.</p>);
  return card(<Proposal status={status} dueOn={dueOn} proposed={finish} everyMonth={everyMonth} onConfirm={onConfirm} />);
}

// GoalPanel shows the close's goal at the top of its Overview, or offers to hand the close to the agent.
export function GoalPanel({ closeId }: { closeId: string }) {
  const { status, error, refresh } = useGoal(closeId, true, 10000);
  if (!status) return error ? <p role="alert">{error}</p> : null;
  if (!status.goal && status.signed_off && !status.routine) return null;
  return (
    <section className="panel" aria-labelledby="goal-title">
      <h2 id="goal-title">{status.goal && status.goal.state !== "stopped" ? "Handed to the agent" : status.signed_off ? "Monthly hand-over" : "Hand this close to the agent"}</h2>
      {status.goal ? (
        <Progress closeId={closeId} status={status} refresh={refresh} />
      ) : (
        <>
          {!status.signed_off && (
            <p>
              The agent keeps preparation moving and tells you only what needs you.{" "}
              <button type="button" onClick={() => askAssistant(handOver, closeId)}>
                Hand it over
              </button>
            </p>
          )}
          <RoutineLine closeId={closeId} status={status} refresh={refresh} />
        </>
      )}
      {error && <p role="alert">{error}</p>}
    </section>
  );
}
