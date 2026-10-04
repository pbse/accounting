interface LLMEnv {
 OPENAI_API_KEY?: string; OPENAI_MODEL?: string; OPENAI_REASONING_EFFORT?: string;
 LLM_REASONING_EFFORTS?: string; LLM_MODELS?: string; LLM_PROVIDER?: string;
 LLM_ALLOW_UNCAPPED_OUTPUT_TOKENS?: string; CONTEXT_MACHINE_PROFILES?: string;
 CODEX_MODEL?: string; CLAUDE_MODEL?: string; CODEX_REASONING_EFFORT?: string; CLAUDE_REASONING_EFFORT?: string;
 CONTEXT_MACHINE_TOKEN?: string; CONTEXT_MACHINE_MODEL?: string;
}
// Retain credentials in Cloudflare while exposing only the selected routes to
// containers. An absent context-machine model is the explicit standby policy.
export function llmEnvironment(env: LLMEnv): Record<string,string> {
 const context = env.LLM_PROVIDER === 'context_machine';
 const api = !context && !!env.OPENAI_API_KEY;
 const research = !!env.CONTEXT_MACHINE_MODEL;
 return {
  OPENAI_API_KEY: api ? env.OPENAI_API_KEY! : '',
  OPENAI_MODEL: api ? env.OPENAI_MODEL ?? '' : '',
  OPENAI_REASONING_EFFORT: api ? env.OPENAI_REASONING_EFFORT ?? '' : '',
  LLM_REASONING_EFFORTS: context || api ? env.LLM_REASONING_EFFORTS ?? '' : '',
  LLM_MODELS: context || api ? env.LLM_MODELS ?? '' : '',
  LLM_PROVIDER: env.LLM_PROVIDER ?? '',
  LLM_ALLOW_UNCAPPED_OUTPUT_TOKENS: env.LLM_ALLOW_UNCAPPED_OUTPUT_TOKENS ?? '',
  CONTEXT_MACHINE_PROFILES: env.CONTEXT_MACHINE_PROFILES ?? '',
  CODEX_MODEL: env.CODEX_MODEL ?? '', CLAUDE_MODEL: env.CLAUDE_MODEL ?? '',
  CODEX_REASONING_EFFORT: env.CODEX_REASONING_EFFORT ?? '', CLAUDE_REASONING_EFFORT: env.CLAUDE_REASONING_EFFORT ?? '',
  CONTEXT_MACHINE_TOKEN: research ? env.CONTEXT_MACHINE_TOKEN ?? '' : '',
  CONTEXT_MACHINE_MODEL: research ? env.CONTEXT_MACHINE_MODEL! : '',
  CONTEXT_MACHINE_ENDPOINT: research ? 'http://context-machine.internal/v1/run' : '',
 };
}
