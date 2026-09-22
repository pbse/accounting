import { Container } from "@cloudflare/containers";
interface Env { UPLOAD_SCAN_KEY: string; SCANNER: DurableObjectNamespace<TextScanner> }
export class TextScanner extends Container<Env> {
  defaultPort = 8080;
  sleepAfter = "2m";
  envVars = { UPLOAD_SCAN_KEY: this.env.UPLOAD_SCAN_KEY };
}
export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    // Authenticate before starting a container; this URL is server-to-server only.
    if (request.method !== "POST" || new URL(request.url).pathname !== "/scan" ||
        !env.UPLOAD_SCAN_KEY || env.UPLOAD_SCAN_KEY.length < 32 ||
        request.headers.get("Authorization") !== `Bearer ${env.UPLOAD_SCAN_KEY}`) {
      return new Response("Unavailable", { status: 403 });
    }
    const size = Number(request.headers.get("Content-Length"));
    if (!Number.isInteger(size) || size < 1 || size > 512 * 1024) return new Response("Invalid size", { status: 413 });
    return env.SCANNER.getByName("text-scanner").fetch(request);
  },
} satisfies ExportedHandler<Env>;
