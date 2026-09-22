import { Container } from "@cloudflare/containers";
import { deliver } from "./delivery";

export class PreparationWorker extends Container<Env> {
  envVars = {
    DATABASE_URL: this.env.DATABASE_URL,
    SECRETS_KEY: this.env.SECRETS_KEY ?? "",
    OPENAI_API_KEY: this.env.OPENAI_API_KEY ?? "",
    OPENAI_MODEL: this.env.OPENAI_MODEL ?? "",
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
