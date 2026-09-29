import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';

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
