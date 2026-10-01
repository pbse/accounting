import { useId, useMemo } from "react";
import { percents } from "./percents";
import "./HexMap.css";

export type HexPart = { label: string; value: number; tone: "blue" | "teal" | "orange" | "violet" };

// The design system's hex map: a whole split into up to four parts, drawn as clusters of cells over a field of empty
// ones, with a legend of each part's count and share. Geometry and clustering follow the design system (Tray.hexMap):
// pointy-top cells of 9px radius, 2px apart; every later part clusters around its own spot and the first part takes
// the rest of the filled cells. The same parts always draw the same picture.
const R = 9;
const HEX = Math.sqrt(3) * R;
const PX = HEX + 2;
const PY = (PX * Math.sqrt(3)) / 2;
const SPOTS = [[0, 0], [0.15, -0.08], [-0.16, 0.08], [0.01, 0.02]];
const CORNERS = Array.from({ length: 6 }, (_, a) => [(R - 1) * Math.cos((Math.PI / 180) * (60 * a - 90)), (R - 1) * Math.sin((Math.PI / 180) * (60 * a - 90))]);

function random(seed: number) {
  let s = seed % 2147483647 || 1;
  return () => {
    s = (s * 16807) % 2147483647;
    return (s - 1) / 2147483646;
  };
}

function layout(shares: number[], cols: number, rows: number) {
  const width = cols * PX + PX / 2;
  const height = (rows - 1) * PY + 2 * R;
  const rand = random(7);
  const cells: { x: number; y: number; d: number; k: number; part: number }[] = [];
  for (let r = 0; r < rows; r++) {
    for (let c = 0; c < cols; c++) {
      const x = c * PX + HEX / 2 + (r % 2 ? PX / 2 : 0);
      const y = r * PY + R;
      cells.push({ x, y, d: Math.hypot((x - width / 2) / (width * 0.4), (y - height / 2) / (height * 0.42)) + rand() * 0.38, k: 0, part: -1 });
    }
  }
  const filled = [...cells].sort((a, b) => a.d - b.d).slice(0, Math.round(cells.length * 0.42));
  for (let i = shares.length - 1; i > 0; i--) {
    const [sx, sy] = [width / 2 + SPOTS[i][0] * width, height / 2 + SPOTS[i][1] * height];
    const free = filled.filter((f) => f.part < 0);
    free.forEach((f) => (f.k = Math.hypot(f.x - sx, f.y - sy) + rand() * 14));
    free.sort((a, b) => a.k - b.k).slice(0, Math.round(shares[i] * filled.length)).forEach((f) => (f.part = i));
  }
  filled.forEach((f) => f.part < 0 && (f.part = 0));
  return { width, height, cells };
}

export function HexMap({ label, parts, cols = 23, rows = 16 }: { label: string; parts: HexPart[]; cols?: number; rows?: number }) {
  const gloss = `hex-gloss-${useId().replace(/[^a-zA-Z0-9]/g, "")}`;
  const total = parts.reduce((sum, p) => sum + p.value, 0);
  const shares = parts.map((p) => (total ? p.value / total : 0));
  const { width, height, cells } = useMemo(() => layout(shares, cols, rows), [shares.join(), cols, rows]);
  const share = percents(parts.map((p) => p.value));
  return (
    <figure className="hex-map">
      <svg viewBox={`0 0 ${width.toFixed(1)} ${height.toFixed(1)}`} role="img" aria-label={`${label}: ${parts.map((p, i) => `${p.label} ${p.value} (${share[i]}%)`).join(", ")}`}>
        <defs>
          <linearGradient id={gloss} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0" className="gloss-light" />
            <stop offset="0.55" className="gloss-clear" />
            <stop offset="1" className="gloss-shade" />
          </linearGradient>
        </defs>
        {cells.map((cell, i) => {
          const points = CORNERS.map(([dx, dy]) => `${(cell.x + dx).toFixed(2)},${(cell.y + dy).toFixed(2)}`).join(" ");
          return cell.part < 0 ? (
            <polygon key={i} className="empty" points={points} />
          ) : (
            <g key={i} className={parts[cell.part].tone}>
              <polygon points={points} />
              <polygon className="gloss" points={points} fill={`url(#${gloss})`} />
            </g>
          );
        })}
      </svg>
      <figcaption>
        <ul className="hex-legend">
          {parts.map((p, i) => (
            <li key={p.label}>
              <span className={`swatch ${p.tone}`} aria-hidden="true" />
              <span>{p.label}</span>
              <span className="pill neutral">{share[i]}%</span>
              <strong>{p.value}</strong>
            </li>
          ))}
        </ul>
      </figcaption>
    </figure>
  );
}
