import { mkdtempSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";

// One required configuration contract for staging and production. Never print values.
const names = ["CONTEXT_MACHINE_TOKEN", "CONTEXT_MACHINE_DATABASE_URL",
  "CONTEXT_MACHINE_PROFILES", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "CODEX_MODEL", "CLAUDE_MODEL"];
for (const name of [...names, "CONTEXT_MACHINE_MODEL"]) {
  if (!process.env[name]) throw new Error(`Missing context-machine setting: ${name}`);
}
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
    writeFileSync(file, JSON.stringify(Object.fromEntries(names.map(n => [n, process.env[n]]))), { mode: 0o600 });
    const args = ["wrangler", "deploy", "-c", "workers/context-machine/wrangler.jsonc", "--secrets-file", file];
    if (process.argv.includes("--production")) args.push("--env", "production");
    const result = spawnSync("npx", args, { stdio: "inherit" });
    if (result.error || result.status !== 0) throw new Error("Context-machine deployment failed");
  } finally { rmSync(dir, { recursive: true, force: true }); }
}
