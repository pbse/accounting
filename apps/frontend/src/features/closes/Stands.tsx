import { useQuery } from "@tanstack/react-query";
import { workspaceQuery } from "../../data/queries";
import { monthName, useDates, useToday } from "../../data/time";
import { due } from "./Goal";
import type { Plan } from "./Planning";
import type { Close } from "./WorkpaperView";

// A close's page says where the close stands in one sentence (docs/design-system.md, Lede): how many decisions and
// blockers need the person, counted as Overview lists them, or what the close waits on, or that it is ready. The
// brief gives each area's state, the text that says why, and the tab that holds it.

export type Line = { area: string; state: "done" | "attention" | "waiting" | "info" | "unknown"; text: string; tab?: string };
export type Brief = { headline: string; lines: Line[] };

export const area = (line: Line) => line.area[0].toUpperCase() + line.area.slice(1);
// Sign-off is the page's one action: available when the brief's sign-off line says the close is ready (readiness
// with no blockers), never while blockers wait or once it is signed off.
export function signOffAvailable(brief: Brief | undefined) {
  const line = brief?.lines.find((l) => l.area === "sign-off");
  return !!line && line.state === "attention" && /ready/i.test(line.text);
}
// The areas whose line needs the person. Signing off is the page's own action, not a need: a sign-off line that says
// the close is ready leaves the list.
export function needsOf(brief: Brief) {
  return { needs: brief.lines.filter((l) => l.state === "attention" && !(l.area === "sign-off" && /ready/i.test(l.text))) };
}
const words = ["no", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten"];
const counted = (count: number, one: string) => `${words[count] ?? count} ${one}${count === 1 ? "" : "s"}`;
const capital = (text: string) => text[0].toUpperCase() + text.slice(1);

// Read with the page, again every 30 seconds and on returning to the tab, and after any change made through the
// workspace client (which marks every workspace read stale), so the sentence and the action follow the close.
export function useBrief(closeId: string) {
  return useQuery({ ...workspaceQuery<Brief>(`/${closeId}/brief`), refetchInterval: 30_000, refetchOnWindowFocus: true });
}

// What Overview counts for the person (Overview.tsx, needsFor): decisions (several approvals count one each), the
// brief's blockers no decision stands for, whether the close waits to be handed to the agent, whether the agent is
// preparing it, and, when the decisions could not be read or refreshed, a note that says so.
export type Counts = { decisions: number; blockers: number; handOver: boolean; preparing: boolean; stale: string };

// The sentence: what needs the person, counted in their units, or what the close waits on, or that it is ready. Under
// it one line: the due date, whose it is, its basis, and anything that could not be checked or refreshed.
export function CloseLede({ close, counts }: { close: Close & Plan; counts: Counts }) {
  const dates = useDates();
  const today = useToday();
  const read = useBrief(close.id);
  const brief = read.data;
  const lines = brief?.lines ?? [];
  const ready = signOffAvailable(brief);
  const waiting = lines.filter((l) => l.state === "waiting");
  const named = brief ? /Not checked: (.+?)\.?$/.exec(brief.headline)?.[1] : "";
  const notChecked = named || lines.filter((l) => l.state === "unknown").map((l) => l.area).join(", ");
  const { decisions, blockers } = counts;
  const sentence = !brief
    ? read.isError
      ? `${close.entity}’s close could not be read.`
      : `${close.entity}, ${monthName(close.period)}.`
    : brief.headline.startsWith("Signed off.")
      ? `${close.entity} is signed off.`
      : decisions && blockers
        ? `${capital(counted(decisions, "decision"))} and ${counted(blockers, "blocker")} need you.`
        : decisions
          ? `${capital(counted(decisions, "decision"))} need${decisions === 1 ? "s" : ""} you.`
          : // Before the hand-over, handing the close over comes first; its blockers are listed under it.
            counts.handOver
            ? `${monthName(close.period)} is ready to hand to the agent.`
            : blockers
              ? `${capital(counted(blockers, "blocker"))} need${blockers === 1 ? "s" : ""} you.`
              : ready
              ? `${close.entity} is ready to sign off.`
              : waiting.length
                ? `${close.entity} is waiting on ${waiting.map((l) => l.area).join(" and ")}.`
                : counts.preparing
                  ? `The agent is preparing ${monthName(close.period)}.`
                  : `${close.entity} could not be fully checked.`;
  const context = [
    close.due_on ? due(close.due_on, today, dates.calendarDay) : "No due date",
    close.priority && close.priority !== "normal" ? `${close.priority} priority` : "",
    close.owned_by_me ? "yours" : close.assigned ? "held by a colleague" : "",
    close.basis ? basisLabels[close.basis] : "",
    // What could not be checked is said here on every tab: the brief names it in its headline ("Not checked: …"), which
    // covers a check that failed inside an area that still needs the person as well as a section that could not be
    // read. So are decisions that could not be read (never counted as none) and a status that could not be refreshed.
    notChecked ? `not checked: ${notChecked}` : "",
    counts.stale,
    read.error && brief ? `not refreshed since ${dates.time(read.dataUpdatedAt)}` : "",
  ].filter(Boolean);
  return (
    <div className="ks-lede">
      <h1>{sentence}</h1>
      <p>{context.join(" · ")}</p>
    </div>
  );
}
const basisLabels = { cash: "cash basis", modified_cash: "modified cash basis", accrual: "accrual basis" };
