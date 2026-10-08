// Both apps' layout in a real browser, on every page, at desktop and phone width: each page renders inside the shell
// with its header, the top bar and page column sit where the design system puts them, nothing widens the page on a
// phone, and the firm app's shell takes no style from the public site's stylesheet (docs/design-system.md).
//
// CI runs it when the front end changes. Locally: run the API and both apps' dev servers, then
//   WORKSPACE_TOKEN=… DATABASE_URL=<the API's database> node scripts/layout-check.mjs
// FRONTEND_URL, ADMIN_URL and API_URL default to the dev servers' ports; CHROME_PATH picks a browser other than Chrome.
import { execFileSync } from "node:child_process";
import { chromium } from "playwright-core";

const frontend = process.env.FRONTEND_URL ?? "http://127.0.0.1:5173";
const admin = process.env.ADMIN_URL ?? "http://127.0.0.1:5174/admin";
const api = process.env.API_URL ?? "http://127.0.0.1:8080";
const token = process.env.WORKSPACE_TOKEN;
if (!token) throw new Error("Set WORKSPACE_TOKEN to the API's workspace token");

async function call(path, body) {
  const response = await fetch(api + path, {
    method: body ? "POST" : "GET",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    body: body && JSON.stringify(body),
  });
  if (!response.ok) throw new Error(`${path} answered ${response.status}: ${await response.text()}`);
  return response.json();
}

// Synthetic data so the pages that show a close, a workpaper, a firm, a plan or a run have one, reusing what an earlier
// check created. A firm otherwise only comes from the sign-in provider, so the check adds one straight to the database.
const database = process.env.DATABASE_URL;
if (!database) throw new Error("Set DATABASE_URL to the API's database, where the check adds a synthetic firm");
const firm = "org_layoutcheck";
const sql = (statement) => execFileSync("psql", [database, "-qAt", "-v", "ON_ERROR_STOP=1", "-c", statement], { encoding: "utf8" }).trim();
sql(`INSERT INTO organizations(id,name) VALUES('${firm}','Layout check firm') ON CONFLICT (id) DO NOTHING`);
const plan =
  // Only a staff draft (or a rejected one) opens in the editor, so only such a plan is reused.
  sql(`SELECT id FROM workflow_plans WHERE organization_id='${firm}' AND source='staff' AND state IN ('draft','rejected') AND NOT is_template ORDER BY id LIMIT 1`) ||
  (
    await call("/v1/admin/plans", {
      firm,
      template: false,
      reason: "A plan for the browser layout check",
      plan: { version: 1, title: "Layout check", nodes: [{ id: "tell", activity: "plan_signal", input: { name: "layout_check" } }] },
    })
  ).plan_id;
const close = (await call("/v1/closes", { entity: "Harbor Dental Group", period: "2026-09" })).id;
const paper =
  (await call(`/v1/closes/${close}`)).workpapers.find((p) => p.kind === "prepaid")?.id ??
  (
    await call(`/v1/closes/${close}/workpapers`, {
      kind: "prepaid",
      input: {
        entity: "Harbor Dental Group", source_reference: "Synthetic invoice 001, page 1", amount: "12000.00", currency: "USD", basis: "accrual",
        service_start: "2026-09-01", service_end: "2027-08-31", close_period: "2026-09", expense_account: "Software expense",
        prepaid_account: "Prepaid expense", method: "equal_full_months_v1", method_confirmed: true, booking_confirmed: true, examples_confirmed: true,
      },
    })
  ).id;
const run =
  (await call("/v1/admin/workflows/runs")).runs[0]?.id ??
  (await call("/v1/admin/workflows/runs", { workflow: "chat_expiry", organization: "", input: {}, reason: "Layout check" })).id;

const apps = [
  {
    name: "firm app",
    start: `${frontend}/dashboard/closes`,
    unlock: "Unlock workspace",
    prefix: "",
    // Matched against the end of Vite's data-vite-dev-id, which is the stylesheet's absolute file path.
    stylesheet: "/apps/frontend/src/styles.css",
    routes: [
      "/dashboard", "/dashboard/work", "/dashboard/closes", `/dashboard/closes/${close}`, `/dashboard/closes/${close}/workpapers/${paper}`, `/dashboard/closes/${close}?tab=reports`, "/dashboard/measure", `/dashboard/closes/${close}?tab=books`, "/dashboard/executive", `/dashboard/closes/${close}?tab=cash`,
      "/dashboard/settings", "/dashboard/settings?tab=notifications", "/dashboard/settings?tab=connections", "/dashboard/settings?tab=firm", "/dashboard/settings?tab=people", "/dashboard/settings?tab=clients", "/dashboard/settings?tab=policies", "/dashboard/settings?tab=security", "/dashboard/close", "/dashboard/review",
      ...["prepaids", "settlements", "reconciliations", "accruals", "loans", "owners"].map((kind) => `/dashboard/${kind}/new?close=${close}`),
    ],
  },
  // A client's request page stands alone, without the top bar. A request really sent needs a client's preparation work
  // behind it, so the page's one read is answered in the browser with an open request: its questions, form and footer.
  {
    name: "client request page",
    start: `${frontend}/request#LAYOUTCHECKREQUESTLINKAAAAAAAA`,
    standalone: true,
    // The page counts as the request only once the answer form shows, not an error or an empty state.
    ready: ".request-page textarea",
    stylesheet: "/apps/frontend/src/styles.css",
    fixtures: {
      "**/v1/client-request": {
        status: "open",
        open: true,
        entity: "Harbor Dental Group",
        period: "2026-09",
        expires_at: new Date(Date.now() + 7 * 86400000).toISOString(),
        body: "Hello,\n\nWe are closing September and need two things from you. Thank you.",
        questions: [
          { position: 1, text: "What was the $4,800 payment to Northwind Supplies on September 14 for?", state: "", answer: "" },
          { position: 2, text: "Please send the September statement for the operating account ending 4421.", state: "awaiting_document", answer: "" },
          { position: 3, text: "Did the new lease start on September 1?", state: "answered", answer: "Yes, the lease started on September 1." },
        ],
      },
    },
    routes: [""],
  },
  {
    name: "staff console",
    start: `${admin}/`,
    unlock: "Unlock",
    prefix: new URL(admin).pathname,
    routes: [
      "/", "/firms", `/firms/${firm}`, "/people", "/audit", "/workflows", "/runs", `/runs/${run}`, "/runs/new",
      "/plans", `/plans/${plan}`, `/plans/${plan}/edit`, "/plans/new", "/jobs", "/connections", "/contact-notes",
    ],
  },
];
const widths = [
  { name: "desktop", viewport: { width: 1440, height: 900 } },
  { name: "phone", viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true },
];

// Every computed style inside the shell, element by element.
const styles = () =>
  [...document.querySelectorAll(".app-shell, .app-shell *")].map((element) => {
    const style = getComputedStyle(element);
    return [element.tagName.toLowerCase() + (element.className && typeof element.className === "string" ? "." + element.className.trim().replace(/\s+/g, ".") : ""), [...style].map((name) => `${name}: ${style.getPropertyValue(name)}`)];
  });

// A plan's editor shows only under a content grant for the firm (or once the plan is revealed), so the check opens a
// grant as a person would, reveals the plan if the page still asks, and waits for the editor itself.
async function revealPlan(page) {
  const reason = "Browser layout check of the plan editor";
  // Act on what the editor shows until it opens. The grants list loads after the page, so it can offer a grant the
  // staff member already holds (from the desktop pass); opening another would replace that grant and close a plan
  // already revealed under it. So a grant is opened only when the API says none is held for the check's firm.
  const deadline = Date.now() + 30000;
  const holding = async () => ((await call("/v1/admin/grants")).grants ?? []).some((g) => g.organization === firm);
  let asked = 0, opened = false;
  while (Date.now() < deadline) {
    const state = await page.evaluate(() => {
      const main = document.querySelector(".app-main");
      if ([...main.querySelectorAll("button")].some((b) => b.textContent.includes("Add a step"))) return "open";
      const summaries = [...main.querySelectorAll("summary")].map((s) => s.textContent);
      if (summaries.some((t) => t.includes("Open a content grant"))) return "grant";
      if (summaries.some((t) => t.includes("Reveal client content"))) return "reveal";
      return "waiting";
    });
    if (state === "open") return;
    if (state === "grant" && !opened && !(await holding())) {
      // Open one grant, and wait for it to exist before looking again, so a slow answer is not submitted twice.
      const grant = page.locator(".app-main details", { hasText: "Open a content grant" });
      await grant.evaluate((details) => (details.open = true));
      await grant.locator("input").fill(reason);
      await grant.locator('button:has-text("Open grant")').click();
      opened = true;
      const until = Date.now() + 15000;
      while (Date.now() < until && !(await holding())) await page.waitForTimeout(250);
    } else if (state !== "waiting" && Date.now() - asked > 5000) {
      // A grant is held but not yet shown or used (the console's grants list may be loading, or its request failed):
      // give the automatic reveal a moment, then reveal with a reason, which needs no grant.
      if (!asked) asked = Date.now();
      else {
        const reveal = page.locator(".app-main details", { hasText: "Reveal client content" });
        await reveal.evaluate((details) => (details.open = true));
        await reveal.locator("input").fill(reason);
        await reveal.locator('button:has-text("Reveal client content")').click();
        asked = Date.now();
      }
    }
    await page.waitForTimeout(250);
  }
  throw new Error("the plan editor did not open");
}

const browser = await chromium.launch(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : { channel: "chrome" });
const failures = [];
let pages = 0;
for (const app of apps) {
  for (const width of widths) {
    const page = await browser.newPage(width);
    const errors = [];
    page.on("pageerror", (error) => errors.push(error.message));
    for (const [pattern, json] of Object.entries(app.fixtures ?? {})) await page.route(pattern, (route) => route.fulfill({ json }));
    // The token lives in the tab's memory, so a reload (the dev server's first dependency optimisation) asks again.
    const unlock = async () => {
      await page.fill('input[name="token"]', token);
      await page.click(`button:has-text("${app.unlock}")`);
      await page.waitForSelector(".app-shell .topbar");
    };
    await page.goto(app.start);
    if (app.unlock) await unlock();
    for (const route of app.routes) {
      const where = `${app.name} ${route} (${width.name})`;
      const fail = (message) => failures.push(`${where}: ${message}`);
      // The route counts as rendered once its header replaces the previous page's (a new element or a new title).
      if (!app.standalone) await page.evaluate((path) => {
        const target = new URL(path, location.href);
        const same = target.pathname + target.search === location.pathname + location.search;
        const previous = same ? null : document.querySelector(".page-header h1");
        window.__layoutPrevious = previous && { element: previous, title: previous.textContent };
        history.pushState({}, "", path);
        dispatchEvent(new PopStateEvent("popstate"));
      }, app.prefix + route);
      try {
        const header = () =>
          page.waitForFunction(
            () => {
              const h1 = document.querySelector(".page-header h1");
              const previous = window.__layoutPrevious;
              return h1?.textContent?.trim() && (!previous || h1 !== previous.element || h1.textContent !== previous.title);
            },
            null,
            { timeout: 15000 },
          );
        await Promise.race([header(), page.waitForSelector('input[name="token"]', { timeout: 15000 })]);
        if (await page.locator('input[name="token"]').count()) {
          await unlock();
          await header();
        }
      } catch {
        fail("no page header");
        continue;
      }
      if (app.ready) {
        try {
          await page.waitForSelector(app.ready, { timeout: 15000 });
        } catch {
          fail(`the page never showed ${app.ready}`);
          continue;
        }
      }
      if (route.endsWith("/edit")) {
        try {
          await revealPlan(page);
        } catch {
          fail("the plan editor did not open after a content grant and a reveal");
          continue;
        }
      }
      await page.waitForLoadState("networkidle");
      await page.mouse.move(0, width.viewport.height - 1);
      const box = await page.evaluate(() => {
        const rect = (selector) => document.querySelector(selector)?.getBoundingClientRect().toJSON();
        return { topbar: rect(".topbar"), rail: rect(".rail"), main: rect(".app-main"), scrollWidth: document.documentElement.scrollWidth };
      });
      if (box.scrollWidth > width.viewport.width) fail(`the page is ${box.scrollWidth}px wide, wider than the ${width.viewport.width}px screen`);
      // The agent panel opens beside the page on a desktop and as a sheet on a phone, with its composer in view, and
      // widens nothing: empty on the dashboard, and with a card (the close's prepare card, answered without a model)
      // on a close. A running step list needs a model, so it is covered by the agent's event tests, not here.
      const panelRoute = route === "/dashboard" ? "empty" : route === `/dashboard/closes/${close}` ? "card" : "";
      if (panelRoute && !app.standalone) {
        try {
          await page.click(".topbar .ask-agent", { timeout: 5000 });
          await page.waitForSelector("aside.assistant", { timeout: 5000 });
          // The drawer slides in over 180ms; it is measured once it has settled.
          await page.waitForTimeout(400);
          if (panelRoute === "card") {
            // The conversation may already hold a card from an earlier run; the new command must add one.
            const before = await page.locator("aside.assistant .assistant-card").count();
            await page.fill('textarea[aria-label="Message the agent"]', "/prepare");
            await page.keyboard.press("Enter");
            await page.waitForFunction((n) => document.querySelectorAll("aside.assistant .assistant-card").length > n, before, { timeout: 15000 });
            await page.waitForTimeout(400);
          }
          const panel = await page.evaluate(() => {
            const rect = (selector) => document.querySelector(selector)?.getBoundingClientRect().toJSON();
            const cards = [...document.querySelectorAll("aside.assistant .assistant-card")].map((c) => c.getBoundingClientRect().toJSON());
            return { aside: rect("aside.assistant"), field: rect(".assistant-field"), cards, scrollWidth: document.documentElement.scrollWidth };
          });
          if (panel.scrollWidth > width.viewport.width) fail(`with the agent open the page is ${panel.scrollWidth}px wide`);
          if (!(panel.aside?.right <= width.viewport.width + 1 && panel.aside?.left >= 0)) fail("the agent panel runs off the screen");
          if (width.name === "phone" && panel.aside?.width !== width.viewport.width) fail(`the agent sheet is ${panel.aside?.width}px wide on a phone, not the screen`);
          if (width.name === "desktop" && !(panel.aside?.width >= 360 && panel.aside?.width <= 420)) fail(`the agent drawer is ${panel.aside?.width}px wide on a desktop`);
          if (!(panel.field?.bottom <= width.viewport.height && panel.field?.top >= 0)) fail("the agent's composer is not in view");
          if (panelRoute === "card") {
            if (panel.cards.length === 0) fail("the close's prepare card did not render in the agent panel");
            for (const card of panel.cards) if (!(card.left >= panel.aside.left && card.right <= panel.aside.right + 1)) fail("a card runs outside the agent panel");
          }
          await page.click('aside.assistant header button[aria-label="Close the agent"]:visible', { timeout: 5000 });
          await page.waitForSelector("aside.assistant", { state: "detached", timeout: 5000 });
        } catch (e) {
          fail(`the agent panel could not be opened and closed: ${e.message}`);
        }
      }
      if (app.standalone) {
        if (box.topbar) fail("a standalone page shows the top bar");
      } else {
        // The top bar is one row on a desktop and at most two (the brand and tools, then the navigation) on a phone.
        const rows = width.name === "desktop" ? 1 : 2;
        if (!(box.topbar?.height <= 72 * rows)) fail(`the top bar is ${box.topbar?.height}px tall, more than ${rows === 1 ? "one row" : "two rows"}`);
        if (box.topbar?.width !== width.viewport.width) fail(`the top bar is ${box.topbar?.width}px wide, not the ${width.viewport.width}px screen`);
        if (!(box.main?.top >= box.topbar?.bottom)) fail("the page column does not start below the top bar");
        // The rail stands at the left of the page column on a desktop and gives way on a phone.
        if (width.name === "desktop") {
          if (!(box.rail?.width >= 48 && box.rail?.width <= 72)) fail(`the rail is ${box.rail?.width}px wide, not a narrow pill`);
          if (!(box.main?.left >= box.rail?.right)) fail("the page column does not start after the rail");
        } else if (box.rail?.width) fail(`the rail shows on a phone, ${box.rail.width}px wide`);
      }
      // The shell stands alone: emptying the public site's stylesheet changes nothing inside it. Styles are read with
      // the sheet, without it and with it again; only what both readings with it agree on counts, so a page still
      // loading is not taken for a leak, and a page that keeps changing is read again.
      if (app.stylesheet) {
        const toggle = (path) => {
          const sheet = [...document.querySelectorAll("style[data-vite-dev-id]")].find((style) => style.dataset.viteDevId.endsWith(path));
          if (!sheet) return false;
          if (sheet.dataset.saved === undefined) {
            sheet.dataset.saved = sheet.textContent;
            sheet.textContent = "";
          } else {
            sheet.textContent = sheet.dataset.saved;
            delete sheet.dataset.saved;
          }
          return true;
        };
        let leaks = [];
        for (let attempt = 0; attempt < 3; attempt++) {
          const before = await page.evaluate(styles);
          if (!(await page.evaluate(toggle, app.stylesheet))) {
            fail(`${app.stylesheet} is not loaded as a dev-server stylesheet; run the check against the dev servers`);
            break;
          }
          const without = await page.evaluate(styles);
          await page.evaluate(toggle, app.stylesheet);
          const again = await page.evaluate(styles);
          let settled = before.length === again.length;
          leaks = [];
          before.forEach(([label, values], i) =>
            values.forEach((value, j) => {
              if (value !== again[i]?.[1][j]) settled = false;
              else if (value !== without[i]?.[1][j]) leaks.push(`${label} { ${value} } without it: ${without[i]?.[1][j]}`);
            }),
          );
          if (settled || !leaks.length) break;
          await page.waitForTimeout(1000);
        }
        if (leaks.length) fail(`the shell takes ${leaks.length} style(s) from ${app.stylesheet}, for example ${leaks.slice(0, 3).join("; ")}`);
      }
      pages += 1;
    }
    if (errors.length) failures.push(`${app.name} (${width.name}): page errors: ${errors.join(" | ")}`);
    await page.close();
  }
}
// The public site, as a visitor moves through it: from the home page, each public link must open its own page, the
// one a direct load shows (never the 404 page), without throwing.
{
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  const heading = () => page.evaluate(() => document.querySelector("#main h1")?.textContent?.trim() ?? "");
  const headingOf = async (url) => {
    await page.goto(url);
    await page.waitForFunction(() => document.querySelector("#main h1")?.textContent?.trim(), null, { timeout: 10000 }).catch(() => {});
    return heading();
  };
  const missing = await headingOf(`${frontend}/layout-check-missing-page`);
  for (const path of ["/contact", "/security", "/privacy", "/about", "/login"]) {
    const expected = await headingOf(frontend + path);
    if (!expected || expected === missing) {
      failures.push(`public site ${path}: a direct load does not open the page (heading "${expected}")`);
      continue;
    }
    await page.goto(`${frontend}/`);
    await page.waitForLoadState("networkidle");
    errors.length = 0;
    try {
      await page.locator(`a[href="${path}"]`).first().click({ timeout: 10000 });
      await page.waitForFunction((want) => document.querySelector("#main h1")?.textContent?.trim() === want, expected, { timeout: 10000 });
    } catch {
      failures.push(`public site ${path}: clicking its link from the home page did not open it (heading "${await heading()}")`);
    }
    if (errors.length) failures.push(`public site ${path}: page errors after clicking its link: ${errors.join(" | ")}`);
    pages += 1;
  }
  await page.close();
}
await browser.close();
console.log(`Checked ${pages} page views across both apps at desktop and phone width.`);
if (failures.length) {
  console.error(failures.join("\n"));
  process.exit(1);
}
