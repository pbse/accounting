import "./DotChart.css";

export type DotPoint = { label: string; value: number | null; note?: string };

// The design system's dot columns: one series over time as 6px dots on a 7.2px pitch, each point a few columns wide,
// filled from the bottom against the largest value, over a full grid of empty dots (the empty dots are the grid, so
// there are no gridlines). A point with no value stays empty. Values are written with the caller's format, the same one
// its numbers use elsewhere on the page; the label states every point for screen readers.
const PITCH = 7.2;
const ROWS = 20;
const COLUMNS_PER_POINT = 5;

export function DotChart({ label, points, format }: { label: string; points: DotPoint[]; format: (value: number) => string }) {
  const max = Math.max(0, ...points.map((p) => p.value ?? 0));
  const width = points.length * COLUMNS_PER_POINT * PITCH;
  const height = ROWS * PITCH;
  const described = points.map((p) => `${p.label} ${p.value === null ? "none" : format(p.value)}${p.note ? ` (${p.note})` : ""}`).join(", ");
  return (
    <figure className="dot-chart">
      <span className="dot-chart-scale" aria-hidden="true">
        {format(max)}
      </span>
      <svg viewBox={`0 0 ${width.toFixed(1)} ${height.toFixed(1)}`} width={width.toFixed(1)} height={height.toFixed(1)} role="img" aria-label={`${label}: ${described}`}>
        {points.flatMap((point, i) => {
          const on = max > 0 && point.value !== null ? Math.round((point.value / max) * ROWS) : 0;
          return Array.from({ length: COLUMNS_PER_POINT * ROWS }, (_, k) => {
            const column = i * COLUMNS_PER_POINT + Math.floor(k / ROWS);
            const row = k % ROWS;
            return <circle key={`${i}-${k}`} className={row < on ? "on" : undefined} cx={(column * PITCH + PITCH / 2).toFixed(2)} cy={(height - row * PITCH - PITCH / 2).toFixed(2)} r={3} />;
          });
        })}
      </svg>
      <figcaption className="dot-chart-axis" aria-hidden="true">
        <span>{points[0]?.label}</span>
        <span>{points[points.length - 1]?.label}</span>
      </figcaption>
    </figure>
  );
}
