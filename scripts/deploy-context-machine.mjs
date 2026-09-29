import { mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";

// One required configuration contract for staging and production. Never print values.
const names = ["CONTEXT_MACHINE_TOKEN", "CONTEXT_MACHINE_DATABASE_URL", "CONTEXT_MACHINE_PROFILES", "CONTEXT_MACHINE_MODEL",
 "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "CODEX_MODEL", "CLAUDE_MODEL", "CODEX_ACCESS_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
 "CODEX_AUTH_MODE", "CLAUDE_AUTH_MODE", "CODEX_REASONING_EFFORT", "CLAUDE_REASONING_EFFORT"];
for (const name of names.slice(0, 4)) {
 if (!process.env[name]) throw new Error(`Missing context-machine setting: ${name}`);
}
const enabled = new Set();
for (const [agent, prefix, keyName, tokenName] of [["codex", "CODEX", "OPENAI_API_KEY", "CODEX_ACCESS_TOKEN"], ["claude", "CLAUDE", "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"]]) {
 const mode = process.env[`${prefix}_AUTH_MODE`] || "auto";
 const effort = process.env[`${prefix}_REASONING_EFFORT`] || "medium";
 const levels = agent === "codex" ? ["low", "medium", "high", "xhigh", "max", "ultra"] : ["low", "medium", "high", "xhigh", "max"];
 if (!["auto", "api_key", "subscription"].includes(mode) || !levels.includes(effort)) throw new Error(`Invalid ${prefix} auth mode or effort`);
 const key = process.env[keyName], token = process.env[tokenName];
 const credential = mode === "subscription" ? token : mode === "api_key" ? key : key || token;
 if (!credential && mode === "auto") continue;
 if (!credential || !process.env[`${prefix}_MODEL`]) throw new Error(`${prefix} requires its selected credential and model`);
 enabled.add(agent);
}
if (!enabled.has(process.env.CONTEXT_MACHINE_MODEL)) throw new Error("Default context-machine agent is not configured");
if (process.env.CONTEXT_MACHINE_TOKEN.length < 32) throw new Error("CONTEXT_MACHINE_TOKEN requires 32 random characters");
if (!["codex", "claude"].includes(process.env.CONTEXT_MACHINE_MODEL)) throw new Error("CONTEXT_MACHINE_MODEL must be codex or claude");
let db, application;
try { db = new URL(process.env.CONTEXT_MACHINE_DATABASE_URL); } catch { throw new Error("Invalid CONTEXT_MACHINE_DATABASE_URL"); }
try { application = process.env.DATABASE_URL ? new URL(process.env.DATABASE_URL) : undefined; } catch { throw new Error("Invalid DATABASE_URL"); }
if (!["postgres:", "postgresql:"].includes(db.protocol) || !["require", "verify-ca", "verify-full"].includes(db.searchParams.get("sslmode")))
  throw new Error("CONTEXT_MACHINE_DATABASE_URL must be PostgreSQL with TLS");
if (application && db.username === application.username)
  throw new Error("Context machine must use a separate read-only database login");
let profiles;
try { profiles = JSON.parse(process.env.CONTEXT_MACHINE_PROFILES); } catch { throw new Error("Invalid CONTEXT_MACHINE_PROFILES JSON"); }
if (!Array.isArray(profiles) || !profiles.length ||
    profiles.some(p => !p || ["organization", "subject", "access_profile"].some(k => typeof p[k] !== "string" || !p[k].trim())) ||
    new Set(profiles.map(p => p.access_profile)).size !== profiles.length)
  throw new Error("CONTEXT_MACHINE_PROFILES requires unique named organization/subject profiles");

if (!process.argv.includes("--check")) {
  const dir = mkdtempSync(join(tmpdir(), "close-context-machine-"));
  try {
    const file = join(dir, "secrets.json");
    writeFileSync(file, JSON.stringify(Object.fromEntries(names.map(n => [n, process.env[n] || ""]))), { mode: 0o600 });
    const args = ["wrangler", "deploy", "-c", "workers/context-machine/wrangler.jsonc", "--secrets-file", file];
    if (process.argv.includes("--production")) args.push("--env", "production");
    const result = spawnSync("npx", args, { stdio: "inherit" });
    if (result.error || result.status !== 0) throw new Error("Context-machine deployment failed");
  } finally { rmSync(dir, { recursive: true, force: true }); }
}
