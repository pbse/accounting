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
const check = (overrides) => spawnSync(process.execPath, ['scripts/deploy-context-machine.mjs', '--check'], { env: { ...configured, ...overrides }, encoding: 'utf8' });

test('deployment accepts complete private-service configuration', () => {
  assert.equal(check({}).status, 0);
});
test('deployment fails before mutation for missing credentials, unsafe DB or invalid profiles', () => {
  for (const overrides of [
    { OPENAI_API_KEY: '' }, { CONTEXT_MACHINE_TOKEN: 'short' },
    { CONTEXT_MACHINE_DATABASE_URL: configured.DATABASE_URL },
    { CONTEXT_MACHINE_DATABASE_URL: 'postgres://reader:test@db.example/close' },
    { CONTEXT_MACHINE_PROFILES: 'null' }, { CONTEXT_MACHINE_PROFILES: '[]' },
    { CONTEXT_MACHINE_MODEL: 'shell' },
    { CONTEXT_MACHINE_DATABASE_URL: 'invalid secret-credential' },
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
  for (const name of ['CONTEXT_MACHINE_DATABASE_URL', 'CONTEXT_MACHINE_PROFILES', 'CODEX_MODEL', 'OPENAI_API_KEY or CODEX_ACCESS_TOKEN']) assert.ok(result.stderr.includes(name), name);
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
    assert.equal(readFileSync(envFile, 'utf8'), 'CONTEXT_MACHINE_TOKEN=\nCONTEXT_MACHINE_MODEL=\nOPENAI_MODEL=\nOPENAI_REASONING_EFFORT=\nLLM_MODELS=\nLLM_REASONING_EFFORTS=\n');
    writeFileSync(envFile, '');
    const active = spawnSync(process.execPath, [script, '--prepare-callers'], { cwd: dir, env: { ...configured, GITHUB_ENV: envFile }, encoding: 'utf8' });
    assert.equal(active.status, 0, active.stderr);
    assert.equal(readFileSync(envFile, 'utf8'), '');
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

test('standby provisioning needs only a strong service token and explicit disabled mode', () => {
  const provision = (env) => spawnSync(process.execPath, ['scripts/deploy-context-machine.mjs', '--provision-disabled', '--check'], { env, encoding: 'utf8' });
  const standby = { CONTEXT_MACHINE_ENABLED: 'false', CONTEXT_MACHINE_TOKEN: configured.CONTEXT_MACHINE_TOKEN };
  assert.equal(provision(standby).status, 0);
  for (const env of [{ ...standby, CONTEXT_MACHINE_TOKEN: '' }, { ...standby, CONTEXT_MACHINE_TOKEN: 'short' }, { ...configured, CONTEXT_MACHINE_ENABLED: 'true' }, { CONTEXT_MACHINE_TOKEN: configured.CONTEXT_MACHINE_TOKEN }]) {
    assert.notEqual(provision(env).status, 0);
  }
});

test('deployment uploads the enabled flag with scoped secrets in both modes', () => {
  const dir = mkdtempSync(join(tmpdir(), 'close-deploy-secrets-'));
  const captured = join(dir, 'captured.json');
  try {
    writeFileSync(join(dir, 'npx'), `#!${process.execPath}
const fs = require('node:fs');
const file = process.argv[process.argv.indexOf('--secrets-file') + 1];
fs.writeFileSync(process.env.CAPTURED, JSON.stringify({ args: process.argv.slice(2), secrets: JSON.parse(fs.readFileSync(file)), mode: fs.statSync(file).mode & 0o777 }));
`, { mode: 0o755 });
    for (const enabled of ['true', 'false']) {
      const env = enabled === 'true' ? configured : { CONTEXT_MACHINE_TOKEN: configured.CONTEXT_MACHINE_TOKEN };
      const result = spawnSync(process.execPath, ['scripts/deploy-context-machine.mjs', '--production', ...(enabled === 'false' ? ['--provision-disabled'] : [])], {
        env: { ...env, CONTEXT_MACHINE_ENABLED: enabled, PATH: `${dir}:${process.env.PATH}`, CAPTURED: captured }, encoding: 'utf8',
      });
      assert.equal(result.status, 0, result.stderr);
      const upload = JSON.parse(readFileSync(captured, 'utf8'));
      assert.equal(upload.secrets.CONTEXT_MACHINE_ENABLED, enabled);
      assert.equal(upload.secrets.CONTEXT_MACHINE_TOKEN, configured.CONTEXT_MACHINE_TOKEN);
      assert.equal(upload.mode, 0o600);
      assert.deepEqual(upload.args.slice(-2), ['--env', 'production']);
      assert.equal(upload.secrets.DATABASE_URL, undefined);
      if (enabled === 'false') {
        for (const [name, value] of Object.entries(upload.secrets)) {
          if (!['CONTEXT_MACHINE_ENABLED', 'CONTEXT_MACHINE_TOKEN'].includes(name)) assert.equal(value, '', name);
        }
      } else assert.equal(upload.secrets.OPENAI_API_KEY, configured.OPENAI_API_KEY);
      assert.throws(() => readFileSync(upload.args[upload.args.indexOf('--secrets-file') + 1]), { code: 'ENOENT' });
    }
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

test('personal Codex managed login and Claude subscriptions require one owner profile', () => {
  const auth = JSON.stringify({ auth_mode: 'chatgpt', tokens: { access_token: 'fake', refresh_token: 'fake', id_token: 'fake', account_id: 'owner' } });
  assert.equal(check({ OPENAI_API_KEY: '', CODEX_AUTH_JSON: auth, CODEX_AUTH_MODE: 'chatgpt' }).status, 0);
  assert.equal(check({ OPENAI_API_KEY: '', CODEX_AUTH_JSON: auth }).status, 0);
  for (const overrides of [
    { CODEX_AUTH_MODE: 'chatgpt' },
    { CODEX_AUTH_MODE: 'chatgpt', CODEX_AUTH_JSON: '{}' },
    { CODEX_AUTH_JSON: auth.replace('chatgpt', 'api_key') },
    { CLAUDE_AUTH_MODE: 'chatgpt', CODEX_AUTH_JSON: auth },
    { CODEX_AUTH_MODE: 'chatgpt', CODEX_AUTH_JSON: auth, CONTEXT_MACHINE_PROFILES: JSON.stringify([{ organization:'one',subject:'owner',access_profile:'one' }, { organization:'two',subject:'other',access_profile:'two' }]) },
    { CLAUDE_AUTH_MODE: 'subscription', CLAUDE_CODE_OAUTH_TOKEN: 'fake', CONTEXT_MACHINE_PROFILES: JSON.stringify([{ organization:'one',subject:'owner',access_profile:'one' }, { organization:'two',subject:'other',access_profile:'two' }]) },
  ]) assert.notEqual(check(overrides).status, 0);
});

test('personal auth seed is compacted and checked against Worker secret limits before deployment', () => {
 const auth = { auth_mode:'chatgpt',tokens:{access_token:'fake',refresh_token:'fake',id_token:'fake',account_id:'owner'} };
 assert.equal(check({ CODEX_AUTH_MODE:'chatgpt',CODEX_AUTH_JSON:' '.repeat(6000)+JSON.stringify(auth) }).status,0);
 auth.tokens.access_token='x'.repeat(5000);
 const result=check({ CODEX_AUTH_MODE:'chatgpt',CODEX_AUTH_JSON:JSON.stringify(auth) });
 assert.notEqual(result.status,0);
 assert.match(result.stderr,/5 KB secret limit/);
 assert.ok(!result.stderr.includes(auth.tokens.access_token));
});
