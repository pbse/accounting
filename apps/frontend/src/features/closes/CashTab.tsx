import { useEffect, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Icon } from "@close/ui/components/Icon";
import { Segmented } from "@close/ui/components/Segmented";
import { cashPlanQuery } from "../../data/cashPlan";
import { useDates, utcDay } from "../../data/time";
import { CashPlan, every, type Obligation, type Plan, type Scenario, type Week } from "./CashPlan";
import type { Status } from "./Goal";
import { AgentPanel, Glance, MoreLinks, NeedsYou, type Figure, type Need } from "./Overview";
import type { Brief } from "./Stands";
import type { Close, Paper } from "./WorkpaperView";
import { amount } from "./workspace";

// A close's Cash plan (docs/design-system.md, Close page): where the 13-week cash outlook stands in one sentence, then
// the plan's own decisions and its weeks as one table, the lowest week marked, under the plan as it stands or a what-if;
// the rail gives the lowest point and what is left out, and opens the full plan's sections (schedules, what-ifs,
// reviewed versions, bills against schedules).

type Ready = Extract<Plan, { state: "ready" }>;
const words = ["no", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten", "eleven", "twelve"];
const capital = (text: string) => text[0].toUpperCase() + text.slice(1);
const w = (n: number) => words[n] ?? String(n);

// The firm's view of the plan, as the full plan reads it: the same query, so either reads it for the other.
const useCash = (closeId: string, scenario = "") => useQuery(cashPlanQuery<Plan, Obligation, Scenario>(closeId, scenario, false));

// The plan's decisions, each opening where it is made: bills a schedule's payment may already cover, bills to confirm
// paid, decisions that no longer apply, schedules waiting for approval, and open items no one has given a date. A bill
// the capture cannot place is a gap in the sources, not a decision, and stays with the plan's readiness.
function cashNeeds(plan: Ready, obligations: Obligation[], base: string): Need[] {
  const full = (anchor: string) => `${base}?tab=cash&view=plan#${anchor}`;
  const unplaced = plan.past_due.length + plan.unscheduled.length;
  return [
    // Each bill in a question is its own decision.
    ...plan.questions.map((q) => ({ key: `cash-question:${q.obligation_id}:${q.on}`, title: `Are ${q.vendor}'s bills part of ${q.obligation} on ${utcDay(q.on)}?`, detail: q.effect, action: "Decide", to: full("cash-questions"), count: Math.max(1, q.bills.length) })),
    ...plan.matches.flatMap((m) =>
      m.bills.filter((b) => b.confirm).map((b) => ({ key: `cash-paid:${b.id}`, title: `Was ${b.label} paid?`, detail: `It left the open items. Until you confirm it was paid, ${m.obligation} on ${utcDay(m.on)} still counts its estimate.`, action: "Confirm", to: full("cash-matches"), count: 1 })),
    ),
    ...plan.lapsed.map((l) => ({ key: `cash-lapsed:${l.id}`, title: `A decision on ${l.label} no longer applies`, detail: "It moves nothing now. Clear it so the bill can be decided against what applies.", action: "Clear it", to: full("cash-lapsed"), count: 1 })),
    ...obligations
      .filter((o) => !o.approved_at && !o.retired_at)
      .map((o) => ({ key: `cash-schedule:${o.id}`, title: `Approve the ${o.name} schedule`, detail: `${o.direction === "out" ? "Pays" : "Receives"} ${amount(o.amount, o.currency)} ${every[o.every] ?? o.every} from ${utcDay(o.first_on)}; it counts only once approved.`, action: "Review the schedule", to: full("cash-schedules"), count: 1 })),
    ...(unplaced ? [{ key: "cash-unplaced", title: `${capital(w(unplaced))} open item${unplaced === 1 ? " has" : "s have"} no agreed date`, detail: "They are left out of the weeks until a promise or a bill-pay date on the open items places them.", action: "Open items", to: `${base}?tab=books&view=open-items`, count: unplaced }] : []),
  ];
}

// The plan can be reviewed once its capture is current and complete and no bill against a schedule is undecided or
// unplaced, and no review of it stands: the full plan's review form checks the same, with the reviews it reads.
function reviewBlock(plan: Ready) {
  if (plan.review && plan.review.state !== "inputs_changed") return "reviewed";
  if (plan.opening.stale) return "Capture the books again first";
  if (!plan.open_items.complete) return plan.open_items.completeness || "The capture did not read every open item";
  if (plan.questions.length || plan.matches.some((m) => m.bills.some((b) => b.confirm || b.unknown))) return "Settle the bills against schedules first";
  return "";
}

// CashLede is the tab's sentence: the plan's decisions, whether it is reviewed or changed since, or whether it can be.
// Under it: how complete the forecast is, its weeks, its lowest point and its review.
export function CashLede({ closeId, base }: { closeId: string; base: string }) {
  const dates = useDates();
  const read = useCash(closeId);
  const plan = read.data?.plan;
  if (!plan || plan.state === "waiting")
    return (
      <div className="ks-lede">
        <h1>{!plan ? (read.isError ? "The cash plan could not be read." : "The client's cash outlook.") : "The cash plan waits on the month's books."}</h1>
        {plan && <p>{plan.reason}</p>}
      </div>
    );
  const decisions = cashNeeds(plan, read.data?.obligations ?? [], base).reduce((n, need) => n + need.count, 0);
  // A plan that could not be read again is the last read, said so: its review may no longer stand.
  const stale = read.isError ? `not refreshed since ${dates.time(read.dataUpdatedAt)}` : "";
  const blocked = reviewBlock(plan);
  const sentence = decisions
    ? `The cash outlook needs ${w(decisions)} decision${decisions === 1 ? "" : "s"}.`
    : plan.review?.state === "current"
      ? "The cash plan is reviewed."
      : plan.review?.state === "draft_reviewed"
        ? "The cash plan is reviewed as a draft."
        : plan.review?.state === "inputs_changed"
          ? "The cash plan changed after it was reviewed."
          : !blocked
            ? "The cash plan is ready for your review."
            : "The cash plan is not ready for review.";
  const facts = [
    { complete: "Complete forecast", draft: "Draft forecast", incomplete: "Incomplete forecast" }[plan.readiness.state],
    `${plan.horizon.weeks} weeks from ${utcDay(plan.start)}`,
    `lowest ${amount(plan.lowest.amount, plan.currency)} on ${utcDay(plan.lowest.on)}`,
    plan.review ? `reviewed ${dates.date(plan.review.reviewed_at)} by ${plan.review.reviewed_by}` : blocked && blocked !== "reviewed" ? blocked.toLowerCase() : "not reviewed",
    stale,
  ].filter(Boolean);
  return (
    <div className="ks-lede">
      <h1>{sentence}</h1>
      <p>{facts.join(" · ")}</p>
    </div>
  );
}

// What moves cash in a week, largest first: its three biggest items by name.
const movers = (week: Week) => {
  const names = [...new Set([...week.items].sort((a, b) => Math.abs(Number(b.amount)) - Math.abs(Number(a.amount))).map((item) => item.label))];
  return names.length ? names.slice(0, 3).join(", ") + (names.length > 3 ? ` and ${names.length - 3} more` : "") : "Nothing";
};

// Weeks shows the plan week by week, the week of its lowest point marked: the first eight and the lowest, the rest
// behind a line. The what-if shown is in the address, so the full plan it opens shows the same figures.
function Weeks({ plan, scenarios, scenario, base }: { plan: Ready; scenarios: Scenario[]; scenario: string; base: string }) {
  const full = (anchor: string) => `${base}?tab=cash&view=plan${scenario ? `&scenario=${encodeURIComponent(scenario)}` : ""}#${anchor}`;
  const [all, setAll] = useState(false);
  const show = (value: string) => amount(value, plan.currency);
  const flow = (value: string) => (Number(value) === 0 ? "—" : show(value));
  const blocked = reviewBlock(plan);
  const lowest = (week: Week) => plan.lowest.on >= week.start && plan.lowest.on <= week.end;
  const shown = all ? plan.weeks : plan.weeks.filter((week, i) => i < 8 || lowest(week));
  const rest = plan.weeks.length - shown.length;
  return (
    <section className="bare ks-set" aria-labelledby="weeks-title">
      <div className="ks-sec">
        <h2 id="weeks-title">Week by week</h2>
        <small>{plan.readiness.state === "complete" ? "complete" : plan.readiness.state === "draft" ? "draft" : "incomplete"}</small>
        <div className="ks-end">
          {scenarios.length > 0 && <Segmented label="Show the plan" items={[{ key: "", label: "As it stands", to: `${base}?tab=cash` }, ...scenarios.map((s) => ({ key: s.id, label: s.name, to: `${base}?tab=cash&scenario=${encodeURIComponent(s.id)}` }))]} value={scenario} />}
          {blocked === "reviewed" ? null : blocked ? (
            <button type="button" disabled title={blocked}>
              Review this plan
            </button>
          ) : (
            <Link className="button" to={full("cash-reviews")}>
              Review this plan
            </Link>
          )}
        </div>
      </div>
      <div className="ks-table ks-weeks" role="table" aria-labelledby="weeks-title">
        <div className="ks-tr head" role="row">
          <span role="columnheader">Week of</span>
          <span role="columnheader" className="r">
            Receipts
          </span>
          <span role="columnheader" className="r">
            Payments
          </span>
          <span role="columnheader" className="r">
            Closing
          </span>
          <span role="columnheader" className="r">
            Lowest day
          </span>
          <span role="columnheader">What moves cash</span>
          <span role="columnheader">
            <span className="visually-hidden">Open</span>
          </span>
        </div>
        {shown.map((week) => {
          const low = lowest(week);
          return (
            <div className={`ks-tr linked${low ? " ks-low" : ""}`} role="row" key={week.start}>
              <span className="ks-who" role="cell">
                <Link className="ks-rowlink" to={full(`week-${week.start}`)}>
                  {utcDay(week.start)}
                </Link>
              </span>
              <span className="r" role="cell">
                {flow(week.receipts)}
              </span>
              <span className="r" role="cell">
                {flow(week.disbursements)}
              </span>
              <span className={`r${week.closing.startsWith("-") ? " ks-neg" : ""}`} role="cell">
                {show(week.closing)}
              </span>
              <span className={`r${week.lowest.startsWith("-") ? " ks-neg" : ""}`} role="cell" title={`on ${utcDay(week.lowest_on)}`}>
                {show(week.lowest)}
              </span>
              <span className="ks-mut" role="cell">
                {low ? `Lowest point, ${utcDay(plan.lowest.on)} · ` : ""}
                {movers(week)}
              </span>
              <span role="cell">
                <Icon name="arrow" className="ks-go" />
              </span>
            </div>
          );
        })}
      </div>
      {(rest > 0 || all) && (
        <p className="ks-table-foot">
          {all ? "" : `${capital(w(rest))} more week${rest === 1 ? "" : "s"} to ${utcDay(plan.horizon.end)} · `}
          <button type="button" className="ks-link" onClick={() => setAll(!all)}>
            {all ? "Show fewer" : `Show all ${plan.weeks.length}`}
          </button>
        </p>
      )}
    </section>
  );
}

// CashTab is the tab's body: on the left the plan's decisions and its weeks, or the full plan; on the right the lowest
// point and what is left out, the agent and the full plan's sections.
export function CashTab({ close, papers, brief, briefRead, decisionsError, status, base }: { close: Close; papers: Paper[]; brief: Brief | undefined; briefRead: { dataUpdatedAt: number; isFetching: boolean; refetch: () => unknown }; decisionsError: string; status: Status | undefined; base: string }) {
  const dates = useDates();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const full = params.get("view") === "plan";
  const scenario = params.get("scenario") ?? "";
  const [notice, setNotice] = useState("");
  const asIs = useCash(close.id);
  const chosen = useCash(close.id, scenario);
  // The what-if is the address's: choosing one, here or in the full plan, keeps the view and changes only it.
  const choose = (id: string) => {
    const next = new URLSearchParams(params);
    if (id) next.set("scenario", id);
    else next.delete("scenario");
    void navigate(`${base}?${next}`, { replace: true });
  };
  // A what-if that cannot be read (removed, or made for other books) never leaves its figures in place, cached or not:
  // the plan as it stands is shown instead, and said so, as the full plan does.
  useEffect(() => {
    if (scenario && !chosen.error) setNotice("");
    if (!scenario || !chosen.error) return;
    setNotice(`That what-if cannot be shown: ${chosen.error.message || "it is unavailable"}`);
    choose("");
  }, [scenario, chosen.error]);
  const base_ = asIs.data?.plan;
  // A what-if's figures only, never the plan as it stands under its name while it is read.
  const plan = scenario ? chosen.data?.plan : base_;
  const shownRead = scenario ? chosen : asIs;
  const obligations = asIs.data?.obligations ?? [];
  const scenarios = asIs.data?.scenarios ?? [];
  const needs = base_?.state === "ready" ? cashNeeds(base_, obligations, base) : [];
  // The full plan's sections, on the what-if shown.
  const plain = `${base}?tab=cash&view=plan${scenario ? `&scenario=${encodeURIComponent(scenario)}` : ""}`;
  const to = (anchor: string) => `${plain}#${anchor}`;
  const active = obligations.filter((o) => !o.retired_at);
  const pending = active.filter((o) => !o.approved_at).length;
  const figures: Figure[] =
    plan?.state === "ready"
      ? ([
          [amount(plan.lowest.amount, plan.currency), `lowest end of day, ${utcDay(plan.lowest.on)}`, plan.lowest.amount.startsWith("-") ? "att" : undefined],
          [amount(plan.opening.amount, plan.currency), `opening book cash, ${utcDay(plan.start)}`],
          ...(plan.past_due.length + plan.unscheduled.length ? [[String(plan.past_due.length + plan.unscheduled.length), "open items in no week yet", "att"]] : []),
          ...(plan.readiness.unresolved.length ? [[String(plan.readiness.unresolved.length), plan.readiness.unresolved.length === 1 ? "item to settle before it is complete" : "items to settle before it is complete"]] : []),
        ] as Figure[]).slice(0, 4)
      : [];
  const lines = brief ? { ...brief, lines: brief.lines.filter((line) => line.tab === "cash") } : undefined;
  return (
    <div className="ks-close-grid">
      <div className="ks-col">
        {notice && <p role="alert">{notice}</p>}
        {full ? (
          <>
            <p className="ks-view-crumbs">
              <Link to={`${base}?tab=cash`}>Cash plan</Link>
              <span>/</span>
              <b>The full plan</b>
            </p>
            <CashPlan key={`cash-${close.id}-${scenario}`} closeId={close.id} scenario={scenario} onScenario={choose} />
          </>
        ) : (
          <>
            <NeedsYou key={close.id} closeId={close.id} papers={papers} needs={needs} teammates={0} brief={lines} briefRead={briefRead} decisionsError={decisionsError} base={base} preparing={false} />
            {plan && shownRead.isError && <p role="alert">The cash plan could not be read again: {shownRead.error?.message || "it is unavailable"}. Showing the read of {dates.time(shownRead.dataUpdatedAt)}.</p>}
            {!plan ? (
              shownRead.isError ? <p role="alert">{shownRead.error?.message || "The cash plan is unavailable."}</p> : <p role="status">{scenario ? "Reading the what-if…" : "Preparing the cash plan…"}</p>
            ) : plan.state === "waiting" ? (
              <section className="bare ks-set" aria-labelledby="weeks-title">
                <div className="ks-sec">
                  <h2 id="weeks-title">Week by week</h2>
                </div>
                <p className="ks-calm">
                  <Icon name="book" />
                  {plan.reason}
                </p>
              </section>
            ) : (
              <Weeks plan={plan} scenarios={scenarios} scenario={scenario} base={base} />
            )}
          </>
        )}
      </div>
      <aside className="ks-rail" aria-label="About the cash plan">
        <Glance figures={figures} />
        <AgentPanel close={close} status={status} />
        <MoreLinks
          title="More on Cash plan"
          links={[
            { label: "The full plan", to: plain },
            { label: "Schedules", note: active.length ? `${active.length}${pending ? `, ${pending} to approve` : ""}` : undefined, to: to("cash-schedules") },
            { label: "What-ifs", note: scenarios.length ? String(scenarios.length) : undefined, to: to("cash-scenarios") },
            { label: "Reviewed versions", note: base_?.state === "ready" && base_.review ? dates.dayMonth(base_.review.reviewed_at) : undefined, to: to("cash-reviews") },
          ]}
        />
      </aside>
    </div>
  );
}
