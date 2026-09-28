import assert from "node:assert/strict";
import { test } from "node:test";
import { forwardsToAPI } from "./routes.ts";

test("MCP and its OAuth discovery reach the API; internal routes and assets do not", () => {
  for (const path of ["/healthz", "/readyz", "/v1/closes", "/mcp", "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"]) {
    assert.ok(forwardsToAPI(path), path);
  }
  for (const path of ["/internal/email-reply", "/", "/dashboard", "/mcp/extra", "/.well-known/openid-configuration", "/.well-known/oauth-protected-resourcex"]) {
    assert.ok(!forwardsToAPI(path), path);
  }
});
