import { useState } from "react";
import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Dialog } from "@close/ui/components/Dialog";
import { Icon } from "@close/ui/components/Icon";
import { workspaceQuery } from "../../data/queries";
import { monthName, useDates, useToday } from "../../data/time";
import { PlanningControls, type Plan } from "./Planning";
import type { Close } from "./WorkpaperView";

// A close's page says where the close stands in one sentence (docs/design-system.md, Lede), then shows only what is
// left for the person as numbered tasks (Task), with what the agent already did folded to a line beneath (DoneLine).
// Both read the close's brief: each area's state, the text that says why, and the tab that holds it.

type Line = { area: string; state: "done" | "attention" | "waiting" | "info" | "unknown"; text: string; tab?: string };
type Brief = { headline: string; lines: Line[] };

const area = (line: Line) => line.area[0].toUpperCase() + line.area.slice(1);
// What the brief says needs the person: its headline counts the actions ("3 things need you: …"), which can outnumber
// the areas they fall in; the areas are what the tasks show.
// Sign-off is the page's one action: available when the brief's sign-off line says the close is ready (readiness
// with no blockers), never while blockers wait or once it is signed off.
export function signOffAvailable(brief: Brief | undefined) {
  const line = brief?.lines.find((l) => l.area === "sign-off");
  return !!line && line.state === "attention" && /ready/i.test(line.text);
}
export function needsOf(brief: Brief) {
  // A section that could not be read is said so, never counted as the person's work.
  const needs = brief.lines.filter((l) => l.state === "attention");
  const counted = /^(\d+) things? needs? you/.exec(brief.headline);
  return { needs, count: Math.max(needs.length, counted ? Number(counted[1]) : 0) };
}
const n = (count: number, one: string, many = `${one}s`) => `${count} ${count === 1 ? one : many}`;

export function useBrief(closeId: string) {
  return useQuery(workspaceQuery<Brief>(`/${closeId}/brief`));
}

// The sentence: how many steps are left for the person, or what the close waits on, or that it is ready. Under it,
// the due date, the agent's share, and the plan (due date, priority, who holds it), which opens to be changed.
export function CloseLede({ close, onPlanSaved }: { close: Close & Plan; onPlanSaved: () => void }) {
  const dates = useDates();
  const today = useToday();
  const read = useBrief(close.id);
  const brief = read.data;
  const [planning, setPlanning] = useState(false);
  const lines = brief?.lines ?? [];
  const { count } = brief ? needsOf(brief) : { count: 0 };
  const waiting = lines.filter((l) => l.state === "waiting");
  const done = lines.filter((l) => l.state === "done");
  const unchecked = lines.filter((l) => l.state === "unknown");
  const sentence = !brief
    ? read.isError
      ? `${close.entity}’s close could not be read.`
      : `${close.entity}, ${monthName(close.period)}.`
    : brief.headline.startsWith("Signed off.")
      ? `${close.entity} is signed off.`
      : count
        ? `${close.entity} is ${count === 1 ? "one step" : `${n(count, "step")}`} from done.`
        : waiting.length
          ? `${close.entity} is waiting on ${waiting.map((l) => l.area).join(" and ")}.`
          : signOffAvailable(brief)
            ? `${close.entity} is ready to sign off.`
            : `${close.entity} could not be fully checked.`;
  const context = [
    close.due_on ? (close.due_on < today ? `Overdue since ${dates.calendarDay(close.due_on)}` : `Due ${dates.calendarDay(close.due_on)}`) : "No due date",
    brief ? `${done.length} of ${lines.length} done` : "",
    close.priority && close.priority !== "normal" ? `${close.priority} priority` : "",
    close.owned_by_me ? "yours" : close.assigned ? "held by a colleague" : "",
    // A section that could not be read is said here on every tab, not only where the tasks show it.
    unchecked.length ? `not checked: ${unchecked.map((l) => l.area).join(", ")}` : "",
  ].filter(Boolean);
  return (
    <div className="ks-lede">
      <h2>{sentence}</h2>
      <p>
        {context.join(" · ")}
        {" · "}
        <button type="button" className="ks-link" onClick={() => setPlanning(true)}>
          Plan
        </button>
      </p>
      <Dialog open={planning} onClose={() => setPlanning(false)} title="Plan this close" description="Its due date, priority, and who holds it.">
        <PlanningControls
          key={`${close.due_on}-${close.priority}-${close.owned_by_me}-${close.assigned}-${close.can_assign}-${close.can_take_over}`}
          closeId={close.id}
          plan={close}
          onSaved={() => {
            setPlanning(false);
            onPlanSaved();
          }}
        />
      </Dialog>
    </div>
  );
}

// What is left for the person, in order, one card each with one button; then what waits on someone else; then what
// the agent did, folded to a line that opens to the list.
export function CloseTasks({ closeId, base }: { closeId: string; base: string }) {
  const dates = useDates();
  const read = useBrief(closeId);
  const brief = read.data;
  const error = read.error ? read.error.message || "The close brief is unavailable." : "";
  if (!brief) return error ? <p role="alert">{error}</p> : null;
  const { needs } = needsOf(brief);
  const waiting = brief.lines.filter((l) => l.state === "waiting");
  const unchecked = brief.lines.filter((l) => l.state === "unknown");
  const rest = brief.lines.filter((l) => l.state === "done" || l.state === "info");
  const done = rest.filter((l) => l.state === "done");
  return (
    <div className="ks-tasks" aria-label="Where this close stands">
      {error && <p role="alert">{error} Showing the last read.</p>}
      {needs.map((line, i) => (
        <div className="ks-task" key={line.area}>
          <span className="ks-numeral">{i + 1}</span>
          <div>
            <b>{area(line)}</b>
            <p>{line.text}</p>
          </div>
          {line.tab ? (
            <Link className="button" to={`${base}?tab=${line.tab}`}>
              Open
            </Link>
          ) : (
            <span />
          )}
        </div>
      ))}
      {unchecked.map((line) => (
        <div className="ks-task unchecked" key={line.area}>
          <span className="ks-numeral">
            <Icon name="blocked" size={14} />
          </span>
          <div>
            <b>{area(line)} · not checked</b>
            <p>{line.text}</p>
          </div>
          {line.tab ? (
            <Link className="button" to={`${base}?tab=${line.tab}`}>
              Open
            </Link>
          ) : (
            <span />
          )}
        </div>
      ))}
      {waiting.map((line) => (
        <div className="ks-task waiting" key={line.area}>
          <span className="ks-numeral">
            <Icon name="waiting" size={14} />
          </span>
          <div>
            <b>{area(line)}</b>
            <p>{line.text}</p>
          </div>
          {line.tab ? (
            <Link className="button" to={`${base}?tab=${line.tab}`}>
              Open
            </Link>
          ) : (
            <span />
          )}
        </div>
      ))}
      <details className="ks-done">
        <summary className="ks-done-line">
          <Icon name="check" />
          {done.length ? `${done.length} done · ${done.map((l) => l.area).join(", ")}` : rest.length ? `${n(rest.length, "note")}` : "Nothing done yet"}
          <small>
            As of {dates.time(read.dataUpdatedAt)}
            {" · "}
            <button
              type="button"
              className="ks-link"
              disabled={read.isFetching}
              onClick={(event) => {
                event.preventDefault();
                void read.refetch();
              }}
            >
              Refresh
            </button>
          </small>
          <Icon name="chevron" className="ks-chev" size={14} />
        </summary>
        <div className="ks-done-list">
          {rest.map((line) => (
            <div className="ks-done-row" key={line.area}>
              <span className={`status ${line.state === "done" ? "ok" : "info"}`}>{line.state === "done" ? "Done" : "Note"}</span>
              <b>{area(line)}</b>
              <span>{line.text}</span>
              {line.tab ? <Link to={`${base}?tab=${line.tab}`}>Open</Link> : <span />}
            </div>
          ))}
          {rest.length === 0 && <p className="empty">Nothing yet.</p>}
        </div>
      </details>
    </div>
  );
}
