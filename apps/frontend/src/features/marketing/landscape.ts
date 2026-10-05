// The pen-and-ink landscape at the foot of every public page: ridges hatched in lines that follow the land, a low sun
// and grass on the plain. Drawn once for the canvas width; nothing moves.
import { fractalNoise, seeded, token, valueNoise } from "./noise";

export function drawLandscape(canvas: HTMLCanvasElement) {
  const box = canvas.getBoundingClientRect();
  if (!box.width) return;
  const W = box.width, H = box.height, dpr = Math.min(window.devicePixelRatio || 1, 2);
  canvas.width = Math.round(W * dpr);
  canvas.height = Math.round(H * dpr);
  const ctx = canvas.getContext("2d")!;
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, W, H);
  const paper = token(canvas, "--paper", "#fafaf8"), ink = token(canvas, "--ink", "#121316");
  const rnd = seeded(42), nx = (x: number) => (x / W) * 1200;
  const ridges = [
    (x: number) => {
      const u = x / W;
      let y = H * 0.34 - H * 0.17 * fractalNoise(nx(x) * 0.004 + 3, 1.7) - H * 0.06 * fractalNoise(nx(x) * 0.018 + 9, 4.1);
      const t = Math.abs(u - 0.7) / 0.11;
      if (t < 1) y -= H * 0.17 * (t < 0.55 ? 1 : 1 - Math.pow((t - 0.55) / 0.45, 1.4)) * (0.94 + 0.06 * valueNoise(nx(x) * 0.08, 2));
      return y;
    },
    (x: number) => H * 0.54 - H * 0.12 * fractalNoise(nx(x) * 0.0065 + 20, 5.5) - H * 0.03 * valueNoise(nx(x) * 0.03, 8),
    (x: number) => H * 0.72 - H * 0.025 * fractalNoise(nx(x) * 0.01 + 40, 2.2),
  ];
  {
    // A low sun, engraved in horizontal lines.
    const cx = W * 0.16, cy = H * 0.13, rr = Math.min(H * 0.08, 26);
    ctx.save();
    ctx.beginPath(); ctx.arc(cx, cy, rr, 0, Math.PI * 2); ctx.clip();
    ctx.strokeStyle = ink; ctx.globalAlpha = 0.75;
    for (let y = cy - rr + 1; y < cy + rr; y += 3.2) {
      const q = (y - (cy - rr)) / (2 * rr);
      ctx.lineWidth = Math.max(0.35, 1.1 * q + 0.3);
      ctx.beginPath(); ctx.moveTo(cx - rr, y); ctx.lineTo(cx + rr, y); ctx.stroke();
    }
    ctx.restore();
  }
  const base = [0.24, 0.34, 0.1], spacing = [2.7, 3.1, 4.2], flat = [H * 0.3, H * 0.2, H * 0.12], widths = [0.35, 0.6, 0.85, 1.15];
  ridges.forEach((ridge, li) => {
    ctx.beginPath(); ctx.moveTo(0, H);
    for (let x = 0; x <= W + 2; x += 2) ctx.lineTo(x, ridge(x));
    ctx.lineTo(W, H); ctx.closePath(); ctx.fillStyle = paper; ctx.fill();
    let minY = H, mean = 0, count = 0;
    for (let x = 0; x <= W; x += 4) { const y = ridge(x); minY = Math.min(minY, y); mean += y; count++; }
    mean /= count;
    const strokes: number[][] = [[], [], [], []];
    for (let k = 1; ; k++) {
      const off = k * spacing[li] * (1 + k * 0.004 * li);
      if (minY + off > H + 2) break;
      const bl = Math.min(1, off / flat[li]), b2 = bl * bl * (3 - 2 * bl);
      let px: number | null = null, py = 0;
      for (let x = -3; x <= W + 3; x += 3) {
        const ry = ridge(x), y = ry + off + (mean + off - (ry + off)) * b2;
        if (y < ry + 0.6 || y > H + 1) { px = null; continue; }
        if (px !== null) {
          const slope = (y - py) / 3, n = fractalNoise(x * 0.022 + li * 5, y * 0.06 + k * 0.02), depth = off / H;
          const shade = base[li] + slope * 2.6 + (n - 0.5) * 1.05 - depth * 0.35 + (k < 3 ? 0.28 : 0), gate = valueNoise(x * 0.07 + k * 1.7, k * 0.93);
          if (shade > 0.16 && gate < 0.2 + shade * 0.85) strokes[shade > 0.92 ? 3 : shade > 0.62 ? 2 : shade > 0.38 ? 1 : 0].push(px, py, x, y);
        }
        px = x; py = y;
      }
    }
    ctx.strokeStyle = ink; ctx.globalAlpha = 0.82; ctx.lineCap = "round";
    strokes.forEach((s, i) => {
      ctx.lineWidth = widths[i]; ctx.beginPath();
      for (let j = 0; j < s.length; j += 4) { ctx.moveTo(s[j], s[j + 1]); ctx.lineTo(s[j + 2], s[j + 3]); }
      ctx.stroke();
    });
    ctx.globalAlpha = 1; ctx.lineWidth = li === 2 ? 0.7 : 0.95; ctx.beginPath();
    for (let x = 0; x <= W + 2; x += 2) { const y = ridge(x); if (x) ctx.lineTo(x, y); else ctx.moveTo(x, y); }
    ctx.stroke();
  });
  // Grass on the plain, larger toward the viewer.
  const plain = ridges[2];
  ctx.strokeStyle = ink; ctx.globalAlpha = 0.8; ctx.lineWidth = 0.7; ctx.beginPath();
  for (let i = 0, n = Math.round(W / 11); i < n; i++) {
    const x = rnd() * W, top = plain(x), t = Math.pow(rnd(), 0.6), y = top + 8 + (H - top - 8) * t, size = 3 + t * 13, blades = 6 + Math.floor(rnd() * 8);
    for (let j = 0; j < blades; j++) {
      const a = Math.PI * (0.12 + 0.76 * rnd()), l = size * (0.45 + 0.65 * rnd()), bx = x + (rnd() - 0.5) * size * 0.35;
      ctx.moveTo(bx, y); ctx.quadraticCurveTo(bx + Math.cos(a) * l * 0.35, y - Math.sin(a) * l * 0.7, bx + Math.cos(a) * l, y - Math.sin(a) * l);
    }
  }
  ctx.stroke();
  ctx.globalAlpha = 1;
}
