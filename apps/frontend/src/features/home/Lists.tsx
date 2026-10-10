import { Link } from "react-router";
import { Icon } from "@close/ui/components/Icon";
import { EntityCell, RowList } from "@close/ui/components/RowList";
import { SectionHeader } from "@close/ui/components/SectionHeader";
import { useDates } from "../../data/time";
import { NotOpened } from "../closes/Morning";
import type { HomeCounts } from "./Home";

// The lists Home's and Closes' counts open: the closes due or undated, the ended months without month-end books,
// and the closes owed through this month. Each shows only when it has rows, under the id its count scrolls to.

export const dueWords = (dueOn: string, today: string, day: (value: string) => string) => {
  if (!dueOn) return "";
  const date = day(dueOn);
  return dueOn < today ? `Overdue since ${date}` : `Due ${date}`;
};

// The deadline count's population: dated closes overdue or close, the months owed by then and not opened, and the
// outstanding closes with no date at all.
export function deadlineParts(counts: HomeCounts, today: string) {
  const dueUnopened = counts.not_opened.filter((m) => m.due_on && m.due_on <= new Date(Date.parse(today) + 7 * 86400000).toISOString().slice(0, 10));
  const undated = counts.owed.filter((item) => item.state !== "signed_off" && !item.due_on);
  const undatedUnopened = counts.not_opened.filter((m) => !m.due_on);
  return { dueUnopened, undated, undatedUnopened, any: counts.due.length + dueUnopened.length + undated.length + undatedUnopened.length > 0 };
}

export function DeadlinesSection({ counts, today }: { counts: HomeCounts; today: string }) {
  const dates = useDates();
  const { dueUnopened, undated, undatedUnopened, any } = deadlineParts(counts, today);
  if (!any) return null;
  return (
    <>
      <SectionHeader title="Deadlines" id="deadlines">
        <small>Overdue or due within a week and not signed off, and closes with no due date.</small>
      </SectionHeader>
      {counts.due.length + undated.length > 0 && (
        <RowList label="Due closes">
          {[...counts.due, ...undated].map((item) => (
            <Link className="row" key={item.close_id} to={`/dashboard/closes/${item.close_id}`}>
              <EntityCell name={item.entity} title={`${item.entity} · ${item.period}`} detail={item.due_on ? dueWords(item.due_on, today, dates.calendarDay) : "No due date"} />
              {!item.due_on ? <span className="status">No date</span> : item.due_on < today ? <span className="status danger">Overdue</span> : <span className="status warn">Due soon</span>}
              <small>{{ open: "Not signed off", reopen_required: "Changed since sign-off", unknown: "Sign-off could not be rechecked", signed_off: "Signed off" }[item.state]}</small>
              <Icon name="arrow" />
            </Link>
          ))}
        </RowList>
      )}
      <NotOpened months={[...dueUnopened, ...undatedUnopened]} due />
    </>
  );
}

export function UncapturedSection({ counts, today }: { counts?: HomeCounts; today: string }) {
  const dates = useDates();
  if (!counts || counts.uncaptured.length === 0) return null;
  return (
    <>
      <SectionHeader title="Books to capture" id="uncaptured">
        <small>Ended months with no usable month-end capture: capture them, or label historical exports.</small>
      </SectionHeader>
      <RowList label="Books to capture">
        {counts.uncaptured.map((item) => (
          <Link className="row" key={item.close_id} to={`/dashboard/closes/${item.close_id}?tab=evidence`}>
            <EntityCell name={item.entity} title={`${item.entity} · ${item.period}`} detail={item.due_on ? dueWords(item.due_on, today, dates.calendarDay) : "No due date"} />
            <span className="status warn">No month-end books</span>
            <span />
            <Icon name="arrow" />
          </Link>
        ))}
      </RowList>
    </>
  );
}

export function OwedSection({ counts, today }: { counts?: HomeCounts; today: string }) {
  const dates = useDates();
  if (!counts || (counts.owed.length === 0 && counts.not_opened.length === 0)) return null;
  return (
    <details className="owed" id="owed">
      <summary>
        Closes owed through this month · {counts.signed_off.done} of {counts.signed_off.total} signed off
      </summary>
      <RowList label="Closes owed">
        {counts.owed.map((item) => (
          <Link className="row" key={item.close_id} to={`/dashboard/closes/${item.close_id}`}>
            <EntityCell name={item.entity} title={`${item.entity} · ${item.period}`} detail={item.due_on ? dueWords(item.due_on, today, dates.calendarDay) : "No due date"} />
            <span className={item.state === "signed_off" ? "status ok" : item.state === "open" ? "status warn" : "status danger"}>
              {{ signed_off: "Signed off", open: "Not signed off", reopen_required: "Changed since sign-off", unknown: "Sign-off could not be rechecked" }[item.state]}
            </span>
            <span />
            <Icon name="arrow" />
          </Link>
        ))}
        {counts.not_opened.map((m) => (
          <div className="row" key={`${m.entity}:${m.period}`}>
            <EntityCell name={m.entity} title={`${m.entity} · ${m.period}`} detail={m.due_on ? dueWords(m.due_on, today, dates.calendarDay) : "No due date"} />
            <span className="status danger">Not opened</span>
            <span />
            <span />
          </div>
        ))}
      </RowList>
    </details>
  );
}
