import { test } from 'node:test';
import assert from 'node:assert/strict';
import { registerHooks } from 'node:module';

// Only the platform Container base is mocked; exercise the actual Worker handler.
const hooks = registerHooks({ resolve(specifier, context, next) {
  if (specifier === '@cloudflare/containers') return { url: 'data:text/javascript,export class Container {}', shortCircuit: true };
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
