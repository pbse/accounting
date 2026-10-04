import assert from "node:assert/strict";
import { test } from "node:test";
import { adminPage, adminPath, forwardsToAPI, secureAdmin, staffGate } from "./routes.ts";

test("MCP and its OAuth discovery reach the API; internal routes and assets do not", () => {
  for (const path of ["/healthz", "/healthz/worker", "/readyz", "/v1/closes", "/mcp", "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"]) {
    assert.ok(forwardsToAPI(path), path);
  }
  for (const path of ["/internal/email-reply", "/", "/dashboard", "/mcp/extra", "/.well-known/openid-configuration", "/.well-known/oauth-protected-resourcex"]) {
    assert.ok(!forwardsToAPI(path), path);
  }
});

test("admin pages load the admin app; its files and other paths do not", () => {
  for (const path of ["/admin", "/admin/", "/admin/firms", "/admin/firms/org_1/runs"]) {
    assert.ok(adminPage(path), path);
  }
  for (const path of ["/admin/assets/index-1a2b.js", "/admin/favicon.svg", "/administrator", "/dashboard", "/"]) {
    assert.ok(!adminPage(path), path);
  }
});

test("the admin app runs only its own scripts and cannot be framed", async () => {
  const response = secureAdmin(new Response("<!doctype html>", { status: 200, headers: { "Content-Type": "text/html" } }));
  assert.equal(response.headers.get("X-Frame-Options"), "DENY");
  assert.match(response.headers.get("Content-Security-Policy") ?? "", /frame-ancestors 'none'/);
  assert.match(response.headers.get("Content-Security-Policy") ?? "", /script-src 'self'/);
  assert.equal(response.headers.get("Content-Type"), "text/html");
  assert.equal(await response.text(), "<!doctype html>");
});

test("everything under /admin is the console's; nothing else is", () => {
  for (const path of ["/admin", "/admin/", "/admin/firms", "/admin/index.html", "/admin/assets/index-1a2b.js"]) assert.ok(adminPath(path), path);
  for (const path of ["/administrator", "/admin.html", "/dashboard", "/v1/admin/me", "/"]) assert.ok(!adminPath(path), path);
});

test("the console opens only for a signed-in staff member the API accepts", async () => {
  const asked: Request[] = [];
  const answering = (status: number) => async (check: Request) => {
    asked.push(check);
    return new Response(null, { status });
  };
  const page = new Request("https://app.example.test/admin/firms?tab=people", { headers: { Cookie: "__Host-close_staff=abc", "Sec-Fetch-Site": "cross-site" } });

  // Signed in with platform:read: through, the API asked about the cookies alone.
  assert.equal(await staffGate(page, "server", answering(200)), undefined);
  assert.equal(new URL(asked[0].url).pathname, "/v1/admin/me");
  assert.equal(asked[0].headers.get("Cookie"), "__Host-close_staff=abc");
  assert.equal(asked[0].headers.get("Sec-Fetch-Site"), null);

  // Signed out: a page goes to staff sign-in and comes back; a file is refused.
  const signIn = await staffGate(page, "server", answering(401));
  assert.equal(signIn?.status, 302);
  const location = new URL(signIn!.headers.get("Location")!);
  assert.equal(location.pathname, "/v1/auth/login");
  assert.equal(location.searchParams.get("staff"), "1");
  assert.equal(location.searchParams.get("return"), "/admin/firms?tab=people");
  const file = await staffGate(new Request("https://app.example.test/admin/assets/index-1a2b.js"), "server", answering(401));
  assert.equal(file?.status, 401);
  assert.doesNotMatch(await file!.text(), /import|function/);

  // A staff member without the console's role, or a check that cannot answer, gets nothing of it.
  assert.equal((await staffGate(page, "server", answering(403)))?.status, 403);
  assert.equal((await staffGate(page, "server", answering(503)))?.status, 503);
  assert.equal((await staffGate(page, "server", async () => { throw new Error("container down"); }))?.status, 503);

  // Without a session the edge can see, nothing is served, and the API is not even asked.
  const before = asked.length;
  for (const mode of [undefined, "", "client"]) assert.equal((await staffGate(page, mode, answering(200)))?.status, 503);
  assert.equal(asked.length, before);
});

test("what the console serves is for its staff member alone; refusals are never kept", async () => {
  assert.equal(secureAdmin(new Response("ok")).headers.get("Cache-Control"), "private, no-cache");
  const refused = await staffGate(new Request("https://app.example.test/admin/"), "client", async () => new Response());
  assert.equal(secureAdmin(refused!).headers.get("Cache-Control"), "no-store");
  assert.equal(secureAdmin(refused!).headers.get("X-Frame-Options"), "DENY");
});
