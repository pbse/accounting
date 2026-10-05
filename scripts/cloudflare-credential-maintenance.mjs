// Manual credential maintenance: inspect names/revisions, then execute an
// explicitly reviewed deletion plan only while those revisions still match.
import { credentials, secretNames, wrangler } from './cloudflare-credentials.mjs';
import { writeFileSync } from 'node:fs';
const environment=process.env.CREDENTIAL_MAINTENANCE_ENV;
if (!['production','staging'].includes(environment)) throw new Error('Select production or staging');
const production=environment==='production';
function revision(app) {
 let entries;
 try { entries=JSON.parse(wrangler(app,['deployments','list','--json'],production)); } catch {throw new Error(`Cannot inspect ${app} deployment metadata`);}
 if (!Array.isArray(entries) || !entries.length || entries.some(entry=>typeof entry?.created_on!=='string'||!Number.isFinite(Date.parse(entry.created_on)))) throw new Error(`Invalid ${app} deployment metadata`);
 const current=entries.sort((a,b)=>Date.parse(a.created_on)-Date.parse(b.created_on)).at(-1);
 if (typeof current.id!=='string'||!/^[a-zA-Z0-9-]+$/.test(current.id)) throw new Error(`Invalid ${app} deployment revision`);
 return current.id;
}
function inspect(app) {
 const before=revision(app);
 const installed=secretNames(app,production);
 const after=revision(app);
 if (before!==after) throw new Error(`Inspection refused: ${app} changed during metadata reads`);
 return {installed,deployment:after};
}
const protectedNames=new Set(['DATABASE_URL','CONTEXT_MACHINE_DATABASE_URL','UPLOAD_SCAN_KEY','CONTEXT_MACHINE_TOKEN','SECRETS_KEY','WORKOS_API_KEY','EMAIL_INTAKE_KEY']);
try {
 const raw=process.env.CREDENTIAL_MAINTENANCE_PLAN || '';
 if (!raw) {
  const workers={};
  for (const [app,names] of Object.entries(credentials)) {
   const snapshot=inspect(app);
   workers[app]={deployment:snapshot.deployment,credentials:names.filter(name=>snapshot.installed.has(name))};
  }
  writeFileSync('credential-metadata.json',JSON.stringify({environment,workers},null,2)+'\n');
  console.log('Credential names and deployment revisions inspected; no values read or mutations performed.');
 } else {
  let plan;try {plan=JSON.parse(raw);} catch {throw new Error('Invalid credential deletion plan JSON');}
  if (raw.length>16384 || !plan || Array.isArray(plan) || typeof plan!=='object' || !Object.keys(plan).length) throw new Error('Invalid credential deletion plan');
  for (const [app,operation] of Object.entries(plan)) {
   if (!Object.hasOwn(credentials,app) || !operation || Object.keys(operation).sort().join(',')!=='deployment,remove' || typeof operation.deployment!=='string' || !/^[a-zA-Z0-9-]+$/.test(operation.deployment) || !Array.isArray(operation.remove) || !operation.remove.length || operation.remove.some(name=>typeof name!=='string'||(!credentials[app].includes(name)||protectedNames.has(name))) || new Set(operation.remove).size!==operation.remove.length) throw new Error('Deletion plan contains an invalid destination, revision or credential name');
   const snapshot=inspect(app);
   if (operation.remove.some(name=>!snapshot.installed.has(name))) throw new Error(`Deletion plan no longer matches ${app} credential names`);
   if (snapshot.deployment!==operation.deployment) throw new Error(`Deletion refused: ${app} changed since inspection`);
  }
  // Check every destination before the first deletion. Workflow concurrency
  // serializes deploys; operators must avoid simultaneous dashboard edits.
  for (const [app,operation] of Object.entries(plan)) {
   for (const name of operation.remove) {
    wrangler(app,['secret','delete',name],production);
    if (secretNames(app,production).has(name)) throw new Error(`Credential deletion verification failed for ${app}`);
    console.log(`Deleted ${name} from ${app}`);
   }
  }
 }
} catch (error) {console.error(error.message);process.exitCode=1;}
