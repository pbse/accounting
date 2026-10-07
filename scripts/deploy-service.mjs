import { mkdtempSync, mkdirSync, writeFileSync, rmSync, appendFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { credentials, secretNames, wrangler } from './cloudflare-credentials.mjs';
export const configuration = {
 "edge": [
  "TEXT_SCANNER_URL",
  "UPLOAD_SCAN_HOST",
  "EMAIL_INTAKE_ADDRESS",
  "CONTACT_FORWARD_ADDRESS",
  "QBO_CLIENT_ID",
  "QBO_REDIRECT_URI",
  "QBO_PRODUCTION_CLIENT_ID",
  "QBO_PRODUCTION_REDIRECT_URI",
  "XERO_CLIENT_ID",
  "XERO_REDIRECT_URI",
  "PUBLIC_APP_URL",
  "WORKOS_CLIENT_ID",
  "WORKOS_ORGANIZATION_ID",
  "WORKOS_ISSUER",
  "PLATFORM_ORGANIZATION_ID",
  "STAFF_EMAIL_DOMAIN",
  "FRONTEND_ORIGIN",
  "OPENAI_MODEL",
  "OPENAI_REASONING_EFFORT",
  "LLM_REASONING_EFFORTS",
  "LLM_MODELS",
  "LLM_PROVIDER",
  "LLM_ALLOW_UNCAPPED_OUTPUT_TOKENS",
  "CONTEXT_MACHINE_PROFILES",
  "CODEX_MODEL",
  "CLAUDE_MODEL",
  "CODEX_REASONING_EFFORT",
  "CLAUDE_REASONING_EFFORT",
  "CONTEXT_MACHINE_MODEL",
  "QBO_SANDBOX_REALM_ID",
  "QBO_SANDBOX_ENTITY",
  "QBO_SANDBOX_PREPAID_ACCOUNT_ID",
  "QBO_SANDBOX_EXPENSE_ACCOUNT_ID",
  "WORKOS_AUTHKIT_DOMAIN",
  "MCP_RESOURCE_URL",
  "MCP_ALLOWED_ORIGINS",
  "PRODUCTION_POSTING_KINDS",
  "XERO_POSTING_KINDS",
  "AUTH_SESSION"
 ],
 "queue": [
  "OPENAI_MODEL",
  "OPENAI_REASONING_EFFORT",
  "LLM_REASONING_EFFORTS",
  "LLM_MODELS",
  "LLM_PROVIDER",
  "LLM_ALLOW_UNCAPPED_OUTPUT_TOKENS",
  "CONTEXT_MACHINE_PROFILES",
  "CODEX_MODEL",
  "CLAUDE_MODEL",
  "CODEX_REASONING_EFFORT",
  "CLAUDE_REASONING_EFFORT",
  "CONTEXT_MACHINE_MODEL",
  "QBO_CLIENT_ID",
  "QBO_REDIRECT_URI",
  "QBO_PRODUCTION_CLIENT_ID",
  "QBO_PRODUCTION_REDIRECT_URI",
  "XERO_CLIENT_ID",
  "XERO_REDIRECT_URI",
  "PUBLIC_APP_URL",
  "TEXT_SCANNER_URL"
 ],
 "scanner": []
};

const app = process.argv[2];
const production = process.argv.includes('--production');
if (!configuration[app]) throw new Error('Specify edge, queue or scanner');
if (process.argv.includes('--check')) {
 const installed = secretNames(app, production);
 const required = app === 'scanner' ? ['UPLOAD_SCAN_KEY'] : ['DATABASE_URL','UPLOAD_SCAN_KEY'];
 if (app === 'edge' && process.env.EMAIL_INTAKE_ADDRESS) required.push('EMAIL_INTAKE_KEY');
 if (app !== 'scanner') {
  for (const [id,secret,redirect] of [['QBO_CLIENT_ID','QBO_CLIENT_SECRET','QBO_REDIRECT_URI'],['QBO_PRODUCTION_CLIENT_ID','QBO_PRODUCTION_CLIENT_SECRET','QBO_PRODUCTION_REDIRECT_URI'],['XERO_CLIENT_ID','XERO_CLIENT_SECRET','XERO_REDIRECT_URI']]) {
   if (!process.env[id] && !process.env[redirect]) continue;
   if (!process.env[id] || !process.env[redirect]) throw new Error(`${id} and ${redirect} must be configured together`);
   required.push(secret,'SECRETS_KEY');
  }
  if (process.env.AUTH_SESSION === 'server') required.push('WORKOS_API_KEY','SECRETS_KEY');
  if (process.env.LLM_PROVIDER === 'context_machine' || process.env.CONTEXT_MACHINE_ENABLED !== 'false') required.push('CONTEXT_MACHINE_TOKEN');
 }
 const missing = required.filter(name => !installed.has(name));
 if (missing.length) throw new Error(`Missing Cloudflare credentials for ${app}: ${missing.join(', ')}`);
 console.log(`Cloudflare credential metadata verified for ${app}`);
} else if (process.argv.includes('--version')) {
 // A direct secret rotation publishes a Worker version too. Use metadata to
 // retain the scanner build cache without reading or hashing its credential.
 let deployments;
 try { deployments = JSON.parse(wrangler(app,['deployments','list','--json'],production)); } catch { throw new Error('Cannot read scanner deployment metadata'); }
 if (!Array.isArray(deployments) || deployments.some(d => typeof d?.created_on !== 'string' || !Number.isFinite(Date.parse(d.created_on)))) throw new Error('Invalid scanner deployment metadata');
 const current = deployments.sort((a,b)=>Date.parse(a.created_on)-Date.parse(b.created_on)).at(-1);
 if (typeof current?.id !== 'string' || !/^[a-zA-Z0-9-]+$/.test(current.id)) throw new Error('Invalid scanner deployment metadata');
 if (process.argv.includes('--record')) {mkdirSync('.scanner-deployed',{recursive:true});writeFileSync('.scanner-deployed/version',current.id+'\n');}
 else {if (!process.env.GITHUB_OUTPUT) throw new Error('--version requires GITHUB_OUTPUT');appendFileSync(process.env.GITHUB_OUTPUT,`version=${current.id}\n`);}
} else {
 const dir = mkdtempSync(join(tmpdir(),'close-deploy-config-'));
 try {
  const file = join(dir,'configuration.json');
  const values = Object.fromEntries(configuration[app].map(name => [name,process.env[name] || '']));
  if (app !== 'scanner' && process.env.LLM_PROVIDER === 'context_machine') {values.OPENAI_MODEL=''; values.OPENAI_REASONING_EFFORT='';}
  if (Object.keys(values).some(name => credentials[app].includes(name))) throw new Error('Deployment configuration contains a credential');
  writeFileSync(file,JSON.stringify(values),{mode:0o600});
  // Pinned Wrangler preserves existing secrets with --secrets-file. Never
  // remove this flag or include credentials (including empty values) here.
  const args=app === 'edge' ? ['run',production ? 'deploy:production' : 'deploy:staging','--','--secrets-file',file] : ['--no','wrangler','deploy','-c',`workers/${app}/wrangler.jsonc`,'--secrets-file',file,...(production ? ['--env','production'] : [])];
  if (app === 'edge' && !production && process.env.UPLOAD_SCAN_HOST) args.push('--route',`${process.env.UPLOAD_SCAN_HOST}/*`);
  const result=spawnSync(app === 'edge' ? 'npm' : 'npx',args,{stdio:'inherit'});
  if (result.error || result.status !== 0) throw new Error(`${app} deployment failed`);
 } finally {rmSync(dir,{recursive:true,force:true});}
}
