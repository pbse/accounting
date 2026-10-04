import { Container } from "@cloudflare/containers";
export { ContainerProxy } from "@cloudflare/containers";
import { credentialRequest } from "./auth.ts";

interface Env {
  MACHINE: DurableObjectNamespace<ContextMachine>;
  CONTEXT_MACHINE_TOKEN: string;
  CONTEXT_MACHINE_ENABLED?: string;
  CONTEXT_MACHINE_DATABASE_URL: string;
  CONTEXT_MACHINE_PROFILES: string;
  OPENAI_API_KEY: string;
  ANTHROPIC_API_KEY: string;
  CODEX_MODEL: string;
  CLAUDE_MODEL: string;
  CODEX_AUTH_MODE?: string;
  CLAUDE_AUTH_MODE?: string;
  CODEX_REASONING_EFFORT?: string;
  CLAUDE_REASONING_EFFORT?: string;
  CODEX_ACCESS_TOKEN?: string;
  CODEX_AUTH_JSON?: string;
  CLAUDE_CODE_OAUTH_TOKEN?: string;
  CONTEXT_MACHINE_MODEL?: string;
 LLM_MODELS?: string;
 LLM_REASONING_EFFORTS?: string;

}

export class ContextMachine extends Container<Env> {
  async credentials(request: Request): Promise<Response> {
    return credentialRequest(request, this.env.CONTEXT_MACHINE_TOKEN, this.env.CODEX_AUTH_JSON ?? "", this.ctx.storage);
  }
  defaultPort = 8080;
  sleepAfter = "5m";
  envVars = {
    CONTEXT_MACHINE_TOKEN: this.env.CONTEXT_MACHINE_TOKEN,
    CONTEXT_MACHINE_DATABASE_URL: this.env.CONTEXT_MACHINE_DATABASE_URL,
    CONTEXT_MACHINE_PROFILES: this.env.CONTEXT_MACHINE_PROFILES,
    OPENAI_API_KEY: this.env.OPENAI_API_KEY,
    ANTHROPIC_API_KEY: this.env.ANTHROPIC_API_KEY,
    CODEX_MODEL: this.env.CODEX_MODEL,
    CLAUDE_MODEL: this.env.CLAUDE_MODEL,
    CODEX_AUTH_MODE: this.env.CODEX_AUTH_MODE ?? "",
    CLAUDE_AUTH_MODE: this.env.CLAUDE_AUTH_MODE ?? "",
    CODEX_REASONING_EFFORT: this.env.CODEX_REASONING_EFFORT ?? "",
    CLAUDE_REASONING_EFFORT: this.env.CLAUDE_REASONING_EFFORT ?? "",
    CODEX_ACCESS_TOKEN: this.env.CODEX_ACCESS_TOKEN ?? "",
    CODEX_AUTH_JSON: this.env.CODEX_AUTH_JSON ?? "",
    CODEX_AUTH_STATE_URL: "http://context-machine-auth.internal/codex",
    CLAUDE_CODE_OAUTH_TOKEN: this.env.CLAUDE_CODE_OAUTH_TOKEN ?? "",
    CONTEXT_MACHINE_MODEL: this.env.CONTEXT_MACHINE_MODEL ?? "",
 LLM_MODELS: this.env.LLM_MODELS ?? "",
 LLM_REASONING_EFFORTS: this.env.LLM_REASONING_EFFORTS ?? "",

  };
}

ContextMachine.outboundByHost = {
  "context-machine-auth.internal": async (request, env) =>
    (env as Env).MACHINE.getByName("context-machine").credentials(request),
};

// No public route or workers.dev hostname: only a Worker service binding.
export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const path = new URL(request.url).pathname;
    if (!["/v1/run", "/v1/complete", "/readyz"].includes(path)) return new Response(null, { status: 404 });
    if (!env.CONTEXT_MACHINE_TOKEN || env.CONTEXT_MACHINE_TOKEN.length < 32 || request.headers.get("Authorization") !== `Bearer ${env.CONTEXT_MACHINE_TOKEN}`)
      return new Response(null, { status: 401 });
    if (env.CONTEXT_MACHINE_ENABLED !== "true")
      return Response.json({ status: "disabled" }, { status: 503, headers: { "Cache-Control": "no-store" } });
    return env.MACHINE.getByName("context-machine").fetch(request);
  },
} satisfies ExportedHandler<Env>;
