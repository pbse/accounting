import { Container } from "@cloudflare/containers";
import { deliver } from "./delivery";

export class PreparationWorker extends Container<Env> {
  envVars = {
    DATABASE_URL: this.env.DATABASE_URL,
    SECRETS_KEY: this.env.SECRETS_KEY ?? "",
    OPENAI_API_KEY: this.env.OPENAI_API_KEY ?? "",
    OPENAI_MODEL: this.env.OPENAI_MODEL ?? "",
    OPERATIONS_ALERT_WEBHOOK_URL: this.env.OPERATIONS_ALERT_WEBHOOK_URL ?? "",
    OPERATIONS_ALERT_WEBHOOK_TOKEN: this.env.OPERATIONS_ALERT_WEBHOOK_TOKEN ?? "",
    QBO_CLIENT_ID: this.env.QBO_CLIENT_ID ?? "",
    QBO_CLIENT_SECRET: this.env.QBO_CLIENT_SECRET ?? "",
    QBO_REDIRECT_URI: this.env.QBO_REDIRECT_URI ?? "",
    PUBLIC_APP_URL: this.env.PUBLIC_APP_URL ?? "",
    UPLOAD_SCAN_KEY: this.env.UPLOAD_SCAN_KEY ?? "",
    TEXT_SCANNER_URL: this.env.TEXT_SCANNER_URL ?? "",
  };
  defaultPort = 8080;
  sleepAfter = "5m";
  entrypoint = ["/worker"];
}

interface Env {
  DATABASE_URL: string;
  SECRETS_KEY?: string;
  OPENAI_API_KEY?: string;
  OPENAI_MODEL?: string;
  OPERATIONS_ALERT_WEBHOOK_URL?: string;
  OPERATIONS_ALERT_WEBHOOK_TOKEN?: string;
  QBO_CLIENT_ID?: string;
  QBO_CLIENT_SECRET?: string;
  QBO_REDIRECT_URI?: string;
  PUBLIC_APP_URL?: string;
  UPLOAD_SCAN_KEY?: string;
  TEXT_SCANNER_URL?: string;
  WORKER: DurableObjectNamespace<PreparationWorker>;
}

export default {
  async scheduled(_event: ScheduledController, env: Env): Promise<void> {
    // Wake the existing worker; Postgres owns queued work and replay protection.
    const response = await env.WORKER.getByName("preparation-worker").fetch(
      "http://worker/healthz",
    );
    if (!response.ok) throw new Error("Preparation worker is unavailable");
  },
  async queue(batch: MessageBatch<unknown>, env: Env): Promise<void> {
    const worker = env.WORKER.getByName("reference-worker");
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
