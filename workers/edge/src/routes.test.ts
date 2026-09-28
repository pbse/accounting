import assert from "node:assert/strict";
import { test } from "node:test";
import { adminPage, forwardsToAPI } from "./routes.ts";

test("MCP and its OAuth discovery reach the API; internal routes and assets do not", () => {
  for (const path of ["/healthz", "/readyz", "/v1/closes", "/mcp", "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"]) {
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
