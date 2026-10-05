// The home page's plate: a month of transactions settles into a skyline drawn in ledger lines, then the work carries
// on. Transactions keep arriving, Krith prepares one client at a time, and decisions appear in blue and are taken by
// people. Every figure and name here is illustrative. With reduced motion, or while paused, it is a still drawing.
import { clamp, easeInOut, fractalNoise, prefersReducedMotion, seeded, token } from "./noise";

type Ask = { client: string; ask: string; amt: string };
const DECISIONS: Ask[] = [
  { client: "Harbor Street Dental", ask: "Approve AJE-3", amt: "490.00 to repairs and maintenance" },
  { client: "Alder & Finch Architects", ask: "Send the September request", amt: "2 receipts outstanding" },
  { client: "Northgate Logistics", ask: "Approve the freight accrual", amt: "3,120.00" },
  { client: "Mesa Physical Therapy", ask: "Owner transfer: draw or loan?", amt: "12,000.00 on 18 Sep" },
  { client: "Copperline Studio", ask: "Sign off the September close", amt: "Every figure traced" },
  { client: "Pine Row Veterinary", ask: "Approve the payroll accrual", amt: "8,940.00" },
  { client: "Lumen Coffee Roasters", ask: "Release the management pack", amt: "Commentary drafted for you" },
];
const CLIENTS = ["Fairway Landscaping", "Bayside Orthodontics", "Kestrel Freight", "Willow & Oak Interiors", "Summit Physio", "Blue Fern Bakery", "Ridgeline Electrical", "Orchard Street Clinic", "Cobalt Print Works", "Hollis & Grant Legal", "Tidewater Marine", "Juniper Yoga"];
const JOBS = [
  { job: "Bank reconciliation, operating", done: "Ties to the statement", ask: "Review 2 unmatched items", amt: "Operating account" },
  { job: "September accruals", done: "Accruals prepared", ask: "Approve the accrual", amt: "3,120.00" },
  { job: "Prepaid schedule", done: "Rolled forward, nothing new", ask: "Approve AJE-4", amt: "1,200.00 to software" },
  { job: "Payroll tie-out", done: "Ties to the provider report", ask: "Approve the payroll accrual", amt: "8,940.00" },
  { job: "Equipment loan schedule", done: "Interest recalculated", ask: "Approve the interest entry", amt: "412.50" },
  { job: "Management pack", done: "Pack drafted", ask: "Release the management pack", amt: "Commentary drafted for you" },
  { job: "Thirteen-week cash plan", done: "Plan updated", ask: "Review a cash shortfall", amt: "Week of 30 Nov" },
  { job: "Client request", done: "Nothing missing", ask: "Send the client request", amt: "2 receipts outstanding" },
];
const REVIEWERS = ["M. Alvarez", "J. Okafor", "R. Chen", "S. Patel"];
export const TRANSACTIONS = 18412;
export const ILLUSTRATIVE_DECISIONS = DECISIONS;

export type CityElements = {
  hero: HTMLElement;
  canvas: HTMLCanvasElement;
  copy: HTMLElement;
  card: HTMLElement;
  cardClient: HTMLElement;
  cardAsk: HTMLElement;
  cardAmount: HTMLElement;
  tag: HTMLElement;
  tagTitle: HTMLElement;
  tagClient: HTMLElement;
  tagLine: HTMLElement;
  transactions: HTMLElement;
  decisions: HTMLElement;
};
export type City = { setPaused(paused: boolean): void; destroy(): void };

type Window_ = { x: number; y: number; w: number; h: number; r: number; c: number };
type Building = { id: number; x: number; w: number; top: number; peak: number; k: number; wins: Window_[]; client?: string };
type Segment = {
  x: number; y: number; len: number; th: number; k: number; b: number;
  sx: number; sy: number; vx: number; vy: number; a0: number; dash: number; ph: number; cv: number; dly: number; dur: number; done: boolean;
};
type Decision = { x: number; y: number; w: number; h: number; bid: number; d: Ask; delay: number; born: number; gone: number; by: string };
type Particle = { x: number; y: number; vx: number; vy: number; age: number; drift: number; fx: number | null; fy: number; tx: number; ty: number; s: Segment | null };
type BuildingSpec = {
  x: number; w: number; h: number; k: number; d: number; side?: number; setbacks?: number; crown?: boolean; roof?: boolean;
  win?: { wx: number; ww: number; wy: number; wh: number; wl: number };
};

const ROW = 4, CX = 2;
const fmtInt = (n: number) => n.toLocaleString("en-US");
const verb = (ask: string) => (/^Send/.test(ask) ? "Sent" : /^Release/.test(ask) ? "Released" : /^Sign/.test(ask) ? "Signed off" : /^Review/.test(ask) ? "Reviewed" : /^Owner/.test(ask) ? "Answered" : "Approved");
const alphaOf = (k: number) => (k === 1 ? 0.55 : k === 4 ? 0.3 : 1);

export function startCity(el: CityElements): City {
  const reduce = prefersReducedMotion();
  const ctx = el.canvas.getContext("2d")!;
  let W = 0, H = 0, dpr = 1, gw = 0, gh = 0, topY = 0, lastW = 0;
  let dens = new Float32Array(0), kind = new Uint8Array(0), own = new Int16Array(0);
  let B: Building[] = [], segs: Segment[] = [], byB = new Map<number, number[]>(), dec: Decision[] = [];
  let cache = document.createElement("canvas"), cctx = cache.getContext("2d")!;
  let ink = "#121316", ink2 = "#55575e", blue = "#1f3fbf", paper = "#fafaf8";
  let phase: "idle" | "settle" | "live" | "still" = "idle";
  let t0 = 0, raf = 0, inView = true, hover = -1, settled = 0, last = 0, safety = 0, shownOpen = -1, txCount = TRANSACTIONS, txDirty = false, userPaused = false, destroyed = false;
  let amb: Particle[] = [], flashes: { s: Segment; t: number }[] = [];
  const work = { stage: "wait" as "wait" | "scan" | "rest", t: 0, b: null as Building | null, job: JOBS[0], start: 0, doneAt: 0, resolved: false, prev: [] as number[], n: 0, c: 0, r: 0 };

  function measure(): number | null {
    const r = el.hero.getBoundingClientRect();
    if (!r.width) return null;
    W = r.width; H = r.height; dpr = Math.min(window.devicePixelRatio || 1, 2);
    el.canvas.width = Math.round(W * dpr); el.canvas.height = Math.round(H * dpr);
    cache = document.createElement("canvas"); cache.width = el.canvas.width; cache.height = el.canvas.height;
    cctx = cache.getContext("2d")!; cctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    gw = Math.ceil(W / CX); gh = Math.ceil(H / ROW);
    dens = new Float32Array(gw * gh); kind = new Uint8Array(gw * gh); own = new Int16Array(gw * gh).fill(-1);
    const cr = el.copy.getBoundingClientRect();
    topY = cr.bottom - r.top + 16;
    ink = token(el.hero, "--ink", ink); ink2 = token(el.hero, "--ink-2", ink2); blue = token(el.hero, "--blue", blue); paper = token(el.hero, "--paper", paper);
    return cr.width / 2;
  }
  const rowsOf = (y0: number, y1: number) => [Math.max(0, Math.ceil((y0 - 2) / ROW)), Math.min(gh, Math.ceil((y1 - 2) / ROW))];
  const colsOf = (x0: number, x1: number) => [Math.max(0, Math.ceil((x0 - 1) / CX)), Math.min(gw, Math.ceil((x1 - 1) / CX))];
  function rect(x0: number, y0: number, x1: number, y1: number, d: number, k: number, b: number) {
    const [r0, r1] = rowsOf(y0, y1), [c0, c1] = colsOf(x0, x1);
    for (let r = r0; r < r1; r++) for (let c = c0, o = r * gw; c < c1; c++) { dens[o + c] = d; kind[o + c] = k; own[o + c] = b; }
  }

  function generate(copyHalf: number) {
    const rnd = seeded(20261004), R = (a: number, b: number) => a + (b - a) * rnd(), RI = (a: number, b: number) => Math.floor(a + (b - a + 1) * rnd());
    B = []; dec = []; hover = -1; el.card.classList.remove("is-on");
    const cityH = Math.max(170, H - topY), half = W / 2, free = copyHalf + 28;
    // How tall a building may be: under the copy it stays below it; beside the copy it may rise.
    const cap = (x0: number, x1: number) => {
      const a = x0 - half, b = x1 - half, near = a < 0 && b > 0 ? 0 : Math.min(Math.abs(a), Math.abs(b));
      if (near <= free || half - free < 80) return cityH;
      const t = Math.min(1, (near - free) / (half - free));
      return cityH + (H * 0.8 - cityH) * t * t * (3 - 2 * t);
    };
    function building(o: BuildingSpec): Building {
      const id = B.length, top = H - o.h, x1 = o.x + o.w, b: Building = { id, x: o.x, w: o.w, top, peak: top, k: o.k, wins: [] };
      B.push(b);
      rect(o.x, top, x1, H, o.d, o.k, id);
      const sd = Math.min(0.97, o.d + 0.18);
      if (o.side) rect(x1 - o.side, top, x1, H, sd, o.k, id);
      if (o.win) {
        const { wx, ww, wy, wh, wl } = o.win, m = Math.max(4, Math.round(wx * 0.6)), rTop = rowsOf(top, H)[0];
        for (let r = rTop + 2; r < gh - 1; r++) {
          const rb = gh - 1 - r;
          if (rb % wy >= wh) continue;
          for (let wxp = o.x + m; wxp + ww <= x1 - m; wxp += wx) {
            if (o.side && wxp >= x1 - o.side) continue;
            const [c0, c1] = colsOf(wxp, wxp + ww);
            for (let c = c0; c < c1; c++) { const i = r * gw + c; dens[i] = o.d * wl; kind[i] = o.k; own[i] = id; }
            if (rb % wy === wh - 1) b.wins.push({ x: wxp, y: r * ROW, w: ww, h: wh * ROW, r, c: clamp(Math.floor((wxp + ww / 2 - 1) / CX), 0, gw - 1) });
          }
        }
      }
      if (o.k >= 2) rect(o.x, top, x1, top + ROW, Math.min(0.97, o.d + 0.22), o.k, id);
      let pk = top, xa = o.x, xb = x1;
      for (let s = 0; s < (o.setbacks ?? 0); s++) {
        const ins = RI(3, 6); xa += ins; xb -= ins;
        if (xb - xa < 10) break;
        const th = RI(3, 6) * ROW; rect(xa, pk - th, xb, pk, o.d * (1 - 0.06 * (s + 1)), o.k, id); pk -= th;
      }
      if (o.crown) {
        const ac = Math.round((xa + xb) / 2), ah = RI(5, 9) * ROW, cd = Math.min(0.95, o.d + 0.15);
        rect(ac - 1, pk - ah, ac + 1, pk, cd, o.k, id);
        if (xb - xa > 16) rect(ac - 7, pk - ah + 2 * ROW, ac - 5, pk, cd, o.k, id);
        pk -= ah;
      }
      if (o.roof && o.w > 44) {
        const td = Math.min(0.95, o.d + 0.12);
        if (rnd() < 0.7) {
          const tx = RI(o.x + 6, x1 - 22);
          rect(tx, top - 2 * ROW, tx + 2, top, td, o.k, id); rect(tx + 12, top - 2 * ROW, tx + 14, top, td, o.k, id);
          rect(tx - 1, top - 5 * ROW, tx + 15, top - 2 * ROW, td, o.k, id); rect(tx + 3, top - 6 * ROW, tx + 11, top - 5 * ROW, td, o.k, id);
          pk = Math.min(pk, top - 6 * ROW);
        }
        if (rnd() < 0.5) { const ax = RI(o.x + 4, x1 - 14); rect(ax, top - 2 * ROW, ax + 10, top, td * 0.85, o.k, id); }
      }
      b.peak = pk;
      return b;
    }
    // Clouds: long faint streaks toward the upper corners.
    for (let r = 0; r < gh; r++) {
      const y = r * ROW + 2;
      if (y < 100 || y > topY + 30) continue;
      for (let c = 0; c < gw; c++) {
        const side = clamp((Math.abs(c / gw - 0.5) * 2 - 0.52) / 0.48, 0, 1);
        if (side && (fractalNoise(c * CX * 0.004, y * 0.03) - 0.56) * 3 * side > 0.1) { const i = r * gw + c; dens[i] = 0.1; kind[i] = 4; }
      }
    }
    for (let x = -10; x < W; ) { const w = RI(14, 34); building({ x, w, h: cap(x, x + w) * R(0.42, 0.9), k: 1, d: R(0.1, 0.16), setbacks: rnd() < 0.3 ? 1 : 0, crown: rnd() < 0.2 }); x += w + RI(2, 10); }
    for (let x = -20; x < W; ) { const w = RI(28, 58); building({ x, w, h: cap(x, x + w) * R(0.26, 0.6), k: 2, d: R(0.26, 0.36), win: { wx: 12, ww: 4, wy: 4, wh: 1, wl: 0 }, side: rnd() < 0.6 ? 10 : 0, setbacks: rnd() < 0.35 ? RI(1, 2) : 0, crown: rnd() < 0.25 }); x += w + RI(10, 30); }
    const lw = W < 700 ? 34 : 46;
    building({ x: Math.round(W / 2 - lw / 2), w: lw, h: Math.max(60, Math.min(cityH * 0.68, cityH - 120)), k: 2, d: 0.4, win: { wx: 10, ww: 4, wy: 4, wh: 1, wl: 0 }, side: 10, setbacks: 3, crown: true });
    for (let x = -30; x < W; ) { const w = RI(52, 104); building({ x, w, h: Math.min(cap(x, x + w), cityH * 1.5) * R(0.13, 0.3), k: 3, d: R(0.5, 0.62), win: { wx: rnd() < 0.5 ? 12 : 14, ww: 6, wy: 5, wh: 2, wl: 0 }, side: RI(12, 20), roof: true }); x += w + RI(3, 16); }
    if (W >= 1000) {
      const rw = Math.round(W * 0.075), rx = W - rw;
      building({ x: rx, w: rw + 10, h: cap(rx, W) * 0.96, k: 3, d: 0.7, win: { wx: 14, ww: 6, wy: 5, wh: 2, wl: 0 }, side: 18 });
      building({ x: -10, w: Math.round(W * 0.12), h: H * 0.17, k: 3, d: 0.62, win: { wx: 14, ww: 6, wy: 5, wh: 2, wl: 0 }, side: 16, roof: true });
    }
    // Seven windows become the decisions left for people, spread across the city.
    const cands: { wn: Window_; bid: number }[] = [];
    for (const b of B) {
      if (b.k < 2 || !b.wins.length || b.x < 28 || b.x + b.w > W - 28) continue;
      const vis = b.wins.filter((wn) => wn.y > topY + 70 && wn.y < H - 56 && own[wn.r * gw + wn.c] === b.id);
      if (vis.length) cands.push({ wn: vis[Math.floor(vis.length * 0.3)], bid: b.id });
      if (vis.length > 6 && W < 900) cands.push({ wn: vis[Math.floor(vis.length * 0.8)], bid: b.id });
    }
    cands.sort((a, b) => a.wn.x - b.wn.x);
    const want = Math.min(DECISIONS.length, cands.length), order = [0, 4, 2, 6, 1, 5, 3];
    for (let i = 0; i < want; i++) {
      const p = cands[Math.floor(((i + 0.5) * cands.length) / want)];
      const b = B[p.bid];
      b.client = b.client ?? DECISIONS[i].client;
      dec.push({ ...p.wn, bid: p.bid, d: { ...DECISIONS[i], client: b.client }, delay: order.indexOf(i) >= 0 ? order.indexOf(i) : i, born: 0, gone: 0, by: "" });
    }
    // Every run of ink becomes a segment, and every segment starts life as a loose transaction.
    segs = [];
    const sr = seeded(99);
    for (let r = 0; r < gh; r++) {
      const y = r * ROW + 2;
      for (let c = 0; c < gw; ) {
        const i = r * gw + c, d = dens[i];
        if (d < 0.06) { c++; continue; }
        const k = kind[i], dashed = k === 4, maxL = dashed ? 6 + Math.floor(sr() * 10) : 5 + Math.floor(sr() * 10), s = c;
        while (c < gw && c - s < maxL) { const j = r * gw + c; if (dens[j] < 0.06 || kind[j] !== k || Math.abs(dens[j] - d) > 0.09) break; c++; }
        segs.push({ x: s * CX, y, len: (c - s) * CX, th: Math.min(3, 0.35 + 2.6 * d), k, b: own[r * gw + s], sx: 0, sy: 0, vx: 0, vy: 0, a0: 0, dash: 0, ph: 0, cv: 0, dly: 0, dur: 0, done: false });
        if (dashed) c += 2 + Math.floor(sr() * 3);
      }
    }
    byB = new Map();
    segs.forEach((sg, i) => { if (sg.k >= 2 && sg.b >= 0) { const list = byB.get(sg.b) ?? []; list.push(i); byB.set(sg.b, list); } });
    const pr = seeded(5), span = H - topY * 0.55;
    for (const s of segs) {
      s.sx = pr() * W * 1.1 - W * 0.05; s.sy = topY * 0.55 + pr() * span;
      const a = -0.3 + (fractalNoise(s.sx * 0.003, s.sy * 0.004) - 0.5) * 2.4, sp = 14 + pr() * 24;
      s.vx = Math.cos(a) * sp; s.vy = Math.sin(a) * sp; s.a0 = a;
      s.dash = 3 + pr() * 5; s.ph = pr() * 6.28; s.cv = (pr() - 0.5) * 90;
      s.dly = 0.35 + clamp((H - s.y) / (H - topY * 0.8), 0, 1) * 1.6 + pr() * 0.6; s.dur = 1.15 + pr() * 0.5; s.done = false;
    }
  }

  function inkSeg(c: CanvasRenderingContext2D, s: Segment) { c.globalAlpha = alphaOf(s.k); c.fillRect(s.x, s.y - s.th / 2, s.len, s.th); }
  function bakeAll() { cctx.clearRect(0, 0, W, H); cctx.fillStyle = ink; for (const s of segs) { s.done = true; inkSeg(cctx, s); } cctx.globalAlpha = 1; settled = segs.length; }

  // A decision is blue while it waits for a person. Once decided it turns to ink and the name floats away.
  function drawDecisions(now: number): number {
    let open = 0;
    for (let i = 0; i < dec.length; i++) {
      const d = dec[i], q = clamp((now - d.born) / 600, 0, 1);
      if (q <= 0) continue;
      const cx = d.x + d.w / 2, cy = d.y + d.h / 2, e = easeInOut(q), pw = Math.max(9, d.w + 4), ph = Math.max(8, d.h + 1);
      if (!d.gone) {
        open++;
        ctx.globalAlpha = 1; ctx.fillStyle = paper; ctx.fillRect(cx - 8, cy - 7, 16, 14);
        ctx.fillStyle = blue; ctx.fillRect(cx - (pw * e) / 2, cy - (ph * e) / 2, pw * e, ph * e);
        ctx.strokeStyle = blue; ctx.lineWidth = 1; ctx.globalAlpha = 0.35 * e; ctx.beginPath(); ctx.arc(cx, cy, 11, 0, 6.2832); ctx.stroke();
        if (q < 1) { ctx.globalAlpha = 1 - q; ctx.lineWidth = 1.2; ctx.beginPath(); ctx.arc(cx, cy, 4 + q * 18, 0, 6.2832); ctx.stroke(); }
        if (i === hover) { ctx.globalAlpha = 1; ctx.lineWidth = 1.4; ctx.beginPath(); ctx.arc(cx, cy, 11, 0, 6.2832); ctx.stroke(); }
      } else {
        const g = clamp((now - d.gone) / 1700, 0, 1), m = clamp(g / 0.35, 0, 1), f = 1 - clamp((g - 0.6) / 0.4, 0, 1);
        ctx.globalAlpha = f; ctx.fillStyle = paper; ctx.fillRect(cx - 8, cy - 7, 16, 14);
        ctx.globalAlpha = f * (1 - m); ctx.fillStyle = blue; ctx.fillRect(cx - pw / 2, cy - ph / 2, pw, ph);
        ctx.globalAlpha = f * m; ctx.fillStyle = ink; ctx.fillRect(cx - pw / 2, cy - ph / 2, pw, ph);
        ctx.globalAlpha = (1 - m) * 0.35; ctx.strokeStyle = blue; ctx.lineWidth = 1; ctx.beginPath(); ctx.arc(cx, cy, Math.max(2, 11 - m * 8), 0, 6.2832); ctx.stroke();
        const ta = g < 0.12 ? g / 0.12 : g > 0.8 ? (1 - g) / 0.2 : 1;
        if (ta > 0.01) {
          ctx.font = `500 11.5px ${token(el.hero, "--sans", "sans-serif")}`; ctx.textAlign = "center"; ctx.textBaseline = "alphabetic";
          const ty = cy - 18 - g * 12, tw = ctx.measureText(d.by).width;
          ctx.globalAlpha = ta; ctx.fillStyle = paper; ctx.fillRect(cx - tw / 2 - 6, ty - 11, tw + 12, 15); ctx.fillStyle = blue; ctx.fillText(d.by, cx, ty);
        }
      }
    }
    ctx.globalAlpha = 1;
    const n = dec.length;
    dec = dec.filter((d) => !(d.gone && now - d.gone > 1700));
    if (dec.length !== n && hover >= 0) { hover = -1; el.card.classList.remove("is-on"); el.canvas.style.cursor = ""; }
    return open;
  }

  // Transactions keep arriving from the edges and land in the client Krith is working on.
  function spawn(p: Particle): Particle {
    const r = Math.random(), band = Math.max(40, H - topY - 70);
    if (r < 0.38) { p.x = -12; p.y = topY + 40 + Math.random() * band; p.vx = 30 + Math.random() * 34; p.vy = (Math.random() - 0.5) * 14; }
    else if (r < 0.76) { p.x = W + 12; p.y = topY + 40 + Math.random() * band; p.vx = -(30 + Math.random() * 34); p.vy = (Math.random() - 0.5) * 14; }
    else { p.x = Math.random() * W; p.y = H + 10; p.vx = (Math.random() - 0.5) * 24; p.vy = -(26 + Math.random() * 20); }
    p.age = 0; p.drift = 0.7 + Math.random() * 1.2; p.fx = null; p.s = null;
    return p;
  }
  function aim(p: Particle) {
    let s: Segment | null = null;
    const list = work.b && Math.random() < 0.7 ? byB.get(work.b.id) : undefined;
    if (list?.length) s = segs[list[Math.floor(Math.random() * list.length)]];
    for (let tries = 0; !s && tries < 40; tries++) { const c = segs[Math.floor(Math.random() * segs.length)]; if (c.k >= 2) s = c; }
    s = s ?? segs[0];
    p.s = s; p.tx = s.x + s.len * Math.random(); p.ty = s.y;
  }
  function drawAmbient(now: number, dt: number) {
    ctx.strokeStyle = ink2; ctx.lineWidth = 1; ctx.lineCap = "round"; ctx.globalAlpha = 0.7; ctx.beginPath();
    for (const p of amb) {
      p.age += dt;
      if (p.age < 0) continue;
      let x: number, y: number, a = Math.atan2(p.vy, p.vx);
      if (p.age < p.drift) { p.x += p.vx * dt; p.y += p.vy * dt; x = p.x; y = p.y; }
      else {
        if (p.fx === null) { p.fx = p.x; p.fy = p.y; aim(p); }
        const q = Math.min(1, (p.age - p.drift) / 1.05), e = easeInOut(q);
        x = p.fx + (p.tx - p.fx) * e; y = p.fy + (p.ty - p.fy) * e - Math.sin(Math.PI * e) * 20; a = a * (1 - e);
        if (q >= 1) { if (p.s) flashes.push({ s: p.s, t: now }); txCount++; txDirty = true; spawn(p); continue; }
      }
      const ca = (Math.cos(a) * 7) / 2, sa = (Math.sin(a) * 7) / 2;
      ctx.moveTo(x - ca, y - sa); ctx.lineTo(x + ca, y + sa);
    }
    ctx.stroke(); ctx.globalAlpha = 1; ctx.lineCap = "butt";
  }
  function drawFlashes(now: number) {
    if (!flashes.length) return;
    flashes = flashes.filter((f) => now - f.t < 450);
    ctx.fillStyle = ink;
    for (const f of flashes) { const k = 1 - (now - f.t) / 450, s = f.s, b = 1.5 * k; ctx.globalAlpha = k; ctx.fillRect(s.x, s.y - (s.th + b) / 2, s.len, s.th + b); }
    ctx.globalAlpha = 1;
  }

  // Every few seconds Krith works through one client: a reading line rises through the building, then the outcome.
  function clientOf(b: Building) { b.client = b.client ?? CLIENTS[work.c++ % CLIENTS.length]; return b.client; }
  function showTag(b: Building, title: string, line: string, mode: "" | "is-blue" | "is-ok") {
    el.tagTitle.textContent = title; el.tagClient.textContent = clientOf(b); el.tagLine.textContent = line;
    el.tag.className = "k-worktag is-on" + (mode ? " " + mode : "");
    const tw = el.tag.offsetWidth, th = el.tag.offsetHeight, x = b.x + b.w / 2, y = Math.max(b.peak, topY + th + 30), left = clamp(x - tw / 2, 12, W - tw - 12);
    el.tag.style.left = left + "px"; el.tag.style.top = y - th - 14 + "px"; el.tag.style.setProperty("--lx", clamp(x - left, 10, tw - 10) + "px");
  }
  const hideTag = () => el.tag.classList.remove("is-on");
  function freeWindow(b: Building): Window_ | null {
    const vis = b.wins.filter((wn) => wn.y > topY + 70 && wn.y < H - 56 && own[wn.r * gw + wn.c] === b.id && !dec.some((d) => d.x === wn.x && d.y === wn.y));
    return vis.length ? vis[Math.floor(Math.random() * vis.length)] : null;
  }
  function outcome(now: number) {
    const b = work.b!, open = dec.filter((d) => !d.gone).length, wn = freeWindow(b);
    work.doneAt = now;
    if (wn && open < 9 && (open < 6 || Math.random() < 0.45)) {
      dec.push({ ...wn, bid: b.id, born: now, gone: 0, by: "", delay: 0, d: { client: clientOf(b), ask: work.job.ask, amt: work.job.amt } });
      showTag(b, "1 decision for you", work.job.ask, "is-blue");
    } else showTag(b, "Prepared", work.job.done, "is-ok");
  }
  function resolveOne(now: number) {
    if (dec.filter((d) => !d.gone).length < 6) return;
    const d = dec.find((x, i) => !x.gone && i !== hover && now - x.born > 5000 && (!work.b || x.bid !== work.b.id));
    if (!d) return;
    d.gone = now; d.by = verb(d.d.ask) + " · " + REVIEWERS[work.r++ % REVIEWERS.length];
  }
  function stepWork(now: number, dt: number) {
    work.t += dt;
    if (work.stage === "wait") {
      if (work.t < 1) return;
      const tall = Math.min(150, (H - topY) * 0.45);
      const c = B.filter((b) => b.k >= 2 && b.wins.length && b.x >= W * 0.06 && b.x + b.w <= W * 0.94 && H - b.peak >= tall && (byB.get(b.id)?.length ?? 0) > 40 && !work.prev.includes(b.id));
      if (!c.length) { work.t = 0; return; }
      const b = c[Math.floor(Math.random() * c.length)];
      work.b = b; work.job = JOBS[work.n++ % JOBS.length]; work.prev.push(b.id);
      if (work.prev.length > 6) work.prev.shift();
      work.stage = "scan"; work.t = 0; work.start = now; showTag(b, "Preparing", work.job.job, "");
    } else if (work.stage === "scan") {
      if (work.t >= 2.6) { outcome(now); work.stage = "rest"; work.t = 0; work.resolved = false; }
    } else {
      if (!work.resolved && work.t >= 1.2) { resolveOne(now); work.resolved = true; }
      if (work.t >= 2.6) hideTag();
      if (work.t >= 3.2) { work.stage = "wait"; work.t = 0; work.b = null; }
    }
  }
  function drawScan(now: number) {
    const b = work.b;
    if (!b) return;
    const list = byB.get(b.id) ?? [];
    ctx.fillStyle = ink;
    if (work.stage === "scan") {
      const q = clamp((now - work.start) / 2600, 0, 1), from = Math.min(H, b.peak + Math.max(140, Math.min(240, (H - b.peak) * 0.7))), y = from - (from - b.peak) * easeInOut(q);
      for (const i of list) { const s = segs[i], dd = s.y - y; if (dd < -2 || dd > 16) continue; const bst = 1.4 * (1 - Math.max(0, dd) / 16); ctx.globalAlpha = 1; ctx.fillRect(s.x, s.y - (s.th + bst) / 2, s.len, s.th + bst); }
      ctx.globalAlpha = 0.6; ctx.fillRect(b.x - 10, y - 0.5, b.w + 20, 1);
      ctx.globalAlpha = 0.9; ctx.fillRect(b.x - 10, y - 2.5, 1, 5); ctx.fillRect(b.x + b.w + 9, y - 2.5, 1, 5);
    } else if (work.stage === "rest") {
      const k = 1 - (now - work.doneAt) / 700;
      if (k > 0) { ctx.globalAlpha = 0.5 * k; for (const i of list) { const s = segs[i]; ctx.fillRect(s.x, s.y - (s.th + 0.9) / 2, s.len, s.th + 0.9); } }
    }
    ctx.globalAlpha = 1;
  }

  function paintBase() { ctx.setTransform(1, 0, 0, 1, 0, 0); ctx.clearRect(0, 0, el.canvas.width, el.canvas.height); ctx.drawImage(cache, 0, 0); ctx.setTransform(dpr, 0, 0, dpr, 0, 0); }
  function frame(now: number) {
    raf = 0;
    const dt = last ? Math.min(0.05, (now - last) / 1000) : 0.016;
    last = now;
    paintBase();
    if (phase === "settle") {
      const T = (now - t0) / 1000, fade = Math.min(1, T / 0.35), buckets: number[][][] = [[], []];
      cctx.fillStyle = ink;
      for (const s of segs) {
        if (s.done) continue;
        let x: number, y: number, len: number, ang: number, th: number;
        if (T < s.dly) { x = s.sx + s.vx * T; y = s.sy + s.vy * T + Math.sin(T * 1.6 + s.ph) * 3; len = s.dash; ang = s.a0; th = 0.9; }
        else {
          const p = (T - s.dly) / s.dur;
          if (p >= 1) { s.done = true; settled++; inkSeg(cctx, s); continue; }
          const e = easeInOut(p), dx = s.sx + s.vx * s.dly, dy = s.sy + s.vy * s.dly + Math.sin(s.dly * 1.6 + s.ph) * 3, tx = s.x + s.len / 2, vx = tx - dx, vy = s.y - dy, L = Math.hypot(vx, vy) || 1, bend = Math.sin(Math.PI * e) * s.cv;
          x = dx + vx * e - (vy / L) * bend; y = dy + vy * e + (vx / L) * bend; len = s.dash + (s.len - s.dash) * Math.pow(e, 1.4); ang = s.a0 * (1 - e); th = 0.9 + (s.th - 0.9) * e;
        }
        const g = s.k === 1 || s.k === 4 ? 0 : 1, bk = Math.max(1, Math.round(th * 2)), arr = (buckets[g][bk] ??= []);
        const ca = (Math.cos(ang) * len) / 2, sa = (Math.sin(ang) * len) / 2;
        arr.push(x - ca, y - sa, x + ca, y + sa);
      }
      cctx.globalAlpha = 1;
      ctx.strokeStyle = ink; ctx.lineCap = "butt";
      buckets.forEach((group, g) => group.forEach((arr, bk) => {
        if (!arr?.length) return;
        ctx.globalAlpha = (g ? 0.95 : 0.5) * fade; ctx.lineWidth = bk / 2; ctx.beginPath();
        for (let j = 0; j < arr.length; j += 4) { ctx.moveTo(arr[j], arr[j + 1]); ctx.lineTo(arr[j + 2], arr[j + 3]); }
        ctx.stroke();
      }));
      ctx.globalAlpha = 1;
      el.transactions.textContent = fmtInt(Math.round((TRANSACTIONS * settled) / Math.max(1, segs.length)));
      if (settled >= segs.length) finishSettle(now);
    } else {
      const moving = phase === "live" && !reduce && !userPaused;
      if (moving) { stepWork(now, dt); drawScan(now); drawFlashes(now); }
      const open = drawDecisions(now);
      if (open !== shownOpen) { shownOpen = open; el.decisions.textContent = String(open); }
      if (moving) drawAmbient(now, dt);
      if (txDirty) { txDirty = false; el.transactions.textContent = fmtInt(txCount); }
    }
    if (phase !== "still" && inView && !document.hidden && !userPaused && !destroyed) raf = requestAnimationFrame(frame);
  }
  function finishSettle(now: number) {
    clearTimeout(safety); phase = "live";
    dec.forEach((d) => (d.born = now + 250 + d.delay * 230));
    txCount = TRANSACTIONS; el.transactions.textContent = fmtInt(TRANSACTIONS);
    work.stage = "wait"; work.t = -1.4;
  }
  function kick() { if (!raf && !destroyed && phase !== "still" && phase !== "idle" && inView && !document.hidden && !userPaused) { last = 0; raf = requestAnimationFrame(frame); } }
  function repaint() { paintBase(); drawDecisions(performance.now()); }
  function still() {
    bakeAll(); phase = "still"; dec.forEach((d) => (d.born = -1e9)); repaint();
    el.transactions.textContent = fmtInt(txCount); shownOpen = dec.filter((d) => !d.gone).length; el.decisions.textContent = String(shownOpen);
  }
  function build(): boolean {
    const half = measure();
    if (half === null) return false;
    generate(half);
    work.stage = "wait"; work.t = 0; work.b = null; work.prev = []; flashes = []; hideTag();
    amb = Array.from({ length: W < 700 ? 8 : 22 }, () => spawn({ x: 0, y: 0, vx: 0, vy: 0, age: 0, drift: 0, fx: null, fy: 0, tx: 0, ty: 0, s: null }));
    amb.forEach((p) => (p.age = -Math.random() * 3));
    return true;
  }
  function start() {
    if (destroyed || !build()) return;
    lastW = W;
    if (reduce || document.hidden) { still(); if (!reduce) phase = "live"; kick(); return; }
    cctx.clearRect(0, 0, W, H); settled = 0; phase = "settle"; t0 = performance.now();
    el.transactions.textContent = "0"; el.decisions.textContent = "0";
    safety = window.setTimeout(() => { if (phase === "settle") { bakeAll(); finishSettle(performance.now()); kick(); } }, 7500);
    kick();
  }
  function rebuild() {
    const w = el.hero.getBoundingClientRect().width;
    if (!w || w === lastW || phase === "idle") return;
    lastW = w;
    if (!build()) return;
    still();
    if (!reduce) { phase = "live"; kick(); }
  }

  // Decisions a visitor can point at.
  function nearest(e: PointerEvent): number {
    const r = el.canvas.getBoundingClientRect(), x = e.clientX - r.left, y = e.clientY - r.top;
    let best = -1, bd = 18;
    dec.forEach((d, i) => { if (d.gone) return; const dd = Math.hypot(d.x + d.w / 2 - x, d.y + d.h / 2 - y); if (dd < bd) { bd = dd; best = i; } });
    return best;
  }
  function showCard(i: number) {
    hover = i; el.canvas.style.cursor = i >= 0 ? "pointer" : "";
    if (i < 0) { el.card.classList.remove("is-on"); if (phase === "still" || userPaused) repaint(); return; }
    const d = dec[i];
    el.cardClient.textContent = d.d.client; el.cardAsk.textContent = d.d.ask; el.cardAmount.textContent = d.d.amt;
    const cw = el.card.offsetWidth, ch = el.card.offsetHeight, cx = d.x + d.w / 2, left = clamp(cx - cw / 2, 12, W - cw - 12);
    el.card.style.left = left + "px"; el.card.style.top = d.y - ch - 20 + "px"; el.card.style.setProperty("--lx", cx - left + "px");
    el.card.classList.add("is-on");
    if (phase === "still" || userPaused) repaint();
  }
  const onMove = (e: PointerEvent) => { if (phase === "settle" || phase === "idle") return; const i = nearest(e); if (i !== hover) showCard(i); };
  const onDown = (e: PointerEvent) => { if (phase === "settle" || phase === "idle") return; showCard(nearest(e)); };
  const onLeave = () => { if (hover >= 0) showCard(-1); };
  const onVisibility = () => { if (!document.hidden) { last = 0; kick(); } };
  let resizeTimer = 0;
  const onResize = () => { clearTimeout(resizeTimer); resizeTimer = window.setTimeout(rebuild, 180); };
  el.canvas.addEventListener("pointermove", onMove);
  el.canvas.addEventListener("pointerdown", onDown);
  el.canvas.addEventListener("pointerleave", onLeave);
  document.addEventListener("visibilitychange", onVisibility);
  window.addEventListener("resize", onResize);
  const sight = new IntersectionObserver((entries) => { inView = entries[0].isIntersecting; kick(); });
  sight.observe(el.hero);

  // The plate measures where the copy ends, so it starts once the fonts have settled.
  const fontsReady = document.fonts?.ready ?? Promise.resolve();
  const fallback = new Promise((resolve) => window.setTimeout(resolve, 1500));
  void Promise.race([fontsReady, fallback]).then(start);

  return {
    setPaused(p: boolean) {
      userPaused = p;
      if (p) {
        if (raf) { cancelAnimationFrame(raf); raf = 0; }
        if (phase === "settle") { bakeAll(); finishSettle(performance.now()); }
        const now = performance.now();
        dec.forEach((d) => { if (d.born > now) d.born = now - 1000; });
        dec = dec.filter((d) => !d.gone);
        hideTag(); flashes = [];
        if (phase !== "idle") repaint();
      } else kick();
    },
    destroy() {
      destroyed = true;
      if (raf) cancelAnimationFrame(raf);
      clearTimeout(safety); clearTimeout(resizeTimer);
      el.canvas.removeEventListener("pointermove", onMove);
      el.canvas.removeEventListener("pointerdown", onDown);
      el.canvas.removeEventListener("pointerleave", onLeave);
      document.removeEventListener("visibilitychange", onVisibility);
      window.removeEventListener("resize", onResize);
      sight.disconnect();
    },
  };
}
