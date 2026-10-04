import test from 'node:test';
import assert from 'node:assert/strict';
import { llmEnvironment } from './llm.ts';

test('stored credentials do not activate standby research or context-routed API calls', () => {
 const env={OPENAI_API_KEY:'private-key',OPENAI_MODEL:'approved',CONTEXT_MACHINE_TOKEN:'private-token',LLM_MODELS:'{"plan":"approved"}'};
 const standby=llmEnvironment(env);
 assert.equal(standby.CONTEXT_MACHINE_TOKEN,'');assert.equal(standby.CONTEXT_MACHINE_ENDPOINT,'');
 assert.equal(standby.OPENAI_API_KEY,env.OPENAI_API_KEY);
 const context=llmEnvironment({...env,LLM_PROVIDER:'context_machine',CONTEXT_MACHINE_MODEL:'codex'});
 assert.equal(context.OPENAI_API_KEY,'');assert.equal(context.OPENAI_MODEL,'');
 assert.equal(context.CONTEXT_MACHINE_TOKEN,env.CONTEXT_MACHINE_TOKEN);assert.equal(context.LLM_MODELS,env.LLM_MODELS);
 assert.equal(env.OPENAI_API_KEY,'private-key');
 const absent=llmEnvironment({...env,OPENAI_API_KEY:''});
 assert.equal(absent.OPENAI_MODEL,'');assert.equal(absent.LLM_MODELS,'');
 // Missing credentials for an explicitly active route still reach strict
 // gateway startup validation instead of silently disabling the route.
 const incomplete=llmEnvironment({...env,CONTEXT_MACHINE_MODEL:'codex',CONTEXT_MACHINE_TOKEN:''});
 assert.equal(incomplete.CONTEXT_MACHINE_MODEL,'codex');assert.ok(incomplete.CONTEXT_MACHINE_ENDPOINT);
});
