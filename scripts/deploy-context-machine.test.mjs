import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, readFileSync, writeFileSync, mkdirSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';

const configured = {
  CONTEXT_MACHINE_TOKEN: 'test-only-0000000000000000000000000000',
  CONTEXT_MACHINE_DATABASE_URL: 'postgres://reader:test@db.example/close?sslmode=require',
  DATABASE_URL: 'postgres://application:test@db.example/close?sslmode=require',
  CONTEXT_MACHINE_PROFILES: JSON.stringify([{ organization: 'org_TEST', subject: 'user_TEST', access_profile: 'read' }]),
  OPENAI_API_KEY: 'test-only', ANTHROPIC_API_KEY: 'test-only', CODEX_MODEL: 'approved', CLAUDE_MODEL: 'approved', CONTEXT_MACHINE_MODEL: 'codex',
};
const credentialNames=['CONTEXT_MACHINE_TOKEN','CONTEXT_MACHINE_DATABASE_URL','OPENAI_API_KEY','ANTHROPIC_API_KEY','CODEX_AUTH_JSON','CODEX_ACCESS_TOKEN','CLAUDE_CODE_OAUTH_TOKEN'];
const metadata = (dir,env) => {
 writeFileSync(join(dir,'metadata.json'),JSON.stringify(credentialNames.filter(name=>env[name]).map(name=>({name,type:'secret_text'}))));
 writeFileSync(join(dir,'npx'), `#!${process.execPath}
const fs=require('node:fs'),path=require('node:path');
if(process.argv.includes('list'))console.log(fs.readFileSync(path.join(__dirname,'metadata.json'),'utf8'));else process.exit(1);`,{mode:0o700});
};
const check = (overrides) => {
 const dir=mkdtempSync(join(tmpdir(),'context-metadata-'));const env={...configured,...overrides};metadata(dir,env);
 try {return spawnSync(process.execPath,['scripts/deploy-context-machine.mjs','--check'],{env:{...env,PATH:`${dir}:${process.env.PATH}`},encoding:'utf8'});} finally {rmSync(dir,{recursive:true,force:true});}
};

test('deployment accepts complete private-service configuration', () => {
  assert.equal(check({}).status, 0);
});
test('deployment fails before mutation for missing credential metadata or invalid profiles', () => {
  for (const overrides of [
    { OPENAI_API_KEY: '' }, { CONTEXT_MACHINE_TOKEN: '' },
    { CONTEXT_MACHINE_PROFILES: 'null' },
    { CONTEXT_MACHINE_MODEL: 'shell' },
  ]) {
    const result = check(overrides);
    assert.notEqual(result.status, 0);
    assert.ok(!result.stderr.includes(configured.CONTEXT_MACHINE_TOKEN));
    assert.ok(!result.stderr.includes("secret-credential"));
  }
});

test('deployment selects available credentials without requiring the unused provider', () => {
 for (const overrides of [
  { ANTHROPIC_API_KEY: '', CLAUDE_MODEL: '' },
  { CONTEXT_MACHINE_MODEL: 'claude', CODEX_MODEL: '' },
  { OPENAI_API_KEY: '', CODEX_ACCESS_TOKEN: 'test-token' },
  { CONTEXT_MACHINE_MODEL: 'claude', OPENAI_API_KEY: '', CLAUDE_CODE_OAUTH_TOKEN: 'test-token', CLAUDE_AUTH_MODE: 'subscription' },
 ]) assert.equal(check(overrides).status, 0);
 for (const overrides of [
  { CODEX_AUTH_MODE: 'subscription' }, { CODEX_AUTH_MODE: 'unknown' },
  { CODEX_REASONING_EFFORT: 'unknown' }, { CLAUDE_REASONING_EFFORT: 'ultra' },
  { CODEX_MODEL: '' }, { CODEX_AUTH_MODE: 'api_key', OPENAI_API_KEY: '', CODEX_ACCESS_TOKEN: 'test-token' },
 ]) assert.notEqual(check(overrides).status, 0);
});


test('preflight reports missing selected-agent settings together without credentials', () => {
  const result = check({ CONTEXT_MACHINE_DATABASE_URL: '', CONTEXT_MACHINE_PROFILES: '', CODEX_MODEL: '', OPENAI_API_KEY: '' });
  assert.notEqual(result.status, 0);
  for (const name of ['CONTEXT_MACHINE_DATABASE_URL', 'CODEX_MODEL', 'OPENAI_API_KEY or CODEX_ACCESS_TOKEN']) assert.ok(result.stderr.includes(name), name);
  assert.ok(!result.stderr.includes(configured.CONTEXT_MACHINE_TOKEN));
});


test('CI retains runtime checks for dependencies and skips unrelated frontend changes', () => {
  const workflow = readFileSync('.github/workflows/ci.yml', 'utf8');
  const block = workflow.split('      - name: Select context-machine integration checks\n')[1].split('      - name:')[0];
  const shell = block.split('        run: |\n')[1].replace(/^          /gm, '');
  const dir = mkdtempSync(join(tmpdir(), 'close-ci-selection-'));
  const env = { ...process.env, GITHUB_EVENT_NAME: 'pull_request', GITHUB_OUTPUT: join(dir, 'output'), GIT_AUTHOR_NAME: 'Test', GIT_COMMITTER_NAME: 'Test', GIT_AUTHOR_EMAIL: 'test@example.invalid', GIT_COMMITTER_EMAIL: 'test@example.invalid' };
  const git = (...args) => assert.equal(spawnSync('git', args, { cwd: dir, env }).status, 0);
  try {
    git('init', '-q');
    git('commit', '-qm', 'base', '--allow-empty');
    for (const [path, expected] of [['apps/frontend/example.tsx', false], ['services/backend/internal/documents/example.go', true], ['infra/postgres/migrations/999_test.sql', true], ['services/context-machine/Dockerfile', true], ['workers/context-machine/src/index.ts', true], ['scripts/deploy-context-machine.mjs', true], ['package-lock.json', true], ['.github/workflows/ci.yml', true]]) {
      mkdirSync(dirname(join(dir, path)), { recursive: true });
      writeFileSync(join(dir, path), 'test');
      git('add', path); git('commit', '-qm', path);
      writeFileSync(env.GITHUB_OUTPUT, '');
      assert.equal(spawnSync('bash', ['-e', '-c', shell], { cwd: dir, env }).status, 0);
      assert.equal(readFileSync(env.GITHUB_OUTPUT, 'utf8').includes('check=true'), expected, path);
    }
    // Reusable calls always run the checks, even without a readable PR diff.
    writeFileSync(env.GITHUB_OUTPUT, '');
    assert.equal(spawnSync('bash', ['-e', '-c', shell], { cwd: dir, env: { ...env, GITHUB_EVENT_NAME: 'workflow_call' } }).status, 0);
    assert.match(readFileSync(env.GITHUB_OUTPUT, 'utf8'), /check=true/);
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

test('disabled research needs no credentials and invalid switches fail closed', () => {
  assert.equal(check({ CONTEXT_MACHINE_ENABLED: 'false', CONTEXT_MACHINE_TOKEN: '', CONTEXT_MACHINE_DATABASE_URL: '', CONTEXT_MACHINE_PROFILES: '', OPENAI_API_KEY: '' }).status, 0);
  assert.notEqual(check({ CONTEXT_MACHINE_ENABLED: 'no' }).status, 0);
});

test('disabled deployment removes only research bindings and clears inactive route metadata', () => {
  const dir = mkdtempSync(join(tmpdir(), 'close-disabled-deploy-'));
  const script = join(process.cwd(), 'scripts/deploy-context-machine.mjs');
  const envFile = join(dir, 'step-env');
  try {
    for (const app of ['edge', 'queue']) {
      const path = `workers/${app}/wrangler.jsonc`;
      const config = JSON.parse(readFileSync(path, 'utf8'));
      config.services.push({ binding: 'OTHER', service: 'other-service' });
      mkdirSync(dirname(join(dir, path)), { recursive: true });
      writeFileSync(join(dir, path), JSON.stringify(config));
    }
    const result = spawnSync(process.execPath, [script, '--prepare-callers'], { cwd: dir, env: { ...configured, CONTEXT_MACHINE_ENABLED: 'false', OPENAI_API_KEY: '', GITHUB_ENV: envFile }, encoding: 'utf8' });
    assert.equal(result.status, 0, result.stderr);
    for (const app of ['edge', 'queue']) {
      const config = JSON.parse(readFileSync(join(dir, `workers/${app}/wrangler.jsonc`), 'utf8'));
      assert.deepEqual(config.services, [{ binding: 'OTHER', service: 'other-service' }]);
      assert.deepEqual(config.env.production.services, []);
      assert.equal(config.containers.length, 1);
      assert.equal(config.env.production.containers.length, 1);
    }
    assert.equal(readFileSync(envFile, 'utf8'), 'CONTEXT_MACHINE_MODEL=\n');
    writeFileSync(envFile, '');
    const active = spawnSync(process.execPath, [script, '--prepare-callers'], { cwd: dir, env: { ...configured, GITHUB_ENV: envFile }, encoding: 'utf8' });
    assert.equal(active.status, 0, active.stderr);
    assert.equal(readFileSync(envFile, 'utf8'), '');
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

test('standby provisioning requires installed service authentication and explicit disabled mode', () => {
 const dir=mkdtempSync(join(tmpdir(),'context-standby-'));
 const provision=(env)=>{metadata(dir,env);return spawnSync(process.execPath,['scripts/deploy-context-machine.mjs','--provision-disabled','--check'],{env:{...env,PATH:`${dir}:${process.env.PATH}`},encoding:'utf8'});};
 const standby={CONTEXT_MACHINE_ENABLED:'false',CONTEXT_MACHINE_TOKEN:configured.CONTEXT_MACHINE_TOKEN};
 try {
  assert.equal(provision(standby).status,0);
  for (const env of [{...standby,CONTEXT_MACHINE_TOKEN:''},{...configured,CONTEXT_MACHINE_ENABLED:'true'},{CONTEXT_MACHINE_TOKEN:configured.CONTEXT_MACHINE_TOKEN}]) assert.notEqual(provision(env).status,0);
 } finally {rmSync(dir,{recursive:true,force:true});}
});

test('deployment preserves stored credentials and uploads only configuration in both modes', () => {
  const dir = mkdtempSync(join(tmpdir(), 'close-deploy-secrets-'));
  const captured = join(dir, 'captured.json');
  try {
    writeFileSync(join(dir, 'npx'), `#!${process.execPath}
const fs = require('node:fs');
if(process.argv.includes('list')){console.log(fs.readFileSync(require('node:path').join(__dirname,'metadata.json'),'utf8'));process.exit(0);}
const file = process.argv[process.argv.indexOf('--secrets-file') + 1];
fs.writeFileSync(process.env.CAPTURED, JSON.stringify({ args: process.argv.slice(2), secrets: JSON.parse(fs.readFileSync(file)), mode: fs.statSync(file).mode & 0o777 }));
`, { mode: 0o755 });
    for (const enabled of ['true', 'false']) {
      const env = enabled === 'true' ? configured : { CONTEXT_MACHINE_TOKEN: configured.CONTEXT_MACHINE_TOKEN };
      writeFileSync(join(dir,'metadata.json'),JSON.stringify(credentialNames.filter(name=>env[name]).map(name=>({name,type:'secret_text'}))));
      const result = spawnSync(process.execPath, ['scripts/deploy-context-machine.mjs', '--production', ...(enabled === 'false' ? ['--provision-disabled'] : [])], {
        env: { ...env, CONTEXT_MACHINE_ENABLED: enabled, PATH: `${dir}:${process.env.PATH}`, CAPTURED: captured }, encoding: 'utf8',
      });
      assert.equal(result.status, 0, result.stderr);
      const upload = JSON.parse(readFileSync(captured, 'utf8'));
      assert.equal(upload.secrets.CONTEXT_MACHINE_ENABLED, enabled);
      for (const name of credentialNames) assert.equal(upload.secrets[name],undefined,name);
      assert.equal(upload.mode, 0o600);
      assert.deepEqual(upload.args.slice(-2), ['--env', 'production']);
      assert.equal(upload.secrets.DATABASE_URL, undefined);
      if (enabled === 'false') {
        for (const [name, value] of Object.entries(upload.secrets)) {
          if (!['CONTEXT_MACHINE_ENABLED', 'CONTEXT_MACHINE_TOKEN'].includes(name)) assert.equal(value, '', name);
        }
      } else assert.equal(upload.secrets.CODEX_MODEL,configured.CODEX_MODEL);
      assert.throws(() => readFileSync(upload.args[upload.args.indexOf('--secrets-file') + 1]), { code: 'ENOENT' });
    }
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

test('data-only inference needs no owner profile while database research profiles remain validated', () => {
 for(const profiles of ['', '[]']) {
  assert.equal(check({CONTEXT_MACHINE_PROFILES:profiles,LLM_PROVIDER:'context_machine'}).status,0);
  assert.equal(check({CONTEXT_MACHINE_PROFILES:profiles,LLM_PROVIDER:'context_machine',CODEX_AUTH_MODE:'subscription',CODEX_ACCESS_TOKEN:'test-only',OPENAI_API_KEY:'',LLM_ALLOW_UNCAPPED_OUTPUT_TOKENS:'true'}).status,0);
 }
});

test('personal Codex managed login and Claude subscriptions require one owner profile', () => {
  const auth = JSON.stringify({ auth_mode: 'chatgpt', tokens: { access_token: 'fake', refresh_token: 'fake', id_token: 'fake', account_id: 'owner' } });
  assert.equal(check({ OPENAI_API_KEY: '', CODEX_AUTH_JSON: auth, CODEX_AUTH_MODE: 'chatgpt' }).status, 0);
  assert.equal(check({ OPENAI_API_KEY: '', CODEX_AUTH_JSON: auth }).status, 0);
  for (const overrides of [
    { CODEX_AUTH_MODE: 'chatgpt' },
    { CLAUDE_AUTH_MODE: 'chatgpt', CODEX_AUTH_JSON: auth },
    { CODEX_AUTH_MODE: 'chatgpt', CODEX_AUTH_JSON: auth, CONTEXT_MACHINE_PROFILES: JSON.stringify([{ organization:'one',subject:'owner',access_profile:'one' }, { organization:'two',subject:'other',access_profile:'two' }]) },
    { CLAUDE_AUTH_MODE: 'subscription', CLAUDE_CODE_OAUTH_TOKEN: 'fake', CONTEXT_MACHINE_PROFILES: JSON.stringify([{ organization:'one',subject:'owner',access_profile:'one' }, { organization:'two',subject:'other',access_profile:'two' }]) },
  ]) assert.notEqual(check(overrides).status, 0);
});

test('an organization-wide profile needs API-key agents and the chat purpose is a known override', () => {
  const wide = JSON.stringify([{ organization: 'org_TEST', subject: '*', access_profile: 'firm-read' }]);
  assert.equal(check({ CONTEXT_MACHINE_PROFILES: wide }).status, 0);
  assert.equal(check({ CONTEXT_MACHINE_PROFILES: wide, LLM_PROVIDER: 'context_machine', LLM_MODELS: JSON.stringify({ chat: 'fast-model' }) }).status, 0);
  const auth = JSON.stringify({ auth_mode: 'chatgpt', tokens: { access_token: 'fake', refresh_token: 'fake', id_token: 'fake', account_id: 'owner' } });
  assert.notEqual(check({ OPENAI_API_KEY: '', CODEX_AUTH_JSON: auth, CODEX_AUTH_MODE: 'chatgpt', CONTEXT_MACHINE_PROFILES: wide }).status, 0);
  assert.notEqual(check({ CLAUDE_AUTH_MODE: 'subscription', CLAUDE_CODE_OAUTH_TOKEN: 'fake', CONTEXT_MACHINE_MODEL: 'claude', CLAUDE_MODEL: 'fixed', ANTHROPIC_API_KEY: '', CONTEXT_MACHINE_PROFILES: wide }).status, 0);
  assert.notEqual(check({ CODEX_AUTH_MODE: 'subscription', CODEX_ACCESS_TOKEN: 'fake', OPENAI_API_KEY: '', LLM_ALLOW_UNCAPPED_OUTPUT_TOKENS: 'true', CONTEXT_MACHINE_PROFILES: wide }).status, 0);
  assert.notEqual(check({ CODEX_ACCESS_TOKEN: 'fake', OPENAI_API_KEY: '', LLM_ALLOW_UNCAPPED_OUTPUT_TOKENS: 'true', CONTEXT_MACHINE_PROFILES: wide }).status, 0);
});

test('completion routing requires an enabled service and explicit subscription token-limit acknowledgement', () => {
  const disabled = check({ CONTEXT_MACHINE_ENABLED: 'false', LLM_PROVIDER: 'context_machine' });
  assert.notEqual(disabled.status, 0);
  assert.match(disabled.stderr, /requires CONTEXT_MACHINE_ENABLED=true/);
  const subscription = { LLM_PROVIDER: 'context_machine', CODEX_AUTH_MODE: 'subscription', CODEX_ACCESS_TOKEN: 'test-only-token', OPENAI_API_KEY: '' };
  const denied = check(subscription);
  assert.notEqual(denied.status, 0);
  assert.match(denied.stderr, /LLM_ALLOW_UNCAPPED_OUTPUT_TOKENS=true/);
  assert.equal(check({ ...subscription, LLM_ALLOW_UNCAPPED_OUTPUT_TOKENS: 'true' }).status, 0);
  const badEffort = check({ LLM_PROVIDER: 'context_machine', LLM_REASONING_EFFORTS: '{"work_command":"none"}' });
  assert.notEqual(badEffort.status, 0);
  assert.match(badEffort.stderr, /Invalid context-machine LLM_REASONING_EFFORTS/);
});

import {check as checkMergedCI,hasProvenance} from './check-merged-ci.mjs';
const pr={merged_at:'now',merge_commit_sha:'merge',base:{ref:'main'},head:{sha:'head'}};
function run(prs,states,attempts=3) {
 let n=0,waits=0;
 return { promise:checkMergedCI({repository:'owner/repo',sha:'merge',attempts,query:args=>args[0]==='api'?prs:states[Math.min(n++,states.length-1)],verify:async()=>true,pause:async()=>{waits++}}), waits:()=>waits };
}
test('a just-merged PR waits for its already-running CI, without rerunning checks',async()=>{
 const r=run([pr],[[{status:'in_progress',conclusion:''}],[{status:'completed',conclusion:'success'}]]);
 await r.promise; assert.equal(r.waits(),1);
});
test('failed, unrelated or absent CI cannot authorize deployment',async()=>{
 for(const prs of [[pr]]) {
  const r=run(prs,[[{status:'completed',conclusion:'failure'}]]);
  await assert.rejects(r.promise); assert.equal(r.waits(),0);
 }
});
test('pending CI has a bounded wait',async()=>{
 const r=run([pr],[[{status:'in_progress'}]]);
 await assert.rejects(r.promise,/still unavailable/); assert.equal(r.waits(),2);
});

test('merge association and CI artifact metadata can lag without losing a green deployment',async()=>{
 let associations=0,checks=0,waits=0;
 await checkMergedCI({repository:'owner/repo',sha:'merge',attempts:4,
  query:args=>args[0]==='api'?(++associations===1?[]:[pr]):[{status:'completed',conclusion:'success',databaseId:7}],
  verify:async(id,head)=>{assert.equal(id,7);assert.equal(head,'head');return ++checks>1},pause:async()=>{waits++}});
 assert.equal(waits,2);
});
test('green CI for a different merge tree cannot authorize deployment',async()=>{
 await assert.rejects(checkMergedCI({repository:'owner/repo',sha:'merge',query:args=>args[0]==='api'?[pr]:[{status:'completed',conclusion:'success'}],verify:async()=>{throw new Error('merged tree differs')},pause:async()=>{}}),/merged tree differs/);
});
test('API OCR requires explicit image consent before any deployment mutation',()=>{
 assert.notEqual(check({LLM_API_IMAGE_ENABLED:'yes'}).status,0);
 for(const name of ['LLM_MODELS','LLM_REASONING_EFFORTS']) assert.notEqual(check({CONTEXT_MACHINE_ENABLED:'false',[name]:'{"ocr":"medium"}'}).status,0);
 assert.equal(check({CONTEXT_MACHINE_ENABLED:'false',LLM_API_IMAGE_ENABLED:'true',LLM_MODELS:'{"ocr":"gpt-6.1-sol"}'}).status,0);
});

test('expired provenance fails promptly with the right recovery instruction',()=>{
 assert.throws(()=>hasProvenance([{name:'ci-provenance',expired:true}]),/expired; rerun CI/);
 assert.equal(hasProvenance([]),false);
 assert.equal(hasProvenance([{name:'ci-provenance',expired:false}]),true);
});
