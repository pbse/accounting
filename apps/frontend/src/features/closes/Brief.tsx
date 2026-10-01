import { useEffect, useState } from "react";
import { Link } from "react-router";
import { workspaceRequest } from "./workspace";
import { RowList } from "@close/ui/components/RowList";
import { SectionHeader } from "@close/ui/components/SectionHeader";

type Line = { area: string; state: "done" | "attention" | "waiting" | "info" | "unknown"; text: string; tab?: string };
const tones: Record<Line["state"], string> = { done: "ok", attention: "warn", waiting: "info", info: "info", unknown: "danger" };
const words: Record<Line["state"], string> = { done: "Done", attention: "Needs you", waiting: "Waiting", info: "Note", unknown: "Not checked" };

export function Brief({ closeId }: { closeId: string }) {
  const [brief, setBrief] = useState<{ headline: string; lines: Line[]; at: Date }>();
  const [error, setError] = useState("");
  const [asked, setAsked] = useState(0);
  useEffect(() => {
    let live = true;
    workspaceRequest(`/${closeId}/brief`)
      .then((data) => {
        if (!live) return;
        setBrief({ ...data, at: new Date() });
        setError("");
      })
      .catch((e) => live && setError(e instanceof Error ? e.message : "The close brief is unavailable."));
    return () => {
      live = false;
    };
  }, [closeId, asked]);
  if (!brief) return error ? <p role="alert">{error}</p> : null;
  return (
    <section aria-label="Where this close stands">
      <SectionHeader title="Where this close stands">
        <span>
          <small>As of {brief.at.toLocaleTimeString()}</small>{" "}
          <button type="button" onClick={() => setAsked((n) => n + 1)}>
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
