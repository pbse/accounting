import assert from "node:assert/strict";
import test from "node:test";
import { pageResponse, serveWorkspace, serverApi, workspacePage, type WorkspaceDeps } from "./workspace.ts";

const shell = '<!doctype html><html><head></head><body><div id="root"></div><script type="module" src="/assets/index.js"></script></body></html>';
const request = (cookie?: string) => new Request("https://getkrith.com/dashboard/settings?tab=people", { headers: cookie ? { Cookie: cookie } : {} });

function deps(overrides: Partial<WorkspaceDeps> = {}): WorkspaceDeps {
  return {
    enabled: true,
    render: async () => ({ kind: "page", status: 200, html: "<main>Pat Lee</main>", queryState: '{"queries":[]}' }),
    asset: async () => new Response(shell, { headers: { "Content-Type": "text/html" } }),
    call: async () => new Response("{}"),
    ...overrides,
  };
}

test("only workspace paths are rendered", () => {
  assert.equal(workspacePage("/dashboard"), true);
  assert.equal(workspacePage("/dashboard/settings"), true);
  assert.equal(workspacePage("/dashboards"), false);
  assert.equal(workspacePage("/login"), false);
});

test("without a cookie, or with rendering off, the browser renders the page itself", async () => {
  let rendered = 0;
  const counting = deps({ render: async () => ((rendered += 1), { kind: "signed-out" }) });
  assert.equal(await (await serveWorkspace(request(), counting)).text(), shell);
  assert.equal(await (await serveWorkspace(request("krith_session=a"), { ...counting, enabled: false })).text(), shell);
  assert.equal(rendered, 0);
});

test("a rendered page carries its data, its cache and a policy only its own scripts pass", async () => {
  const response = await serveWorkspace(request("krith_session=a"), deps());
  const html = await response.text();
  assert.match(html, /<div id="root"><main>Pat Lee<\/main><\/div><script id="krith-query" type="application\/json">\{"queries":\[\]\}<\/script>/);
  assert.equal(response.headers.get("Cache-Control"), "private, no-store");
  assert.equal(response.headers.get("Vary"), "Cookie");
  assert.match(response.headers.get("Content-Security-Policy") ?? "", /^script-src 'self' 'nonce-[A-Za-z0-9+/=]{24}'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'$/);
  assert.equal(response.headers.get("X-Frame-Options"), "DENY");
});

test("each render gets its own nonce, and the render receives it", async () => {
  const seen: string[] = [];
  const capturing = deps({ render: async (_r, _api, nonce) => (seen.push(nonce), { kind: "page", status: 200, html: "", queryState: "{}" }) });
  const first = await serveWorkspace(request("c=1"), capturing);
  const second = await serveWorkspace(request("c=1"), capturing);
  assert.notEqual(seen[0], seen[1]);
  assert.ok(first.headers.get("Content-Security-Policy")?.includes(`'nonce-${seen[0]}'`));
  assert.ok(second.headers.get("Content-Security-Policy")?.includes(`'nonce-${seen[1]}'`));
});

test("signed out, a failed render or an unexpected page falls back to the browser's own render", async () => {
  for (const render of [async () => ({ kind: "signed-out" as const }), async () => { throw new Error("boom"); }]) {
    const logged: string[] = [];
    const response = await serveWorkspace(request("c=1"), deps({ render, log: (m) => logged.push(m) }));
    assert.equal(await response.text(), shell);
    assert.ok(logged.every((m) => !m.includes("Pat")));
  }
  assert.equal(pageResponse("<html><body></body></html>", { status: 200, html: "x", queryState: "{}" }, "n"), undefined);
});

test("a loader's own response, such as a redirect, is returned as it is", async () => {
  const redirect = new Response(null, { status: 302, headers: { Location: "/login" } });
  const response = await serveWorkspace(request("c=1"), deps({ render: async () => ({ kind: "response", response: redirect }) }));
  assert.equal(response.status, 302);
  assert.equal(response.headers.get("Location"), "/login");
});

test("reads go to the API with the person's cookie and refuse as the API refuses", async () => {
  const calls: string[] = [];
  const api = serverApi(async (path, cookie) => {
    calls.push(`${path} ${cookie}`);
    if (path === "/v1/closes/members") return new Response(JSON.stringify({ message: "Not yours." }), { status: 403 });
    if (path === "/v1/auth/session") return new Response(null, { status: 401 });
    return new Response(JSON.stringify({ ok: true }));
  }, "krith_session=a");
  assert.deepEqual(await api.read("/me"), { ok: true });
  await assert.rejects(api.read("/members"), (error: Error & { status?: number }) => error.message === "Not yours." && error.status === 403);
  assert.deepEqual(await api.session(), { user: null });
  assert.deepEqual(calls, ["/v1/closes/me krith_session=a", "/v1/closes/members krith_session=a", "/v1/auth/session krith_session=a"]);
});
