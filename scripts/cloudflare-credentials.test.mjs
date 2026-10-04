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
});

test('direct credential installer scopes stdin, excludes secret arguments, and redacts CLI failures', () => {
 const dir=mkdtempSync(join(tmpdir(),'credential-test-'));
 const log=join(dir,'calls');
 writeFileSync(join(dir,'npx'),`#!/usr/bin/env node\nconst fs=require('fs'); const args=process.argv.slice(2); const input=fs.readFileSync(0,'utf8'); fs.appendFileSync(require('path').join(__dirname,'calls'),JSON.stringify({args,input})+'\\n'); if(fs.existsSync(require('path').join(__dirname,'fail'))){console.error(input);process.exit(1);} console.log(JSON.stringify([{name:'CONTEXT_MACHINE_TOKEN',type:'secret_text'},{name:'CODEX_AUTH_JSON',type:'secret_text'}]));\n`,{mode:0o700});
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
  assert.equal(result.status,1);assert.ok(!`${result.stderr}${result.stdout}`.includes(secret));
  assert.throws(() => install('scanner',{CODEX_AUTH_JSON:'bad'},true),/outside/);
  for (const call of calls.filter(c=>c.input)) {assert.deepEqual(call.args.slice(0,4),['--no','wrangler','secret','bulk']);assert.deepEqual(JSON.parse(call.input),{CONTEXT_MACHINE_TOKEN:secret});}
 } finally {rmSync(dir,{recursive:true,force:true});}
});

test('transfer validates all targets before writes and emits only the runtime role', () => {
 const dir=mkdtempSync(join(tmpdir(),'credential-migrate-'));
 const script=join(process.cwd(),'scripts/migrate-service-credentials.mjs');
 const token='private-service-token-00000000000000000';
 writeFileSync(join(dir,'npx'),`#!${process.execPath}
const fs=require('fs'),path=require('path');const args=process.argv.slice(2);const input=fs.readFileSync(0,'utf8');fs.appendFileSync(path.join(__dirname,'calls'),JSON.stringify({args,input})+'\\n');
if(fs.existsSync(path.join(__dirname,'missing')) && args.includes('workers/context-machine/wrangler.jsonc'))process.exit(1);
console.log(JSON.stringify(['DATABASE_URL','CONTEXT_MACHINE_TOKEN','CONTEXT_MACHINE_DATABASE_URL'].map(name=>({name,type:'secret_text'}))));`,{mode:0o700});
 const env={PATH:`${dir}:${process.env.PATH}`,HOME:process.env.HOME,DATABASE_URL:'postgres://close_runtime:private-db-password@db.example/close?sslmode=require',CONTEXT_MACHINE_TOKEN:token,CONTEXT_MACHINE_DATABASE_URL:'postgres://context_machine_reader:private-reader@db.example/close?sslmode=require'};
 const run=(overrides={},args=['--production'])=>spawnSync(process.execPath,[script,...args],{cwd:dir,env:{...env,...overrides},encoding:'utf8'});
 const calls=()=>readFileSync(join(dir,'calls'),'utf8').trim().split('\n').map(JSON.parse);
 try {
  assert.notEqual(run({CONTEXT_MACHINE_DATABASE_URL:'invalid'}).status,0);
  assert.throws(()=>calls(),{code:'ENOENT'});
  assert.notEqual(run({},[]).status,0);
  writeFileSync(join(dir,'missing'),'true');
  assert.notEqual(run().status,0);assert.ok(calls().every(c=>!c.input));
  rmSync(join(dir,'missing'));writeFileSync(join(dir,'calls'),'');
  const result=run();assert.equal(result.status,0,result.stderr);
  const operations=calls();assert.ok(operations.slice(0,4).every(c=>!c.input));
  assert.equal(readFileSync(join(dir,'runtime-role.txt'),'utf8'),'close_runtime\n');
  assert.ok(!`${result.stdout}${result.stderr}`.includes(token));
  for (const call of operations.filter(c=>c.input)) {
   const app=call.args[call.args.indexOf('-c')+1].split('/')[1];
   assert.ok(Object.keys(JSON.parse(call.input)).every(name=>credentials[app].includes(name)));
   assert.ok(!call.input.includes('MIGRATION_DATABASE_URL'));
  }
 } finally {rmSync(dir,{recursive:true,force:true});}
});

test('installer requires an explicit valid environment', () => {
 for (const args of [[],['CODEX_AUTH_JSON'],['CODEX_AUTH_JSON','prod'],['CODEX_AUTH_JSON','production','extra']]) {
  const result=spawnSync(process.execPath,['scripts/cloudflare-credentials.mjs',...args],{input:'private-value',encoding:'utf8'});
  assert.equal(result.status,1);assert.match(result.stderr,/Usage:/);assert.ok(!result.stderr.includes('private-value'));
 }
});
