// "Agents at work" on the home page: four agents, shown as collaborators' cursors, prepare a bank reconciliation, a
// person signs it in blue, and the next client's workpaper follows. It holds still off screen, in a hidden tab, or
// when the visitor presses Pause; with reduced motion it stays on the finished first workpaper.
import { easeInOut, prefersReducedMotion } from "./noise";
import { WORKPAPERS, type Workpaper } from "./workpapers";

type Agent = "co" | "ma" | "in" | "pr" | "you";
const STAMP: Record<Exclude<Agent, "you">, number> = { co: 0, ma: 1, in: 2, pr: 3 };

export function startAgents(root: HTMLElement, pauseButton: HTMLButtonElement): { destroy(): void } {
  const $ = <T extends Element>(selector: string) => root.querySelector<T>(selector)!;
  const stage = $<HTMLElement>(".k-stage"), ledger = $<HTMLElement>(".k-ledger");
  const rows = [...root.querySelectorAll<HTMLElement>(".k-row")], diffRow = $<HTMLElement>(".k-diff"), rule = $<HTMLElement>(".k-dbl");
  const entry = $<HTMLElement>(".k-aje"), signature = $<HTMLElement>(".k-sig"), reviewed = $<HTMLElement>(".k-reviewed");
  const status = $<HTMLElement>(".k-status"), statusText = $<HTMLElement>(".k-status-text");
  const crew = [...root.querySelectorAll<HTMLElement>(".k-crew li")], odometer = $<HTMLElement>(".k-odo");
  const cursors = Object.fromEntries((["co", "ma", "in", "pr", "you"] as Agent[]).map((k) => [k, $<HTMLElement>(`[data-cursor="${k}"]`)])) as Record<Agent, HTMLElement>;
  const stamps = [...root.querySelectorAll<HTMLElement>(".k-who .k-mk")];
  const reduce = prefersReducedMotion();
  const position: Partial<Record<Agent, { x: number; y: number }>> = {};

  // The difference, as a mechanical counter: four whole digits with a comma after the first, then cents.
  const digits: HTMLElement[] = [], separators: HTMLElement[] = [];
  odometer.replaceChildren(...["d", ",", "d", "d", "d", ".", "d", "d"].map((t) => {
    const span = document.createElement("span");
    span.setAttribute("aria-hidden", "true");
    if (t === "d") {
      span.className = "k-dg";
      const strip = document.createElement("span");
      strip.className = "k-strip";
      strip.append(...[..."0123456789"].map((n) => Object.assign(document.createElement("span"), { textContent: n })));
      span.append(strip);
      digits.push(span);
    } else {
      span.className = "k-sep";
      span.textContent = t;
      separators.push(span);
    }
    return span;
  }));
  function setDifference(value: number) {
    const amount = Math.round(value * 100), text = String(amount).padStart(6, "0"), whole = Math.floor(amount / 100);
    digits.forEach((d, i) => ((d.firstChild as HTMLElement).style.transform = `translateY(${-Number(text[i])}em)`));
    digits[0].classList.toggle("is-off", whole < 1000); separators[0].classList.toggle("is-off", whole < 1000);
    digits[1].classList.toggle("is-off", whole < 100); digits[2].classList.toggle("is-off", whole < 10);
    odometer.setAttribute("aria-label", "Difference " + value.toLocaleString("en-US", { minimumFractionDigits: 2 }));
  }

  // A clock that stops while the section is off screen, the tab is hidden or the visitor pressed Pause.
  let run = 0, index = 0, started = false, userHold = false, inSight = false, held = false, heldAt = 0, heldTotal = 0, destroyed = false;
  const clock = () => (held ? heldAt : performance.now()) - heldTotal;
  function syncHold() {
    const hold = userHold || !inSight || document.hidden;
    if (hold === held) return;
    if (hold) { held = true; heldAt = performance.now(); }
    else { heldTotal += performance.now() - heldAt; held = false; }
    stage.classList.toggle("is-held", hold);
  }
  const timers = new Set<number>();
  const wait = (ms: number, id: number) => new Promise<void>((resolve, reject) => {
    const end = clock() + ms;
    const tick = () => {
      if (id !== run) return reject(new Error("stopped"));
      const left = end - clock();
      if (left <= 0) return resolve();
      const timer = window.setTimeout(() => { timers.delete(timer); tick(); }, Math.max(16, Math.min(150, left)));
      timers.add(timer);
    };
    tick();
  });
  function place(k: Agent, x: number, y: number) { position[k] = { x, y }; cursors[k].style.transform = `translate(${x.toFixed(1)}px,${y.toFixed(1)}px)`; }
  function glide(k: Agent, x: number, y: number, ms: number, id: number, wave?: (q: number) => number) {
    return new Promise<void>((resolve, reject) => {
      const from = position[k] ?? { x, y }, start = clock(), arc = Math.min(36, Math.hypot(x - from.x, y - from.y) * 0.12);
      const step = () => {
        if (id !== run) return reject(new Error("stopped"));
        const q = Math.min(1, (clock() - start) / ms), e = wave ? q : easeInOut(q);
        place(k, from.x + (x - from.x) * e, from.y + (y - from.y) * e + (wave ? wave(q) : -Math.sin(Math.PI * e) * arc));
        if (q < 1) requestAnimationFrame(step); else resolve();
      };
      requestAnimationFrame(step);
    });
  }
  function rel(element: Element) {
    const s = stage.getBoundingClientRect(), r = element.getBoundingClientRect();
    return { x: r.left - s.left, y: r.top - s.top, w: r.width, h: r.height };
  }
  function say(text: string, state?: "is-done" | "is-signed") { statusText.textContent = text; status.classList.remove("is-done", "is-signed"); if (state) status.classList.add(state); }
  const highlight = (k: Agent | null) => crew.forEach((li) => li.classList.toggle("is-on", li.dataset.agent === k));
  const hideCursors = () => Object.values(cursors).forEach((c) => c.classList.remove("is-on", "is-idle", "is-mini"));
  function fill(paper: Workpaper) {
    $<HTMLElement>(".k-lhead-title").textContent = paper.head;
    rows.forEach((row, i) => {
      const [label, tag, title, text, amount] = paper.rows[i];
      row.querySelector(".k-l")!.textContent = label;
      row.querySelector(".k-src-tag")!.textContent = tag;
      row.querySelector(".k-card b")!.textContent = title;
      row.querySelector(".k-card-text")!.textContent = text;
      row.querySelector(".k-amt")!.textContent = amount;
    });
    $<HTMLElement>(".k-aje-ref").textContent = `${paper.ajeRef} drafted, not posted.`;
    $<HTMLElement>(".k-aje-text").textContent = paper.aje;
    signature.textContent = paper.signer;
    $<HTMLElement>(".k-rev-meta").textContent = paper.meta;
    $<HTMLElement>(".k-prep-meta").textContent = paper.prepared;
  }
  function stamp(i: number, paper: Workpaper) {
    rows[i].classList.remove("is-pending");
    const value = paper.diff[i];
    if (value !== null) { diffRow.classList.remove("is-dim"); setDifference(value); }
  }
  function park(k: Exclude<Agent, "you">, id: number) {
    // An agent that has finished rests on its own initials on the Prepared line.
    const r = rel(stamps[STAMP[k]]);
    cursors[k].classList.add("is-mini");
    glide(k, r.x + r.w * 0.62, r.y + r.h * 0.72, 1000, id).then(() => cursors[k].classList.add("is-idle"), () => {});
  }
  function reset() {
    ledger.classList.add("is-instant");
    rows.forEach((r) => r.classList.add("is-pending"));
    diffRow.classList.add("is-dim", "is-unmarked");
    setDifference(WORKPAPERS[index].diff[1]);
    entry.classList.add("is-pending"); rule.classList.remove("is-on"); signature.classList.remove("is-on"); reviewed.classList.remove("is-on");
    hideCursors(); highlight(null); say("Opening the September close");
    void ledger.offsetWidth;
    ledger.classList.remove("is-instant");
  }
  async function sequence(id: number) {
    const paper = WORKPAPERS[index];
    reset();
    const width = stage.clientWidth, height = stage.clientHeight;
    const amount = (i: number) => { const r = rel(rows[i].querySelector(".k-amt")!); return [r.x + r.w - 6, r.y + r.h * 0.6] as const; };
    place("co", -60, 30); place("ma", -60, 120); place("in", -60, 210); place("pr", -60, 300); place("you", width + 40, height * 0.7);
    await wait(300, id);
    cursors.co.classList.add("is-on"); highlight("co"); say("Collector · gathering the statement and the ledger");
    for (const i of [0, 1]) { const [x, y] = amount(i); await glide("co", x, y, 850, id); stamp(i, paper); await wait(280, id); }
    park("co", id);
    cursors.ma.classList.add("is-on"); highlight("ma"); say(`Matcher · matching ${paper.lines} statement lines`);
    for (const i of [2, 3]) { const [x, y] = amount(i); await glide("ma", x, y, 900, id); stamp(i, paper); await wait(320, id); }
    park("ma", id);
    cursors.in.classList.add("is-on"); highlight("in"); say("Investigator · tracing what didn't match");
    const source = rows[4].querySelector(".k-src")!, s = rel(source);
    await glide("in", s.x + s.w * 0.55, s.y + s.h * 0.7, 950, id);
    stamp(4, paper); source.classList.add("is-peek");
    await wait(1700, id);
    source.classList.remove("is-peek");
    park("in", id);
    cursors.pr.classList.add("is-on"); highlight("pr"); say(`Preparer · drafting ${paper.ajeRef} and ruling off`);
    const r = rel(rule);
    await glide("pr", r.x + 2, r.y - 2, 850, id);
    diffRow.classList.remove("is-unmarked"); rule.classList.add("is-on");
    await glide("pr", r.x + r.w - 4, r.y - 2, 800, id, () => 0);
    entry.classList.remove("is-pending");
    await wait(700, id);
    say("Ready for your review", "is-done"); park("pr", id);
    await wait(700, id);
    cursors.you.classList.add("is-on"); highlight("you");
    const line = rel(signature);
    await glide("you", line.x + 4, line.y + line.h * 0.6, 1100, id);
    signature.classList.add("is-on");
    await glide("you", line.x + line.w, line.y + line.h * 0.5, 1600, id, (q) => Math.sin(q * Math.PI * 7) * 5 * (1 - q * 0.4));
    reviewed.classList.add("is-on"); say("Reviewed and signed", "is-signed");
    await wait(600, id);
    cursors.you.classList.add("is-idle");
  }
  async function loop() {
    const id = ++run;
    try {
      for (;;) {
        await sequence(id);
        await wait(4500, id);
        ledger.classList.add("is-turning"); hideCursors(); highlight(null);
        await wait(480, id);
        index = (index + 1) % WORKPAPERS.length;
        fill(WORKPAPERS[index]); reset();
        ledger.classList.remove("is-turning");
        await wait(400, id);
      }
    } catch {
      // A newer run, or leaving the page, stopped this one.
    }
  }

  // Start from the finished first workpaper, whatever an earlier mount left behind.
  fill(WORKPAPERS[0]);
  rows.forEach((r) => r.classList.remove("is-pending"));
  diffRow.classList.remove("is-dim", "is-unmarked");
  entry.classList.remove("is-pending"); rule.classList.add("is-on"); signature.classList.add("is-on"); reviewed.classList.add("is-on");
  hideCursors(); highlight(null); say("Reviewed and signed", "is-signed");
  setDifference(0);
  pauseButton.hidden = reduce;
  const onPause = () => {
    userHold = !userHold;
    pauseButton.textContent = userHold ? "Play" : "Pause";
    pauseButton.setAttribute("aria-pressed", String(userHold));
    syncHold();
  };
  pauseButton.addEventListener("click", onPause);
  const sight = new IntersectionObserver((entries) => {
    inSight = entries[0].isIntersecting;
    syncHold();
    if (inSight && !started && !reduce && !destroyed) { started = true; void loop(); }
  }, { threshold: 0.35 });
  sight.observe(stage);
  document.addEventListener("visibilitychange", syncHold);
  return {
    destroy() {
      destroyed = true;
      run++;
      timers.forEach((t) => clearTimeout(t));
      sight.disconnect();
      pauseButton.removeEventListener("click", onPause);
      document.removeEventListener("visibilitychange", syncHold);
    },
  };
}
