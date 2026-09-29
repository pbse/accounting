import { Container } from "@cloudflare/containers";

interface Env {
  MACHINE: DurableObjectNamespace<ContextMachine>;
  CONTEXT_MACHINE_TOKEN: string;
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
  CLAUDE_CODE_OAUTH_TOKEN?: string;
  CONTEXT_MACHINE_MODEL?: string;

}

export class ContextMachine extends Container<Env> {
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
    CLAUDE_CODE_OAUTH_TOKEN: this.env.CLAUDE_CODE_OAUTH_TOKEN ?? "",
    CONTEXT_MACHINE_MODEL: this.env.CONTEXT_MACHINE_MODEL ?? "",

  };
}

// No public route or workers.dev hostname: only a Worker service binding.
export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const path = new URL(request.url).pathname;
    if (!["/v1/run", "/readyz"].includes(path)) return new Response(null, { status: 404 });
    if (request.headers.get("Authorization") !== `Bearer ${env.CONTEXT_MACHINE_TOKEN}`)
      return new Response(null, { status: 401 });
    return env.MACHINE.getByName("context-machine").fetch(request);
  },
} satisfies ExportedHandler<Env>;
