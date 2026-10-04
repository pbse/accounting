import { spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

// Credentials are installed out of band. Deployments must never include these
// names, even with an empty value, in their configuration payloads.
export const credentials = {
 edge: ['DATABASE_URL','UPLOAD_SCAN_KEY','EMAIL_INTAKE_KEY','SECRETS_KEY','QBO_CLIENT_SECRET','QBO_WEBHOOK_VERIFIER','WORKOS_API_KEY','QBO_PRODUCTION_CLIENT_SECRET','XERO_CLIENT_SECRET','XERO_WEBHOOK_KEY','OPENAI_API_KEY','CONTEXT_MACHINE_TOKEN','QBO_SANDBOX_ACCESS_TOKEN'],
 queue: ['DATABASE_URL','UPLOAD_SCAN_KEY','SECRETS_KEY','QBO_CLIENT_SECRET','WORKOS_API_KEY','QBO_PRODUCTION_CLIENT_SECRET','XERO_CLIENT_SECRET','OPENAI_API_KEY','CONTEXT_MACHINE_TOKEN','OPERATIONS_ALERT_WEBHOOK_URL','OPERATIONS_ALERT_WEBHOOK_TOKEN'],
 scanner: ['UPLOAD_SCAN_KEY'],
 'context-machine': ['CONTEXT_MACHINE_TOKEN','CONTEXT_MACHINE_DATABASE_URL','OPENAI_API_KEY','ANTHROPIC_API_KEY','CODEX_ACCESS_TOKEN','CLAUDE_CODE_OAUTH_TOKEN','CODEX_AUTH_JSON'],
};
export function wrangler(app, command, production, input) {
 if (!credentials[app]) throw new Error('Unknown credential destination');
 const result = spawnSync('npx', ['wrangler', ...command, '-c', `workers/${app}/wrangler.jsonc`, ...(production ? ['--env','production'] : [])], {
  input, encoding: 'utf8', env: Object.fromEntries(['PATH','HOME','CI','CLOUDFLARE_API_TOKEN','CLOUDFLARE_ACCOUNT_ID','WRANGLER_SEND_METRICS'].map(name => [name,name === 'WRANGLER_SEND_METRICS' ? 'false' : process.env[name] || ''])), maxBuffer: 1024 * 1024,
 });
 // Never relay CLI diagnostics: providers may echo rejected secret values.
 if (result.error || result.status !== 0) throw new Error(`Cloudflare credential operation failed for ${app}; check account access and Worker existence`);
 return result.stdout;
}
export function secretNames(app, production) {
 let entries;
 try { entries = JSON.parse(wrangler(app, ['secret','list'], production)); } catch { throw new Error(`Cannot read Cloudflare credential metadata for ${app}`); }
 if (!Array.isArray(entries) || entries.some(e => !e || typeof e.name !== 'string' || e.type !== 'secret_text')) throw new Error(`Invalid Cloudflare credential metadata for ${app}`);
 return new Set(entries.map(e => e.name));
}
export function validateCredential(name, input) {
 if (!Object.values(credentials).some(names => names.includes(name))) throw new Error('Unknown service credential');
 let value = name === 'CODEX_AUTH_JSON' ? input.trim() : input.replace(/\r?\n$/, '');
 if (value !== value.trim()) throw new Error(`${name} cannot contain surrounding whitespace`);
 if (!value) throw new Error(`${name} must be nonempty`);
 if (name === 'CODEX_AUTH_JSON') {
  let auth;
  try { auth = JSON.parse(value); } catch { throw new Error('Invalid CODEX_AUTH_JSON'); }
  if (auth?.auth_mode !== 'chatgpt' || ['access_token','refresh_token','id_token','account_id'].some(k => typeof auth.tokens?.[k] !== 'string' || !auth.tokens[k])) throw new Error('CODEX_AUTH_JSON requires a complete managed ChatGPT login');
  value = JSON.stringify(auth);
 }
 if (Buffer.byteLength(value) > 5000) throw new Error(`${name} must be at most 5 KB after compaction`);
 if (['CONTEXT_MACHINE_TOKEN','UPLOAD_SCAN_KEY','EMAIL_INTAKE_KEY'].includes(name) && value.length < 32) throw new Error(`${name} requires at least 32 random characters`);
 if (name === 'SECRETS_KEY' && !/^[a-f0-9]{64}$/i.test(value)) throw new Error('SECRETS_KEY requires 32 hex-encoded bytes');
 if (['DATABASE_URL','CONTEXT_MACHINE_DATABASE_URL'].includes(name)) {
  let db; try { db = new URL(value); } catch { throw new Error(`Invalid ${name}`); }
  if (!['postgres:','postgresql:'].includes(db.protocol) || !db.username || !db.password || !['require','verify-ca','verify-full'].includes(db.searchParams.get('sslmode')) || db.hostname.includes('-pooler')) throw new Error(`${name} requires an authenticated direct PostgreSQL endpoint with TLS`);
 }
 if (name === 'OPERATIONS_ALERT_WEBHOOK_URL') {
  let url; try { url = new URL(value); } catch { throw new Error('Invalid operations webhook URL'); }
  if (url.protocol !== 'https:' || !url.hostname || url.username || url.password || url.hash) throw new Error('Operations webhook requires HTTPS without embedded credentials or fragment');
 }
 return value;
}
export function install(app, values, production) {
 if (!credentials[app] || Object.keys(values).some(name => !credentials[app].includes(name))) throw new Error('Credential destination is outside its allowed scope');
 const checked = Object.fromEntries(Object.entries(values).map(([name,value]) => [name,validateCredential(name,value)]));
 if (!Object.keys(checked).length) return;
 wrangler(app, ['secret','bulk'], production, JSON.stringify(checked));
 const installed = secretNames(app, production);
 if (Object.keys(checked).some(name => !installed.has(name))) throw new Error(`Cloudflare credential verification failed for ${app}`);
 console.log(`Installed ${Object.keys(checked).join(', ')} in ${app}`);
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
 try {
  const [name, environment] = process.argv.slice(2);
  if (!['production','staging'].includes(environment) || process.argv.length !== 4) throw new Error('Usage: node scripts/cloudflare-credentials.mjs NAME production|staging < credential-file');
  if (process.stdin.isTTY) throw new Error('Redirect a credential file or pipe into stdin; interactive input would echo the credential');
  const value = validateCredential(name, readFileSync(0,'utf8'));
  const apps = Object.entries(credentials).filter(([,names]) => names.includes(name)).map(([app]) => app);
  for (const app of apps) secretNames(app, environment === 'production');
  for (const app of apps) install(app, {[name]:value}, environment === 'production');
 } catch (error) { console.error(error.message); process.exitCode = 1; }
}
