import { test } from 'node:test';
import assert from 'node:assert/strict';
import { registerHooks } from 'node:module';

// Only the platform Container base is mocked; exercise the actual Worker handler.
const hooks = registerHooks({ resolve(specifier, context, next) {
  if (specifier === '@cloudflare/containers') return { url: 'data:text/javascript,export class Container {}; export class ContainerProxy {}', shortCircuit: true };
  return next(specifier, context);
} });
const { default: worker } = await import('../workers/context-machine/src/index.ts');
hooks.deregister();

test('standby authenticates and refuses research without starting a container; enabled forwards', async () => {
  let calls = 0;
  const token = 'test-only-0000000000000000000000000000';
  const env = { CONTEXT_MACHINE_TOKEN: token, MACHINE: { getByName(name) {
    assert.equal(name, 'context-machine');
    calls++;
    return { fetch: async () => new Response('container response') };
  } } };
  for (const enabled of [undefined, '', 'false', 'invalid', 'true']) {
    env.CONTEXT_MACHINE_ENABLED = enabled;
    for (const path of ['/readyz', '/v1/run']) {
      const request = new Request(`https://context-machine.internal${path}`, { headers: { Authorization: `Bearer ${token}` } });
      assert.equal((await worker.fetch(new Request(request.url), env)).status, 401);
      assert.equal((await worker.fetch(request, { ...env, CONTEXT_MACHINE_TOKEN: '' })).status, 401);
      const response = await worker.fetch(request, env);
      if (enabled === 'true') {
        assert.equal(await response.text(), 'container response');
      } else {
        assert.equal(response.status, 503);
        assert.equal(response.headers.get('Cache-Control'), 'no-store');
        assert.deepEqual(await response.json(), { status: 'disabled' });
        assert.equal(calls, 0);
      }
    }
  }
  assert.equal(calls, 2);
  assert.equal((await worker.fetch(new Request('https://context-machine.internal/unknown'), env)).status, 404);
  assert.equal(calls, 2);
});

test('private auth store serializes refresh and survives restarts without exposing credentials through fetch', async () => {
  const { credentialRequest } = await import('../workers/context-machine/src/auth.ts');
  let state;
  const storage = { transaction: async fn => fn({ get: async () => structuredClone(state), put: async (_key, value) => { state = structuredClone(value); } }) };
  const token = 'service-only-0000000000000000000000000';
  const seedJSON = JSON.stringify({ auth_mode: 'chatgpt', tokens: { access_token: 'fake', refresh_token: 'original', id_token: 'fake', account_id: 'owner' } });
  const seed = Buffer.from(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(seedJSON))).toString('hex');
  const lease = 'lease-00000000000000000000001';
  const request = (method, body, overrides = {}) => new Request('http://context-machine-auth.internal/codex', { method, body, headers: { Authorization: `Bearer ${token}`, 'X-Auth-Seed': seed, 'X-Auth-Lease': lease, ...overrides } });
  const call = req => credentialRequest(req, token, seedJSON, storage);
  assert.equal((await call(request('POST', undefined, { Authorization: 'Bearer wrong' }))).status, 401);
  assert.equal(state, undefined);
  assert.equal((await call(request('POST', undefined, { 'X-Auth-Seed': 'old' }))).status, 409);
  const first = await call(request('POST'));
  assert.equal(first.status, 200);
  assert.equal(first.headers.get('Cache-Control'), 'no-store');
  assert.equal(await first.text(), seedJSON);
  assert.equal((await call(request('POST', undefined, { 'X-Auth-Lease': 'lease-00000000000000000000002' }))).status, 429);
  assert.equal((await call(request('PUT', seedJSON, { 'X-Auth-Lease': 'lease-00000000000000000000002' }))).status, 409);
  assert.equal((await call(request('PUT', seedJSON.replace('owner', 'other')))).status, 400);
  assert.equal((await call(request('PUT', 'x'.repeat(32769)))).status, 413);
  const rotated = seedJSON.replace('original', 'rotated');
  assert.equal((await call(request('PUT', rotated, { 'X-Auth-Release': 'true' }))).status, 200);
  assert.equal(await (await call(request('POST'))).text(), rotated);
  // A crashed/expired lease cannot reuse a possibly stale refresh token.
  state.expires = Date.now() - 1;
  assert.equal((await call(request('POST'))).status, 503);
  assert.equal((await call(request('PUT', seedJSON))).status, 409);
  // New login reseeds, but the old container cannot overwrite it.
  const newSeed = seedJSON.replace('original', 'new-login');
  const newHash = Buffer.from(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(newSeed))).toString('hex');
  assert.equal((await credentialRequest(request('POST', undefined, { 'X-Auth-Seed': newHash }), token, newSeed, storage)).status, 200);
  assert.equal((await credentialRequest(request('PUT', rotated), token, newSeed, storage)).status, 409);
  const env = { CONTEXT_MACHINE_TOKEN: token, CONTEXT_MACHINE_ENABLED: 'true', MACHINE: { getByName: () => { throw Error('must not forward auth requests'); } } };
  assert.equal((await worker.fetch(request('POST'), env)).status, 404);
});

test('aborting a restore releases only its own lease without modifying the stored credential', async () => {
 const { credentialRequest } = await import('../workers/context-machine/src/auth.ts');
 const raw = JSON.stringify({ auth_mode:'chatgpt', tokens:{ access_token:'fake',refresh_token:'fake',id_token:'fake',account_id:'owner' } });
 const seed = Buffer.from(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(raw))).toString('hex');
 const token = 'service-only-0000000000000000000000000';
 let state;
 const storage = { transaction: async fn => fn({ get: async () => structuredClone(state), put: async (_key,v) => { state=structuredClone(v); } }) };
 const req = (method,lease='lease-00000000000000000000001') => new Request('http://context-machine-auth.internal/codex',{method,headers:{Authorization:`Bearer ${token}`,'X-Auth-Seed':seed,'X-Auth-Lease':lease}});
 const call = r => credentialRequest(r,token,raw,storage);
 assert.equal((await call(req('POST'))).status,200);
 assert.equal((await call(req('DELETE','lease-00000000000000000000002'))).status,409);
 assert.equal((await call(req('DELETE'))).status,200);
 assert.equal(state.json,raw);
 assert.equal((await call(req('POST'))).status,200);
});
