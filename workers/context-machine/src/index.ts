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
