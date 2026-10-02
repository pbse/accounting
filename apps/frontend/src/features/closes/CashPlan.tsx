import { type FormEvent, useEffect, useState } from "react";
import { AskAgent } from "../dashboard/AskAgent";
import { amount, workspaceRequest } from "./workspace";
import { SectionHeader } from "@close/ui/components/SectionHeader";

type Item = { on: string; amount: string; label: string; source: string; basis: string };
type Week = { start: string; end: string; opening: string; receipts: string; disbursements: string; closing: string; lowest: string; lowest_on: string; items: Item[] };
type Obligation = {
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
type Plan =
  | { state: "waiting"; reason: string }
  | {
      state: "ready";
      entity: string;
      currency: string;
      start: string;
      opening: { amount: string; as_of: string; stale: boolean; accounts: { name: string; balance: string }[]; left_out: string[] };
      open_items: { captured_at: string; complete: boolean; hours_apart: number; through: string; missing_later: boolean };
      weeks: Week[];
      lowest: { amount: string; on: string };
      past_due: Item[];
      after_horizon: Item[];
      unscheduled: { label: string; amount: string; reason: string }[];
      other_currency: string[];
      covered: { obligation: string; on: string; vendor: string }[];
      note: string;
    };

const every: Record<string, string> = { once: "once", week: "every week", two_weeks: "every two weeks", month: "every month" };
const negative = (value: string) => value.startsWith("-");

export function CashPlan({ closeId }: { closeId: string }) {
  const [plan, setPlan] = useState<Plan>();
  const [obligations, setObligations] = useState<Obligation[]>([]);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState("");
  const [busy, setBusy] = useState(false);
  const [version, setVersion] = useState(0);
  useEffect(() => {
    let live = true;
    Promise.all([workspaceRequest(`/${closeId}/cash-plan`), workspaceRequest(`/${closeId}/cash-obligations`)])
      .then(([data, list]) => {
        if (live) {
          setPlan(data);
          setObligations(list.obligations);
          setError("");
        }
      })
      .catch((e) => live && setError(e instanceof Error ? e.message : "The cash plan is unavailable."));
    return () => {
      live = false;
    };
  }, [closeId, version]);
  async function act(body: Record<string, unknown>, done: string) {
    setBusy(true);
    setError("");
    setSaved("");
    try {
      await workspaceRequest(`/${closeId}/cash-obligations`, body);
      setSaved(done);
      setVersion((n) => n + 1);
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
  if (plan.state === "waiting") return <p className="empty">{plan.reason}</p>;
  const show = (value: string) => amount(value, plan.currency);
  const cell = (value: string) => <td style={{ whiteSpace: "nowrap", color: negative(value) ? "var(--danger, #b42318)" : undefined }}>{show(value)}</td>;
  const active = obligations.filter((o) => !o.retired_at);
  return (
    <section aria-label="Cash plan">
      <AskAgent
        closeId={closeId}
        description="Ask about the next 13 weeks. The agent reads the same plan, open items and approved schedules."
        placeholder="Which week is tightest, and why?"
        suggestions={["When does book cash run lowest, and what causes it?", "Which overdue invoices would cover the shortfall?", "Which payments could move without missing a due date?"]}
      />
      <p>
        <strong>{plan.entity}</strong> · 13 weeks from {plan.start} · {plan.currency} · opening book cash {show(plan.opening.amount)} at{" "}
        {new Date(plan.opening.as_of).toLocaleString()}
      </p>
      <p className={negative(plan.lowest.amount) ? undefined : "note"} role={negative(plan.lowest.amount) ? "alert" : undefined}>
        Lowest end-of-day book cash: <strong>{show(plan.lowest.amount)}</strong> on {plan.lowest.on}.
      </p>
      <p className="note">{plan.note}</p>
      {plan.opening.stale && <p role="alert">The bank balances come from a capture that is no longer current. Capture the books again before relying on the plan.</p>}
      {!plan.open_items.complete && <p role="alert">The latest capture did not read every open item, so some receipts or payments are missing.</p>}
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
            <tr key={week.start}>
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
          <SectionHeader title="Not in the weeks: no agreed date" />
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
      {plan.covered.length > 0 && (
        <p className="note">
          Not counted twice: {plan.covered.map((c) => `${c.obligation} on ${c.on} is ${c.vendor}'s open bill due that week`).join("; ")}.
        </p>
      )}
      {plan.after_horizon.length > 0 && <p className="note">{plan.after_horizon.length} open items fall after the 13 weeks.</p>}
      {plan.other_currency.length > 0 && <p className="note">Left out (another currency): {plan.other_currency.join(", ")}.</p>}

      <SectionHeader title="Payroll, tax, debt and other schedules" />
      <p className="note">
        Commitments the open items do not show, from the client's own schedules (payroll provider, tax calendar, loan agreement, lease). Each counts once a person
        approves it; nothing here runs payroll or files tax. Name the vendor whose bills pay it so a week with that bill does not count the payment twice.
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
    </section>
  );
}
