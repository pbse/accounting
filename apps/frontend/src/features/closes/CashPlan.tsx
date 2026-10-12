import { cashPlanQuery } from "../../data/cashPlan";
import { useQuery } from "@tanstack/react-query";
import { useDates } from "../../data/time";
import { type FormEvent, useEffect, useState } from "react";
import { amount, workspaceRequest } from "./workspace";
import { SectionHeader } from "@close/ui/components/SectionHeader";
import { CashObservations } from "./CashObservations";

export type Item = { key?: string; on: string; amount: string; label: string; source: string; basis: string };
type Change = { kind: "move" | "add"; item?: string; on?: string; label?: string; direction?: string; amount?: string; first_on?: string; every?: string; until_on?: string };
export type Scenario = { id: string; name: string; changes: Change[] };
export type Week = { start: string; end: string; opening: string; receipts: string; disbursements: string; closing: string; lowest: string; lowest_on: string; items: Item[] };
export type Obligation = {
  id: string;
  name: string;
  category: string;
  direction: "in" | "out";
  amount: string;
  currency: string;
  first_on: string;
  every: string;
  until_on: string;
  vendor: string;
  basis: string;
  created_by: string;
  approved_by: string;
  approved_at: string | null;
  retired_at: string | null;
  retire_reason: string;
};
export type Plan =
  | { state: "waiting"; reason: string }
  | {
      state: "ready";
      entity: string;
      currency: string;
      start: string;
      opening: { amount: string; as_of: string; stale: boolean; accounts: { name: string; balance: string }[]; left_out: string[] };
      open_items: { captured_at: string; capture_id: string; complete: boolean; completeness?: string; hours_apart: number; through: string; missing_later: boolean };
      weeks: Week[];
      lowest: { amount: string; on: string };
      past_due: Item[];
      after_horizon: Item[];
      unscheduled: { label: string; amount: string; reason: string }[];
      other_currency: string[];
      horizon: { start: string; end: string; weeks: number };
      readiness: { state: "complete" | "draft" | "incomplete"; unresolved: { kind: "source" | "decision"; text: string; amount?: string }[] };
      matches: Matched[];
      questions: Question[];
      lapsed: { id: string; obligation_id: string; on: string; document: string; label: string; relation: Relation }[];
      review: { id: string; reviewed_by: string; reviewed_at: string; state: "current" | "draft_reviewed" | "inputs_changed" } | null;
      note: string;
      unapproved_schedules?: number;
      digest: string;
      scenario: (Scenario & { missing: string[]; base_lowest: { amount: string; on: string } }) | null;
    };

type Matched = {
  obligation_id: string;
  obligation: string;
  on: string;
  amount: string;
  counted: string;
  note: string;
  bills: { id: string; document: string; label: string; relation: Relation; amount: string; state: string; on?: string; confirm?: boolean; unknown?: boolean }[];
};
type Question = { obligation_id: string; obligation: string; on: string; amount: string; vendor: string; effect: string; bills: { document: string; label: string; amount: string; on: string; due: string }[] };
type Relation = "replaces" | "part" | "separate" | "duplicate";
const relations: Record<Relation, string> = {
  replaces: "pays all of it",
  part: "invoices part of it",
  separate: "is a separate payment",
  duplicate: "repeats a payment already counted",
};
const readinessLabel = {
  complete: ["status ok", "Complete forecast"],
  draft: ["status warn", "Draft forecast"],
  incomplete: ["status warn", "Incomplete forecast"],
} as const;

export const every: Record<string, string> = { once: "once", week: "every week", two_weeks: "every two weeks", month: "every month" };
const negative = (value: string) => value.startsWith("-");

// readOnly is an executive's view: the plan alone, without schedules to edit or the agent. scenario is the what-if it
// opens on; onScenario hears each one chosen here.
export function CashPlan({ closeId, readOnly = false, scenario: opened = "", onScenario }: { closeId: string; readOnly?: boolean; scenario?: string; onScenario?: (id: string) => void }) {
  const dates = useDates();
  const [scenario, setScenario] = useState(opened);
  // The plan (as it stands, or under a chosen scenario), the open obligations (not an executive's) and the scenarios,
  // read with the page.
  const view = useQuery(cashPlanQuery<Plan, Obligation, Scenario>(closeId, scenario, readOnly));
  const plan = view.data?.plan;
  const obligations = view.data?.obligations ?? [];
  const scenarios = view.data?.scenarios ?? [];
  const [notice, setNotice] = useState("");
  // A scenario that cannot be shown (removed, or made for other books) never leaves its figures in place.
  useEffect(() => {
    if (!scenario || !view.error) return;
    setScenario("");
    setNotice(`That scenario cannot be shown: ${view.error.message || "it is unavailable"}`);
  }, [scenario, view.error]);
  const readFailed = scenario ? null : view.error;
  const [actionError, setError] = useState("");
  const error = actionError || (readFailed ? readFailed.message || "The cash plan is unavailable." : "");
  const [saved, setSaved] = useState("");
  const [busy, setBusy] = useState(false);
  async function act(body: Record<string, unknown>, done: string) {
    setBusy(true);
    setError("");
    setSaved("");
    try {
      await workspaceRequest(`/${closeId}/cash-obligations`, body);
      setSaved(done);
      return true;
    } catch (e) {
      setError(e instanceof Error ? e.message : "The obligation could not be saved.");
      return false;
    } finally {
      setBusy(false);
    }
  }
  function add(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const element = event.currentTarget;
    const form = Object.fromEntries(new FormData(element));
    void act({ action: "add", ...form }, `Added ${form.name}; it counts once it is approved.`).then((ok) => ok && element.reset());
  }
  if (!plan) return error ? <p role="alert">{error}</p> : <p role="status">Preparing the cash plan…</p>;
  if (plan.state === "waiting")
    return (
      <>
        <p className="empty">{plan.reason}</p>
        {!readOnly && <CashObservations closeId={closeId} />}
      </>
    );
  const show = (value: string) => amount(value, plan.currency);
  const cell = (value: string) => <td style={{ whiteSpace: "nowrap", color: negative(value) ? "var(--danger, #b42318)" : undefined }}>{show(value)}</td>;
  const active = obligations.filter((o) => !o.retired_at);
  return (
    <section aria-label="Cash plan">
      {!readOnly && <CashObservations closeId={closeId} />}
      <p>
        <strong>{plan.entity}</strong> · 13 weeks from {plan.start} · {plan.currency} · opening book cash {show(plan.opening.amount)} at{" "}
        {dates.dateTime(plan.opening.as_of)}
      </p>
      <p className={negative(plan.lowest.amount) ? undefined : "note"} role={negative(plan.lowest.amount) ? "alert" : undefined}>
        <span className={readinessLabel[plan.readiness.state][0]}>{readinessLabel[plan.readiness.state][1]}</span> Lowest projected end-of-day book cash:{" "}
        <strong>{show(plan.lowest.amount)}</strong> on {plan.lowest.on}, over {plan.horizon.weeks} weeks to {plan.horizon.end}.
        {plan.readiness.state !== "complete" && " It is not a conclusion until the items below are settled."}
      </p>
      {plan.review && (
        <p className="note">
          {plan.review.state === "current" ? (
            <span className="status ok">Reviewed</span>
          ) : plan.review.state === "draft_reviewed" ? (
            <span className="status info">Reviewed as a draft</span>
          ) : (
            <span className="status warn">Changed since reviewed</span>
          )}{" "}
          by {plan.review.reviewed_by} {dates.dateTime(plan.review.reviewed_at)}
          {plan.review.state === "draft_reviewed" && ", with the unresolved items above accepted as assumptions"}
          {plan.review.state === "inputs_changed" && "; the reviewed version is kept under Reviewed versions."}
        </p>
      )}
      {plan.readiness.unresolved.length > 0 && (
        <details open={plan.readiness.unresolved.length <= 5}>
          <summary>
            {plan.readiness.unresolved.length} unresolved {plan.readiness.unresolved.length === 1 ? "item" : "items"}
          </summary>
          <ul>
            {plan.readiness.unresolved.map((g, i) => (
              <li key={i}>
                {g.kind === "decision" ? <span className="status info">Decision</span> : <span className="status warn">Source</span>} {g.text}
                {g.amount && <> · {show(g.amount)}</>}
              </li>
            ))}
          </ul>
        </details>
      )}
      {!readOnly && plan.questions.length > 0 && <Questions closeId={closeId} capture={plan.open_items.capture_id} questions={plan.questions} show={show} onDecided={() => void view.refetch()} />}
      {notice && <p role="alert">{notice}</p>}
      {readOnly && error && <p role="alert">{error}</p>}
      <Scenarios
        closeId={closeId}
        plan={plan}
        scenarios={scenarios}
        selected={scenario}
        onSelect={async (id) => {
          // Choosing a scenario reads its plan (a new query). A scenario just saved is chosen only once the list has
          // it, so the figures and the selector always describe the same view.
          setNotice("");
          if (id && !scenarios.some((s) => s.id === id)) {
            const listed = await view.refetch();
            if (!listed.data?.scenarios.some((s) => s.id === id)) {
              setNotice("The scenario was saved, but the list of scenarios could not be read. Reload to choose it.");
              return;
            }
          }
          setScenario(id);
          onScenario?.(id);
        }}
      />
      <Reviews closeId={closeId} plan={plan} scenario={scenario} readOnly={readOnly} />
      <p className="note">{plan.note}</p>
      {readOnly && !!plan.unapproved_schedules && <p className="note">{plan.unapproved_schedules} schedule{plan.unapproved_schedules === 1 ? " is" : "s are"} waiting for the firm's approval and not counted.</p>}
      {plan.opening.stale && <p role="alert">The bank balances come from a capture that is no longer current. Capture the books again before relying on the plan.</p>}
      {!plan.open_items.complete && <p role="alert">{plan.open_items.completeness || "The latest capture did not read every open item, so some receipts or payments are missing."}</p>}
      {plan.open_items.missing_later && (
        <p role="alert">
          This close's capture holds invoices and bills dated up to {plan.open_items.through}; anything dated after that is missing here. The latest month's close has
          them.
        </p>
      )}
      {plan.open_items.hours_apart > 24 && (
        <p role="status">
          The bank balances and the open items were read {plan.open_items.hours_apart} hours apart; an item paid in between may be counted twice or missed.
        </p>
      )}
      {plan.opening.left_out.length > 0 && <p className="note">Left out of opening cash (another currency or unreadable): {plan.opening.left_out.join(", ")}.</p>}
      <table>
        <caption>Week by week</caption>
        <thead>
          <tr>
            <th scope="col" style={{ textAlign: "left" }}>
              Week
            </th>
            <th scope="col">Opening</th>
            <th scope="col">Receipts</th>
            <th scope="col">Payments</th>
            <th scope="col">Closing</th>
            <th scope="col">Lowest in the week</th>
            <th scope="col" style={{ textAlign: "left" }}>
              What moves cash
            </th>
          </tr>
        </thead>
        <tbody>
          {plan.weeks.map((week) => (
            <tr key={week.start} id={`week-${week.start}`}>
              <th scope="row" style={{ textAlign: "left", fontWeight: 400 }}>
                {week.start} to {week.end}
              </th>
              {cell(week.opening)}
              {cell(week.receipts)}
              {cell(week.disbursements)}
              {cell(week.closing)}
              <td style={{ whiteSpace: "nowrap", color: negative(week.lowest) ? "var(--danger, #b42318)" : undefined }}>
                {show(week.lowest)} <small>{week.lowest_on}</small>
              </td>
              <td style={{ textAlign: "left" }}>
                {week.items.length === 0 ? (
                  "Nothing"
                ) : (
                  <details>
                    <summary aria-label={`What moves cash in the week of ${week.start}`}>{week.items.length} items</summary>
                    <ul>
                      {week.items.map((item, i) => (
                        <li key={i}>
                          {item.on} · {item.label} · {show(item.amount)} <small>({item.basis})</small>
                        </li>
                      ))}
                    </ul>
                  </details>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {(plan.past_due.length > 0 || plan.unscheduled.length > 0) && (
        <>
          <SectionHeader id="cash-unplaced" title="Not in the weeks: no agreed date" />
          <p className="note">These are left out rather than given a date nobody agreed. A promise or a bill-pay handoff on the close's open items places them.</p>
          <ul>
            {plan.past_due.map((item, i) => (
              <li key={`past-${i}`}>
                {item.label} · {show(item.amount)} · {item.basis} {item.on} has passed
              </li>
            ))}
            {plan.unscheduled.map((item, i) => (
              <li key={`un-${i}`}>
                {item.label} · {show(item.amount)} · {item.reason}
              </li>
            ))}
          </ul>
        </>
      )}
      {plan.matches.length > 0 && <Matches closeId={closeId} capture={plan.open_items.capture_id} matches={plan.matches} show={show} readOnly={readOnly} onDecided={() => void view.refetch()} />}
      {!readOnly && plan.lapsed.length > 0 && <Lapsed closeId={closeId} capture={plan.open_items.capture_id} lapsed={plan.lapsed} onCleared={() => void view.refetch()} />}
      {plan.after_horizon.length > 0 && <p className="note">{plan.after_horizon.length} open items fall after the 13 weeks.</p>}
      {plan.other_currency.length > 0 && <p className="note">Left out (another currency): {plan.other_currency.join(", ")}.</p>}
      {!readOnly && <>
      <SectionHeader id="cash-schedules" title="Payroll, tax, debt and other schedules" />
      <p className="note">
        Commitments the open items do not show, from the client's own schedules (payroll provider, tax calendar, loan agreement, lease). Each counts once a person
        approves it; nothing here runs payroll or files tax. Name the vendor whose bills pay it: when one of their bills falls in a payment's week, you decide whether it pays it, so
        nothing is counted twice or dropped on a guess.
      </p>
      {saved && <p role="status">{saved}</p>}
      {error && <p role="alert">{error}</p>}
      <ul>
        {active.length === 0 && <li className="empty">No schedules yet.</li>}
        {active.map((o) => (
          <li key={o.id}>
            <strong>{o.name}</strong> · {o.direction === "out" ? "pays" : "receives"} {amount(o.amount, o.currency)} {every[o.every]} from {o.first_on}
            {o.until_on && ` to ${o.until_on}`}
            {o.vendor && ` · paid by ${o.vendor}'s bills`} · <small>{o.basis}</small> ·{" "}
            {o.approved_at ? (
              <span className="status ok">Approved by {o.approved_by}</span>
            ) : (
              <>
                <span className="status warn">Waiting for approval</span>{" "}
                <button type="button" disabled={busy} onClick={() => void act({ action: "approve", id: o.id }, `Approved ${o.name}.`)}>
                  Approve
                </button>
              </>
            )}{" "}
            <details>
              <summary aria-label={`Retire ${o.name}`}>Retire</summary>
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                  void act({ action: "retire", id: o.id, reason: new FormData(e.currentTarget).get("reason") }, `Retired ${o.name}.`);
                }}
              >
                <fieldset disabled={busy}>
                  <label>
                    Why does it no longer apply?
                    <textarea name="reason" required maxLength={2000} />
                  </label>
                  <button>Retire {o.name}</button>
                </fieldset>
              </form>
            </details>
          </li>
        ))}
      </ul>
      <details>
        <summary>Add a schedule</summary>
        <form onSubmit={add}>
          <fieldset disabled={busy}>
            <label>
              Name
              <input name="name" required maxLength={200} placeholder="Payroll" />
            </label>
            <label>
              Kind
              <select name="category" defaultValue="payroll">
                <option value="payroll">Payroll</option>
                <option value="tax">Tax</option>
                <option value="debt">Debt service</option>
                <option value="rent">Rent</option>
                <option value="other">Other</option>
              </select>
            </label>
            <label>
              Direction
              <select name="direction" defaultValue="out">
                <option value="out">Payment out</option>
                <option value="in">Receipt in</option>
              </select>
            </label>
            <label>
              Amount ({plan.currency})
              <input name="amount" required inputMode="decimal" pattern="[0-9]{1,16}(\.[0-9]{1,2})?" placeholder="4000.00" />
            </label>
            <label>
              First date
              <input name="first_on" type="date" required />
            </label>
            <label>
              How often
              <select name="every" defaultValue="month">
                {Object.entries(every).map(([key, label]) => (
                  <option key={key} value={key}>
                    {label}
                  </option>
                ))}
              </select>
            </label>
            <label>
              Last date (optional)
              <input name="until_on" type="date" />
            </label>
            <label>
              Vendor whose bills pay it (optional)
              <input name="vendor" maxLength={200} />
            </label>
            <label>
              Where the schedule comes from
              <textarea name="basis" required minLength={5} maxLength={2000} placeholder="Gusto pay schedule, approved by the CFO" />
            </label>
            <button>Add schedule</button>
          </fieldset>
        </form>
      </details>
      </>}
    </section>
  );
}

// What-ifs over the plan: each moves receipts or payments, or adds them, and changes only its own view of the weeks; the
// base plan, open items and ledger stay as they are.
function Scenarios({ closeId, plan, scenarios, selected, onSelect }: { closeId: string; plan: Extract<Plan, { state: "ready" }>; scenarios: Scenario[]; selected: string; onSelect: (id: string) => void }) {
  const movable = [...plan.past_due, ...plan.weeks.flatMap((w) => w.items), ...plan.after_horizon].filter((i) => i.key);
  const [kind, setKind] = useState<"move" | "add">(movable.length > 0 ? "move" : "add");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const show = (value: string) => amount(value, plan.currency);
  const current = plan.scenario;
  async function save(body: Record<string, unknown>) {
    setBusy(true);
    setError("");
    try {
      const result = await workspaceRequest(`/${closeId}/cash-scenarios`, body);
      onSelect(body.action === "add" ? result.id : "");
      return true;
    } catch (e) {
      setError(e instanceof Error ? e.message : "The scenario could not be saved.");
      return false;
    } finally {
      setBusy(false);
    }
  }
  function add(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const element = event.currentTarget;
    const form = Object.fromEntries([...new FormData(element)].map(([k, v]) => [k, String(v).trim()]));
    const change: Change =
      kind === "move"
        ? { kind, item: form.item, on: form.on }
        : { kind, label: form.label, direction: form.direction, amount: form.amount, first_on: form.first_on, every: form.every, ...(form.until_on ? { until_on: form.until_on } : {}) };
    void save({ action: "add", name: form.name, changes: [change] }).then((ok) => ok && element.reset());
  }
  return (
    <>
      <SectionHeader id="cash-scenarios" title="What if" />
      {error && <p role="alert">{error}</p>}
      {scenarios.length > 0 && (
        <label>
          Show the plan{" "}
          <select value={selected} onChange={(e) => onSelect(e.target.value)}>
            <option value="">as it stands</option>
            {scenarios.map((s) => (
              <option key={s.id} value={s.id}>
                with “{s.name}”
              </option>
            ))}
          </select>
        </label>
      )}
      {current && (
        <p role="status">
          With “{current.name}”, book cash runs lowest at <strong>{show(plan.lowest.amount)}</strong> on {plan.lowest.on}; as the plan stands, its lowest is{" "}
          {show(current.base_lowest.amount)} on {current.base_lowest.on}. A scenario changes nothing in the plan, the open items or the ledger.
          {current.missing.length > 0 && ` Not applied (no longer open or undated): ${current.missing.join(", ")}.`}{" "}
          <button type="button" disabled={busy} onClick={() => void save({ action: "retire", id: current.id })}>
            Remove this scenario
          </button>
        </p>
      )}
      <details>
        <summary>Try a what-if</summary>
        <form onSubmit={add}>
          <fieldset disabled={busy}>
            <label>
              Name
              <input name="name" required maxLength={200} placeholder="Acme pays three weeks late" />
            </label>
            <label>
              Change
              <select value={kind} onChange={(e) => setKind(e.target.value as "move" | "add")}>
                <option value="move" disabled={movable.length === 0}>
                  Move a receipt or payment
                </option>
                <option value="add">Add a receipt or payment</option>
              </select>
            </label>
            {kind === "move" ? (
              <>
                <label>
                  Which one
                  <select name="item" required>
                    {movable.map((i) => (
                      <option key={i.key} value={i.key}>
                        {i.label} · {show(i.amount)} · {i.on}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  New date
                  <input name="on" type="date" required />
                </label>
              </>
            ) : (
              <>
                <label>
                  What it is
                  <input name="label" required maxLength={200} placeholder="New engineer's pay" />
                </label>
                <label>
                  Direction
                  <select name="direction" defaultValue="out">
                    <option value="out">Payment out</option>
                    <option value="in">Receipt in</option>
                  </select>
                </label>
                <label>
                  Amount ({plan.currency})
                  <input name="amount" required inputMode="decimal" pattern="[0-9]{1,16}(\.[0-9]{1,2})?" />
                </label>
                <label>
                  First date
                  <input name="first_on" type="date" required />
                </label>
                <label>
                  How often
                  <select name="every" defaultValue="once">
                    {Object.entries(every).map(([value, label]) => (
                      <option key={value} value={value}>
                        {label}
                      </option>
                    ))}
                  </select>
                </label>
                <label>
                  Last date (optional)
                  <input name="until_on" type="date" />
                </label>
              </>
            )}
            <button>Show the plan with this change</button>
          </fieldset>
        </form>
      </details>
    </>
  );
}

type Review = {
  id: string;
  scenario: string;
  scenario_name: string;
  assumptions: string;
  reviewed_by: string;
  reviewed_at: string;
  start: string;
  lowest: { amount: string; on: string };
  state: "current" | "draft_reviewed" | "inputs_changed" | "superseded" | "unverified";
  currency: string;
  calculation: string;
  actuals: { captured_at: string; on: string; forecast: string; actual?: string; difference?: string; unreadable?: string[] }[];
};
const reviewStates = {
  current: ["status ok", "Current"],
  draft_reviewed: ["status info", "Reviewed as a draft"],
  inputs_changed: ["status warn", "Inputs changed since review"],
  superseded: ["status info", "Superseded"],
  unverified: ["status warn", "Cannot confirm it is current"],
} as const;

// Reviewed plans: a person accepts the assumptions of the plan in view and it is kept as it was; later captures show
// how actual book cash compared with what it expected.
function Reviews({ closeId, plan, scenario, readOnly }: { closeId: string; plan: Extract<Plan, { state: "ready" }>; scenario: string; readOnly: boolean }) {
  const dates = useDates();
  const [list, setList] = useState<Review[]>([]);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState("");
  const [busy, setBusy] = useState(false);
  const [version, setVersion] = useState(0);
  // The states shown are those read for the plan on screen; while reading again, or if that fails, none is shown.
  const [loadedFor, setLoadedFor] = useState("");
  useEffect(() => {
    let live = true;
    setLoadedFor("");
    workspaceRequest(`/${closeId}/cash-plan/reviews`)
      .then((data) => live && (setList(data.reviews), setError(""), setLoadedFor(plan.digest)))
      .catch((e) => live && (setList([]), setError(e instanceof Error ? e.message : "Reviews are unavailable.")));
    return () => {
      live = false;
    };
  }, [closeId, version, plan.digest]);
  const reviewable =
    loadedFor === plan.digest &&
    !plan.opening.stale &&
    plan.open_items.complete &&
    plan.questions.length === 0 &&
    !plan.matches.some((m) => m.bills.some((b) => b.confirm || b.unknown)) &&
    !list.some((r) => r.scenario === scenario && (r.state === "current" || r.state === "draft_reviewed"));
  function review(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const element = event.currentTarget;
    const form = new FormData(element);
    setBusy(true);
    setError("");
    setSaved("");
    workspaceRequest(`/${closeId}/cash-plan/reviews`, { scenario, digest: plan.digest, assumptions: form.get("assumptions"), confirmed: form.has("confirmed") })
      .then(() => {
        setSaved("Reviewed. It stays current until a figure in the plan changes.");
        setVersion((n) => n + 1);
        element.reset();
      })
      .catch((e) => setError(e instanceof Error ? e.message : "The review could not be saved."))
      .finally(() => setBusy(false));
  }
  return (
    <>
      <SectionHeader id="cash-reviews" title="Reviewed versions" />
      {error && <p role="alert">{error}</p>}
      {saved && <p role="status">{saved}</p>}
      {list.length === 0 ? (
        <p className="empty">No reviewed version yet. Until a person reviews it, the plan is a working forecast.</p>
      ) : (
        <ul>
          {list.map((r) => {
            const show = (value: string) => amount(value, r.currency);
            return (
            <li key={r.id}>
              <strong>{r.scenario_name ? `With “${r.scenario_name}”` : "As it stood"}</strong> · from {r.start} · reviewed by {r.reviewed_by} {dates.dateTime(r.reviewed_at)} ·{" "}
              {loadedFor === plan.digest ? <span className={reviewStates[r.state][0]}>{reviewStates[r.state][1]}</span> : <span className="status info">Checking…</span>} · lowest {show(r.lowest.amount)} on {r.lowest.on}
              <p>Assumptions accepted: {r.assumptions}</p>
              {r.actuals.length > 0 && (
                <table>
                  <caption>Book cash at each later capture against the plan's end-of-day balance that day</caption>
                  <thead>
                    <tr>
                      <th scope="col" style={{ textAlign: "left" }}>
                        Captured
                      </th>
                      <th scope="col">Planned end of day</th>
                      <th scope="col">Actual at capture</th>
                      <th scope="col">Difference</th>
                    </tr>
                  </thead>
                  <tbody>
                    {r.actuals.map((a) => (
                      <tr key={a.captured_at}>
                        <th scope="row" style={{ textAlign: "left", fontWeight: 400 }}>
                          {dates.dateTime(a.captured_at)}
                        </th>
                        <td>{show(a.forecast)}</td>
                        {a.actual === undefined ? (
                          <td colSpan={2}>Not stated: the balance of {a.unreadable?.join(", ")} could not be read.</td>
                        ) : (
                          <>
                            <td>{show(a.actual)}</td>
                            <td style={{ color: negative(a.difference ?? "") ? "var(--danger, #b42318)" : undefined }}>{show(a.difference ?? "0.00")}</td>
                          </>
                        )}
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
              <small>
                {r.currency} · calculation {r.calculation} · the actual is measured at the capture's time, which may be before that day's planned payments and
                receipts.
              </small>
            </li>
            );
          })}
        </ul>
      )}
      {!readOnly && reviewable && (
        <form onSubmit={review}>
          <fieldset disabled={busy}>
            <legend>Review the plan {plan.scenario ? `with “${plan.scenario.name}”` : "as it stands"}</legend>
            <label>
              The assumptions you accept: dates, promises and schedules relied on
              <textarea name="assumptions" required minLength={5} maxLength={4000} />
            </label>
            <label>
              <input type="checkbox" name="confirmed" required /> I reviewed these figures; this version is kept as it is.
            </label>
            <button className="primary">Review this plan</button>
          </fieldset>
        </form>
      )}
    </>
  );
}

// One focused decision per schedule date with undecided bills from its vendor that week: what each bill is to that
// date's payment. Until then both count, which the effect states.
function Questions({ closeId, capture, questions, show, onDecided }: { closeId: string; capture: string; questions: Question[]; show: (v: string) => string; onDecided: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [chosen, setChosen] = useState<Record<string, Relation>>({});
  async function decide(body: Record<string, unknown>) {
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(`/${closeId}/cash-matches`, { action: "decide", capture_id: capture, ...body });
      onDecided();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The decision could not be saved.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <SectionHeader id="cash-questions" title="Needs your decision: bills against schedules" />
      {error && <p role="alert">{error}</p>}
      {questions.map((q) => (
        <fieldset key={`${q.obligation_id}-${q.on}`} disabled={busy}>
          <legend>
            {q.obligation} on {q.on} · {show(q.amount)}
          </legend>
          <p className="note">{q.effect}</p>
          <ul>
            {q.bills.map((b) => {
              const key = `${q.obligation_id}-${q.on}-${b.document}`;
              // On a date another bill already pays, the likely answer is a separate payment, never a second full one.
              const relation = chosen[key] ?? (Number(q.amount) === 0 ? "separate" : "replaces");
              return (
                <li key={key}>
                  <form
                    onSubmit={(e) => {
                      e.preventDefault();
                      const amount = String(new FormData(e.currentTarget).get("amount") ?? "").trim();
                      void decide({ obligation_id: q.obligation_id, on: q.on, document: b.document, relation, ...(relation === "part" ? { amount } : {}) });
                    }}
                  >
                    <strong>{b.label}</strong> · {show(b.amount)} · due {b.due || "undated"}
                    {b.on !== b.due && ` · paid ${b.on}`}{" "}
                    <select style={{ width: "auto", display: "inline-block" }} aria-label={`What ${b.label} is to ${q.obligation} on ${q.on}`} value={relation} onChange={(e) => setChosen({ ...chosen, [key]: e.target.value as Relation })}>
                      {(Object.keys(relations) as Relation[]).map((r) => (
                        <option key={r} value={r}>
                          {relations[r]}
                        </option>
                      ))}
                    </select>
                    {relation === "part" && (
                      <label>
                        {" "}of which it invoices{" "}
                        <input style={{ width: "8rem", display: "inline-block" }} name="amount" required inputMode="decimal" pattern="[0-9]{1,16}(\.[0-9]{1,2})?" defaultValue={b.amount} />
                      </label>
                    )}{" "}
                    <button className="primary">Decide</button>
                  </form>
                </li>
              );
            })}
          </ul>
        </fieldset>
      ))}
    </>
  );
}

// Decisions already made: each schedule date with its bills and what the plan still counts from the schedule. A decision
// is changed by naming the one it replaces; a paid bill's decision can be changed but not cleared.
function Matches({ closeId, capture, matches, show, readOnly, onDecided }: { closeId: string; capture: string; matches: Matched[]; show: (v: string) => string; readOnly: boolean; onDecided: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [chosen, setChosen] = useState<Record<string, Relation>>({});
  async function send(body: Record<string, unknown>) {
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(`/${closeId}/cash-matches`, { capture_id: capture, ...body });
      onDecided();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The decision could not be changed.");
    } finally {
      setBusy(false);
    }
  }
  const state = (bill: Matched["bills"][number]) => (bill.state === "counted" ? `counted on ${bill.on}` : bill.state === "paid" ? "paid: no longer open" : bill.state);
  return (
    <>
      <SectionHeader id="cash-matches" title="Bills against schedules" />
      {error && <p role="alert">{error}</p>}
      <ul>
        {matches.map((m) => (
          <li key={`${m.obligation_id}-${m.on}`}>
            <strong>{m.obligation}</strong> on {m.on} ({show(m.amount)}): the plan counts {show(m.counted)} from the schedule. {m.note}
            <ul>
              {m.bills.map((b) => {
                const relation = chosen[b.id] ?? b.relation;
                const base = { obligation_id: m.obligation_id, on: m.on, document: b.document, previous_id: b.id };
                return (
                  <li key={b.id}>
                    {b.label} {relations[b.relation]} ({show(b.amount)}) · {state(b)}
                    {!readOnly && (
                      <form
                        style={{ display: "inline" }}
                        onSubmit={(e) => {
                          e.preventDefault();
                          const amount = String(new FormData(e.currentTarget).get("amount") ?? "").trim();
                          void send({ action: "decide", ...base, relation, ...(relation === "part" ? { amount } : {}) });
                        }}
                      >
                        <fieldset disabled={busy} style={{ display: "inline", border: 0, padding: 0, margin: 0 }}>
                          {" "}
                          <select style={{ width: "auto", display: "inline-block" }} aria-label={`Change what ${b.label} is to ${m.obligation} on ${m.on}`} value={relation} onChange={(e) => setChosen({ ...chosen, [b.id]: e.target.value as Relation })}>
                            {(Object.keys(relations) as Relation[]).map((r) => (
                              <option key={r} value={r}>
                                {relations[r]}
                              </option>
                            ))}
                          </select>
                          {relation === "part" && (
                            <input style={{ width: "8rem", display: "inline-block" }} aria-label="Amount it invoices" name="amount" required inputMode="decimal" pattern="[0-9]{1,16}(\.[0-9]{1,2})?" defaultValue={b.amount} />
                          )}{" "}
                          <button disabled={relation === b.relation && relation !== "part"}>Change</button>
                          {b.confirm && (
                            <>
                              {" "}
                              <button type="button" className="primary" onClick={() => void send({ action: "paid", ...base })}>
                                It was paid
                              </button>
                            </>
                          )}
                          {b.state !== "paid" && (
                            <>
                              {" "}
                              <button type="button" onClick={() => void send({ action: "clear", ...base })}>
                                Clear
                              </button>
                            </>
                          )}
                        </fieldset>
                      </form>
                    )}
                  </li>
                );
              })}
            </ul>
          </li>
        ))}
      </ul>
    </>
  );
}

// Decisions made for a schedule date that no longer applies: they move nothing, and clearing one lets the bill be decided
// against what applies now.
function Lapsed({ closeId, capture, lapsed, onCleared }: { closeId: string; capture: string; lapsed: Extract<Plan, { state: "ready" }>["lapsed"]; onCleared: () => void }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function clear(d: (typeof lapsed)[number]) {
    setBusy(true);
    setError("");
    try {
      await workspaceRequest(`/${closeId}/cash-matches`, { action: "clear", capture_id: capture, obligation_id: d.obligation_id, on: d.on, document: d.document, previous_id: d.id });
      onCleared();
    } catch (e) {
      setError(e instanceof Error ? e.message : "The decision could not be cleared.");
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <SectionHeader id="cash-lapsed" title="Decisions that no longer apply" />
      {error && <p role="alert">{error}</p>}
      <ul>
        {lapsed.map((d) => (
          <li key={d.id}>
            {d.label} {relations[d.relation]} on {d.on}, but that schedule's date no longer counts.{" "}
            <button type="button" disabled={busy} onClick={() => void clear(d)}>
              Clear
            </button>
          </li>
        ))}
      </ul>
    </>
  );
}
