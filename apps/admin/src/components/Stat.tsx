import { StatTile } from "@close/ui/components/StatTile";

// A staff stat: a read-only tile whose note, when given a tone, reads as a status.
export function Stat({ label, value, note, tone }: { label: string; value: number | string; note?: string; tone?: "ok" | "warn" | "danger" }) {
  return <StatTile label={label} value={value} caption={note && (tone ? <span className={`status ${tone}`}>{note}</span> : note)} />;
}
