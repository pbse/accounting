import { llmEnvironment } from '../../shared/llm';
import { Container } from "@cloudflare/containers";
// Required entrypoint for Cloudflare container outbound service interception.
export { ContainerProxy } from "@cloudflare/containers";
import { deliver } from "./delivery";

export class PreparationWorker extends Container<Env> {
  envVars = {
    DATABASE_URL: this.env.DATABASE_URL,
    SECRETS_KEY: this.env.SECRETS_KEY ?? "",
    ...llmEnvironment(this.env),
    OPERATIONS_ALERT_WEBHOOK_URL: this.env.OPERATIONS_ALERT_WEBHOOK_URL ?? "",
    OPERATIONS_ALERT_WEBHOOK_TOKEN: this.env.OPERATIONS_ALERT_WEBHOOK_TOKEN ?? "",
    QBO_CLIENT_ID: this.env.QBO_CLIENT_ID ?? "",
    QBO_CLIENT_SECRET: this.env.QBO_CLIENT_SECRET ?? "",
    QBO_REDIRECT_URI: this.env.QBO_REDIRECT_URI ?? "",
    QBO_PRODUCTION_CLIENT_ID: this.env.QBO_PRODUCTION_CLIENT_ID ?? "",
    QBO_PRODUCTION_CLIENT_SECRET: this.env.QBO_PRODUCTION_CLIENT_SECRET ?? "",
    QBO_PRODUCTION_REDIRECT_URI: this.env.QBO_PRODUCTION_REDIRECT_URI ?? "",
    XERO_CLIENT_ID: this.env.XERO_CLIENT_ID ?? "",
    XERO_CLIENT_SECRET: this.env.XERO_CLIENT_SECRET ?? "",
    XERO_REDIRECT_URI: this.env.XERO_REDIRECT_URI ?? "",
    PUBLIC_APP_URL: this.env.PUBLIC_APP_URL ?? "",
    UPLOAD_SCAN_KEY: this.env.UPLOAD_SCAN_KEY ?? "",
    TEXT_SCANNER_URL: this.env.TEXT_SCANNER_URL ?? "",
    WORKOS_API_KEY: this.env.WORKOS_API_KEY ?? "",
    PLATFORM_EMAIL_KEY: this.env.PLATFORM_EMAIL_KEY ?? "",
    PLATFORM_EMAIL_FROM: this.env.PLATFORM_EMAIL_FROM ?? "",
    // Krith's sender for firms' client email: Krith's address, sent with PLATFORM_EMAIL_KEY.
    CLIENT_EMAIL_ADDRESS: this.env.CLIENT_EMAIL_ADDRESS ?? "",
    // Google Gemini reads every PDF page when its key is installed (scripts/cloudflare-credentials.mjs); blank model: the default.
    GEMINI_API_KEY: this.env.GEMINI_API_KEY ?? "",
    GEMINI_OCR_MODEL: this.env.GEMINI_OCR_MODEL ?? "",
    // "confirmed", installed with the key by the credential script's --paid-service once the operator has checked the
    // key's project is on Google's paid service; no page is sent before.
    GEMINI_PAID_SERVICE: this.env.GEMINI_PAID_SERVICE ?? "",
  };
  defaultPort = 8080;
  sleepAfter = "5m";
  entrypoint = ["/worker"];
}

// Containers reach private Worker bindings through a fixed virtual host.
PreparationWorker.outboundByHost = {
  "context-machine.internal": async (request, env) => (env as Env).CONTEXT_MACHINE?.fetch(request) ?? new Response(null, { status: 503 }),
};

interface Env {
  DATABASE_URL: string;
  SECRETS_KEY?: string;
  OPENAI_API_KEY?: string;
  OPENAI_MODEL?: string;
  OPENAI_REASONING_EFFORT?: string;
  LLM_REASONING_EFFORTS?: string;
  LLM_MODELS?: string;
  LLM_PROVIDER?: string;
  LLM_API_IMAGE_ENABLED?: string;
  LLM_ALLOW_UNCAPPED_OUTPUT_TOKENS?: string;
  CONTEXT_MACHINE_PROFILES?: string;
  CODEX_MODEL?: string;
  CLAUDE_MODEL?: string;
  CODEX_REASONING_EFFORT?: string;
  CLAUDE_REASONING_EFFORT?: string;

  CONTEXT_MACHINE_TOKEN?: string;
  CONTEXT_MACHINE_MODEL?: string;
  CONTEXT_MACHINE?: Fetcher;
  OPERATIONS_ALERT_WEBHOOK_URL?: string;
  OPERATIONS_ALERT_WEBHOOK_TOKEN?: string;
  QBO_CLIENT_ID?: string;
  QBO_CLIENT_SECRET?: string;
  QBO_REDIRECT_URI?: string;
  QBO_PRODUCTION_CLIENT_ID?: string;
  QBO_PRODUCTION_CLIENT_SECRET?: string;
  QBO_PRODUCTION_REDIRECT_URI?: string;
  XERO_CLIENT_ID?: string;
  XERO_CLIENT_SECRET?: string;
  XERO_REDIRECT_URI?: string;
  PUBLIC_APP_URL?: string;
  UPLOAD_SCAN_KEY?: string;
  TEXT_SCANNER_URL?: string;
  WORKOS_API_KEY?: string;
  PLATFORM_EMAIL_KEY?: string;
  PLATFORM_EMAIL_FROM?: string;
  CLIENT_EMAIL_ADDRESS?: string;
  GEMINI_API_KEY?: string;
  GEMINI_OCR_MODEL?: string;
  GEMINI_PAID_SERVICE?: string;
  WORKER: DurableObjectNamespace<PreparationWorker>;
}

// The worker's Durable Object, which starts its container, lives where it was first made and never moves; this one is
// made in eastern North America, with the container and the database. The earlier "preparation-worker" was in Paris.
const preparationWorker = (env: Env) => env.WORKER.getByName("preparation-worker-enam", { locationHint: "enam" });

export default {
  async scheduled(_event: ScheduledController, env: Env): Promise<void> {
    // Wake the existing worker; Postgres owns queued work and replay protection.
    const response = await preparationWorker(env).fetch(
      "http://worker/healthz",
    );
    if (!response.ok) throw new Error("Preparation worker is unavailable");
  },
  async queue(batch: MessageBatch<unknown>, env: Env): Promise<void> {
    const worker = preparationWorker(env);
    for (const message of batch.messages) {
      try {
        await deliver(message.body, (request) => worker.fetch(request));
        message.ack();
      } catch {
        message.retry({ delaySeconds: 60 });
      }
    }
  },
} satisfies ExportedHandler<Env>;
