import { useDates } from "../../data/time";
import { workspaceQuery } from "../../data/queries";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import { RowList } from "@close/ui/components/RowList";
import { SectionHeader } from "@close/ui/components/SectionHeader";

type Line = { area: string; state: "done" | "attention" | "waiting" | "info" | "unknown"; text: string; tab?: string };
const tones: Record<Line["state"], string> = { done: "ok", attention: "warn", waiting: "info", info: "info", unknown: "danger" };
const words: Record<Line["state"], string> = { done: "Done", attention: "Needs you", waiting: "Waiting", info: "Note", unknown: "Not checked" };

export function Brief({ closeId }: { closeId: string }) {
  const dates = useDates();
  // Read with the page and on Refresh; "As of" is when this answer was read, on the server or here.
  const read = useQuery(workspaceQuery<{ headline: string; lines: Line[] }>(`/${closeId}/brief`));
  const brief = read.data;
  const error = read.error ? read.error.message || "The close brief is unavailable." : "";
  if (!brief) return error ? <p role="alert">{error}</p> : null;
  return (
    <section aria-label="Where this close stands">
      <SectionHeader title="Where this close stands">
        <span>
          <small>As of {dates.time(read.dataUpdatedAt)}</small>{" "}
          <button type="button" disabled={read.isFetching} onClick={() => void read.refetch()}>
            Refresh
          </button>
        </span>
      </SectionHeader>
      {error && <p role="alert">{error}</p>}
      <p>
        <strong>{brief.headline}</strong>
      </p>
      <RowList>
        {brief.lines.map((line) => (
          <div className="row" key={line.area}>
            <span>
              <span className={`status ${tones[line.state]}`}>{words[line.state]}</span> <strong>{line.area[0].toUpperCase() + line.area.slice(1)}</strong>
            </span>
            <span>{line.text}</span>
            <span />
            {line.tab ? <Link to={`?tab=${line.tab}`}>Open</Link> : <span />}
          </div>
        ))}
      </RowList>
    </section>
  );
}
