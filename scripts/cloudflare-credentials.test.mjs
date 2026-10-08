import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
import { validateCredential, credentials, install } from './cloudflare-credentials.mjs';

test('credential validation rejects unsafe URLs and compacts managed login', () => {
 for (const value of ['postgres://reader:p@host/db','postgres://reader:p@host-pooler/db?sslmode=require','https://reader:p@host/db?sslmode=require']) assert.throws(() => validateCredential('CONTEXT_MACHINE_DATABASE_URL',value));
 const auth={auth_mode:'chatgpt', tokens:{access_token:'a',refresh_token:'r',id_token:'i',account_id:'id'}};
 assert.equal(validateCredential('CODEX_AUTH_JSON',JSON.stringify(auth,null,6000)),JSON.stringify(auth));
 assert.throws(() => validateCredential('CODEX_AUTH_JSON','{"auth_mode":"apikey"}'));
 assert.throws(() => validateCredential('CODEX_AUTH_JSON',JSON.stringify({...auth,extra:'x'.repeat(5000)})));
 assert.throws(() => validateCredential('CLOUDFLARE_API_TOKEN','x'));
 assert.throws(() => validateCredential('SECRETS_KEY','invalid'));
 assert.throws(() => validateCredential('QBO_WEBHOOK_VERIFIER',' padded '));
 assert.throws(() => validateCredential('OPERATIONS_ALERT_WEBHOOK_URL','http://host'));
 assert.throws(() => validateCredential('OPERATIONS_ALERT_WEBHOOK_URL','https://user:pass@host'));
 assert.equal(validateCredential('OPERATIONS_ALERT_WEBHOOK_URL','https://host/path'),'https://host/path');
 // The Gemini key goes to the worker that reads PDFs, and nowhere else.
 assert.ok(credentials.queue.includes('GEMINI_API_KEY'));
 assert.ok(!credentials.edge.includes('GEMINI_API_KEY') && !credentials.scanner.includes('GEMINI_API_KEY'));
 assert.throws(() => validateCredential('GEMINI_API_KEY','two words inside a key value'));
 assert.throws(() => validateCredential('GEMINI_API_KEY','short'));
 assert.equal(validateCredential('GEMINI_API_KEY','AIzaSyExampleOnly-not-a-real-key_0000\n'),'AIzaSyExampleOnly-not-a-real-key_0000');
 assert.ok(credentials.queue.includes('GEMINI_PAID_SERVICE'));
 assert.throws(() => validateCredential('GEMINI_PAID_SERVICE','yes'));
 assert.equal(validateCredential('GEMINI_PAID_SERVICE','confirmed'),'confirmed');
});

test('direct credential installer scopes stdin, excludes secret arguments, and redacts CLI failures', () => {
 const dir=mkdtempSync(join(tmpdir(),'credential-test-'));
 const log=join(dir,'calls');
 writeFileSync(join(dir,'npx'),`#!/usr/bin/env node\nconst fs=require('fs'); const args=process.argv.slice(2); const input=fs.readFileSync(0,'utf8'); fs.appendFileSync(require('path').join(__dirname,'calls'),JSON.stringify({args,input})+'\\n'); if(fs.existsSync(require('path').join(__dirname,'fail'))){console.error(input);process.exit(1);} console.log(JSON.stringify([{name:'CONTEXT_MACHINE_TOKEN',type:'secret_text'},{name:'CODEX_AUTH_JSON',type:'secret_text'},{name:'GEMINI_API_KEY',type:'secret_text'},{name:'GEMINI_PAID_SERVICE',type:'secret_text'}]));\n`,{mode:0o700});
 const secret='secret-never-appears-in-arguments-0000000';
 const env={...process.env,PATH:`${dir}:${process.env.PATH}`,CALLS:log};
 try {
  let result=spawnSync(process.execPath,['scripts/cloudflare-credentials.mjs','CONTEXT_MACHINE_TOKEN','production'],{input:secret,env,encoding:'utf8'});
  assert.equal(result.status,0,result.stderr);
  const calls=readFileSync(log,'utf8').trim().split('\n').map(JSON.parse);
  assert.deepEqual(calls.filter(c=>c.input).map(c=>c.args[c.args.indexOf('-c')+1]),['workers/edge/wrangler.jsonc','workers/queue/wrangler.jsonc','workers/context-machine/wrangler.jsonc']);
  for (const call of calls) {assert.ok(!call.args.includes(secret));assert.ok(call.args.includes('production'));}
  assert.ok(!result.stdout.includes(secret));
  writeFileSync(join(dir,'fail'),'true');
  result=spawnSync(process.execPath,['scripts/cloudflare-credentials.mjs','CONTEXT_MACHINE_TOKEN','production'],{input:secret,env,encoding:'utf8'});
  assert.equal(result.status,1);
  rmSync(join(dir,'fail'));
  // With the paid service confirmed, a Gemini key goes to the queue worker alone, in one install with the confirmation.
  const key='AIzaSyExampleOnly-not-a-real-key_0000';
  writeFileSync(log,'');
  result=spawnSync(process.execPath,['scripts/cloudflare-credentials.mjs','GEMINI_API_KEY','production','--paid-service'],{input:key,env,encoding:'utf8'});
  assert.equal(result.status,0,result.stderr);
  const geminiCalls=readFileSync(log,'utf8').trim().split('\n').map(JSON.parse).filter(c=>c.input);
  assert.deepEqual(geminiCalls.map(c=>c.args[c.args.indexOf('-c')+1]),['workers/queue/wrangler.jsonc']);
  assert.deepEqual(JSON.parse(geminiCalls[0].input),{GEMINI_API_KEY:key,GEMINI_PAID_SERVICE:'confirmed'});
  // The confirmation is never installed apart from the key.
  result=spawnSync(process.execPath,['scripts/cloudflare-credentials.mjs','GEMINI_PAID_SERVICE','production'],{input:'confirmed',env,encoding:'utf8'});
  assert.equal(result.status,1);assert.match(result.stderr,/--paid-service/);
  writeFileSync(join(dir,'fail'),'true');assert.ok(!`${result.stderr}${result.stdout}`.includes(secret));
  assert.throws(() => install('scanner',{CODEX_AUTH_JSON:'bad'},true),/outside/);
  for (const call of calls.filter(c=>c.input)) {assert.deepEqual(call.args.slice(0,4),['--no','wrangler','secret','bulk']);assert.deepEqual(JSON.parse(call.input),{CONTEXT_MACHINE_TOKEN:secret});}
 } finally {rmSync(dir,{recursive:true,force:true});}
});

test('installer requires an explicit valid environment', () => {
 // A Gemini key also needs the operator's confirmation of the paid service, and only that.
 for (const args of [[],['CODEX_AUTH_JSON'],['CODEX_AUTH_JSON','prod'],['CODEX_AUTH_JSON','production','extra'],['GEMINI_API_KEY','production'],['GEMINI_API_KEY','production','--paid'],['GEMINI_API_KEY','production','--paid-service','extra'],['CODEX_AUTH_JSON','production','--paid-service']]) {
  const result=spawnSync(process.execPath,['scripts/cloudflare-credentials.mjs',...args],{input:'private-value',encoding:'utf8'});
  assert.equal(result.status,1);assert.match(result.stderr,/Usage:/);assert.ok(!result.stderr.includes('private-value'));
 }
});

function fixture() {
 const dir=mkdtempSync(join(tmpdir(),'service-deploy-'));
 const fake=`#!${process.execPath}
const fs=require('node:fs'),path=require('node:path');const args=process.argv.slice(2);
if(args.includes('list')) {console.log(fs.readFileSync(path.join(__dirname,args.includes('deployments')?'versions.json':'names.json'),'utf8'));process.exit(0);}
const file=args[args.indexOf('--secrets-file')+1];fs.writeFileSync(path.join(__dirname,'upload.json'),JSON.stringify({args,values:JSON.parse(fs.readFileSync(file,'utf8')),mode:fs.statSync(file).mode&0o777}));`;
 for (const tool of ['npx','npm']) writeFileSync(join(dir,tool),fake,{mode:0o700});
 writeFileSync(join(dir,'names.json'),JSON.stringify([...new Set(Object.values(credentials).flat())].map(name=>({name,type:'secret_text'}))));
 writeFileSync(join(dir,'versions.json'),JSON.stringify([{id:'new-version',created_on:'2026-10-04T02:00:00Z'},{id:'old-version',created_on:'2026-10-04T01:00:00Z'}]));
 const script=join(process.cwd(),'scripts/deploy-service.mjs');
 const env={...process.env,PATH:`${dir}:${process.env.PATH}`,CONTEXT_MACHINE_ENABLED:'false',LLM_PROVIDER:'api',OPENAI_MODEL:'approved',TEXT_SCANNER_URL:'https://scanner.example/scan',DATABASE_URL:'private-db-value',OPENAI_API_KEY:'private-provider-value',CODEX_AUTH_JSON:'private-managed-auth',CONTEXT_MACHINE_TOKEN:'private-token',GITHUB_OUTPUT:join(dir,'outputs')};
 return {dir,run:(app,args=[],overrides={})=>spawnSync(process.execPath,[script,app,...args],{cwd:dir,env:{...env,...overrides},encoding:'utf8'}),cleanup:()=>rmSync(dir,{recursive:true,force:true})};
}

test('all deployments preserve credentials and use configuration-only secret files', () => {
 const f=fixture();
 try {
  for (const app of ['edge','queue','scanner']) for (const production of [true,false]) {
   const result=f.run(app,production?['--production']:[]);assert.equal(result.status,0,result.stderr);
   const upload=JSON.parse(readFileSync(join(f.dir,'upload.json'),'utf8'));
   assert.ok(upload.args.includes('--secrets-file'));assert.equal(upload.mode,0o600);
   for (const name of Object.values(credentials).flat()) assert.equal(upload.values[name],undefined,name);
   assert.ok(!JSON.stringify(upload.values).includes('private-'));
   assert.equal(upload.values.OPENAI_MODEL,app==='scanner'?undefined:'approved');
   assert.throws(()=>readFileSync(upload.args[upload.args.indexOf('--secrets-file')+1]),{code:'ENOENT'});
   if(app!=='edge')assert.equal(upload.args.includes('--env'),production);
  }
  const result=f.run('edge',['--production'],{LLM_PROVIDER:'context_machine'});assert.equal(result.status,0,result.stderr);
  assert.equal(JSON.parse(readFileSync(join(f.dir,'upload.json'),'utf8')).values.OPENAI_MODEL,'');
 } finally {f.cleanup();}
});

test('metadata preflight fails closed and scanner caching follows Cloudflare rotation', () => {
 const f=fixture();
 try {
  assert.equal(f.run('edge',['--check','--production']).status,0);
  writeFileSync(join(f.dir,'names.json'),'[]');
  const missing=f.run('edge',['--check','--production']);assert.notEqual(missing.status,0);assert.match(missing.stderr,/DATABASE_URL/);
  assert.throws(()=>readFileSync(join(f.dir,'upload.json')),{code:'ENOENT'});
  writeFileSync(join(f.dir,'names.json'),'not-json');assert.notEqual(f.run('queue',['--check']).status,0);
  assert.equal(f.run('scanner',['--version','--production']).status,0);
  assert.match(readFileSync(join(f.dir,'outputs'),'utf8'),/version=new-version/);
  writeFileSync(join(f.dir,'versions.json'),JSON.stringify([{id:'after-rotation',created_on:'2026-10-04T03:00:00Z'}]));
  assert.equal(f.run('scanner',['--version','--record','--production']).status,0);
  assert.equal(readFileSync(join(f.dir,'.scanner-deployed/version'),'utf8'),'after-rotation\n');
  writeFileSync(join(f.dir,'versions.json'),'[]');assert.notEqual(f.run('scanner',['--version']).status,0);
 } finally {f.cleanup();}
});

test('normal workflows reference only GitHub deployment and migration credentials', () => {
 for (const file of ['.github/workflows/deploy-production.yml','.github/workflows/deploy.yml','.github/workflows/provision-context-machine.yml','.github/workflows/provision-context-machine-database.yml','.github/workflows/cloudflare-credential-maintenance.yml']) {
  const names=[...readFileSync(file,'utf8').matchAll(/secrets\.(\w+)/g)].map(match=>match[1]);
  const allowed=file.endsWith('provision-context-machine-database.yml')?['MIGRATION_DATABASE_URL','CONTEXT_MACHINE_DATABASE_PASSWORD']:(file.endsWith('provision-context-machine.yml')||file.endsWith('cloudflare-credential-maintenance.yml'))?['CLOUDFLARE_API_TOKEN','CLOUDFLARE_ACCOUNT_ID']:['CLOUDFLARE_API_TOKEN','CLOUDFLARE_ACCOUNT_ID','MIGRATION_DATABASE_URL'];
  for (const name of names) assert.ok(allowed.includes(name),`${file}: ${name}`);
 }
});

test('metadata preflight preserves optional features and checks configured integration scopes', () => {
 const f=fixture();
 const names=values=>writeFileSync(join(f.dir,'names.json'),JSON.stringify(values.map(name=>({name,type:'secret_text'}))));
 try {
  names(['DATABASE_URL','UPLOAD_SCAN_KEY']);
  assert.equal(f.run('edge',['--check']).status,0);
  for (const overrides of [{AUTH_SESSION:'server'},{QBO_CLIENT_ID:'client'},{XERO_CLIENT_ID:'client',XERO_REDIRECT_URI:'https://host/callback'},{EMAIL_INTAKE_ADDRESS:'evidence@host.example'},{CONTEXT_MACHINE_ENABLED:'true'}]) assert.notEqual(f.run('edge',['--check'],overrides).status,0);
  names(['DATABASE_URL','UPLOAD_SCAN_KEY','XERO_CLIENT_SECRET','SECRETS_KEY','EMAIL_INTAKE_KEY','CONTEXT_MACHINE_TOKEN']);
  assert.equal(f.run('edge',['--check'],{XERO_CLIENT_ID:'client',XERO_REDIRECT_URI:'https://host/callback',EMAIL_INTAKE_ADDRESS:'evidence@host.example',CONTEXT_MACHINE_ENABLED:'true'}).status,0);
  assert.equal(f.run('edge',[],{UPLOAD_SCAN_HOST:'staging.example'}).status,0);
  assert.ok(JSON.parse(readFileSync(join(f.dir,'upload.json'),'utf8')).args.includes('staging.example/*'));
 } finally {f.cleanup();}
});

test('maintenance inspects metadata and rejects stale or out-of-scope deletion plans', () => {
 const f=fixture();
 const script=join(process.cwd(),'scripts/cloudflare-credential-maintenance.mjs');
 writeFileSync(join(f.dir,'npx'),`#!${process.execPath}
const fs=require('node:fs'),path=require('node:path');const args=process.argv.slice(2);const file=name=>path.join(__dirname,name);fs.appendFileSync(file('calls.jsonl'),JSON.stringify(args)+'\\n');
if(args.includes('list')){console.log(fs.readFileSync(file(args.includes('deployments')?'versions.json':'names.json'),'utf8'));process.exit(0);}
if(args.includes('delete')){const name=args[args.indexOf('delete')+1];fs.writeFileSync(file('names.json'),JSON.stringify(JSON.parse(fs.readFileSync(file('names.json'))).filter(entry=>entry.name!==name)));process.exit(0);}process.exit(1);`,{mode:0o700});
 const run=(plan='',environment='production')=>spawnSync(process.execPath,[script],{cwd:f.dir,env:{PATH:`${f.dir}:${process.env.PATH}`,HOME:process.env.HOME,CREDENTIAL_MAINTENANCE_ENV:environment,CREDENTIAL_MAINTENANCE_PLAN:plan},encoding:'utf8'});
 const calls=()=>readFileSync(join(f.dir,'calls.jsonl'),'utf8').trim().split('\n').map(JSON.parse);
 try {
  let result=run();assert.equal(result.status,0,result.stderr);
  const snapshot=JSON.parse(readFileSync(join(f.dir,'credential-metadata.json'),'utf8'));
  assert.equal(snapshot.environment,'production');
  for(const [app,value] of Object.entries(snapshot.workers)){assert.equal(value.deployment,'new-version');assert.ok(value.credentials.every(name=>credentials[app].includes(name)));}
  assert.ok(calls().every(args=>!args.includes('delete')));
  const wrong=run(JSON.stringify({edge:{deployment:'stale-version',remove:['OPENAI_API_KEY']}}));assert.equal(wrong.status,1);assert.match(wrong.stderr,/changed since inspection/);assert.ok(calls().every(args=>!args.includes('delete')));
  for(const plan of [{edge:{deployment:'new-version',remove:['DATABASE_URL']}},{scanner:{deployment:'new-version',remove:['CODEX_AUTH_JSON']}},{edge:{deployment:'new-version',remove:['OPENAI_API_KEY','OPENAI_API_KEY']}},{edge:{deployment:'new-version',remove:['credential-value-pasted-by-mistake']}}]) {
   result=run(JSON.stringify(plan));assert.equal(result.status,1);assert.ok(!result.stderr.includes('credential-value-pasted-by-mistake'));
  }
  result=run(JSON.stringify({edge:{deployment:'new-version',remove:['QBO_WEBHOOK_VERIFIER']},'context-machine':{deployment:'new-version',remove:['CODEX_AUTH_JSON']}}));assert.equal(result.status,0,result.stderr);
  const deletes=calls().filter(args=>args.includes('delete'));
  assert.equal(deletes.length,2);assert.ok(deletes.every(args=>args.includes('production')&&!args.includes('--force')));
  assert.ok(!result.stdout.includes('private-'));
  writeFileSync(join(f.dir,'calls.jsonl'),'');result=run('','staging');assert.equal(result.status,0,result.stderr);assert.ok(calls().every(args=>!args.includes('--env')));
 } finally {f.cleanup();}
});
