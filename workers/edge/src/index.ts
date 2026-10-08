import { llmEnvironment } from '../../shared/llm';
import { Container } from "@cloudflare/containers";
// Required entrypoint for Cloudflare container outbound service interception.
export { ContainerProxy } from "@cloudflare/containers";
import { adminAsset, adminPath, forwardsToAPI, needsWorkspaceAuth, secureAdmin, staffGate } from "./routes";
import { uploadScanRequest } from "./upload-scan";
import { EmailMessage } from "cloudflare:email";
import { contactMessage, noteToForward } from "./contact";

export class CapabilityAPI extends Container<Env> {
  defaultPort = 8080;
  envVars = {
    UPLOAD_SCAN_KEY: this.env.UPLOAD_SCAN_KEY ?? "",
    TEXT_SCANNER_URL: this.env.TEXT_SCANNER_URL ?? "",
    FRONTEND_ORIGIN: this.env.FRONTEND_ORIGIN ?? "",
    DATABASE_URL: this.env.DATABASE_URL ?? "",
    ...llmEnvironment(this.env),
    SECRETS_KEY: this.env.SECRETS_KEY ?? "",
    QBO_CLIENT_ID: this.env.QBO_CLIENT_ID ?? "",
    QBO_CLIENT_SECRET: this.env.QBO_CLIENT_SECRET ?? "",
    QBO_REDIRECT_URI: this.env.QBO_REDIRECT_URI ?? "",
    QBO_WEBHOOK_VERIFIER: this.env.QBO_WEBHOOK_VERIFIER ?? "",
    QBO_PRODUCTION_CLIENT_ID: this.env.QBO_PRODUCTION_CLIENT_ID ?? "",
    QBO_PRODUCTION_CLIENT_SECRET: this.env.QBO_PRODUCTION_CLIENT_SECRET ?? "",
    QBO_PRODUCTION_REDIRECT_URI: this.env.QBO_PRODUCTION_REDIRECT_URI ?? "",
    WORKOS_API_KEY: this.env.WORKOS_API_KEY ?? "",
    XERO_CLIENT_ID: this.env.XERO_CLIENT_ID ?? "",
    XERO_CLIENT_SECRET: this.env.XERO_CLIENT_SECRET ?? "",
    XERO_REDIRECT_URI: this.env.XERO_REDIRECT_URI ?? "",
    XERO_WEBHOOK_KEY: this.env.XERO_WEBHOOK_KEY ?? "",
    PUBLIC_APP_URL: this.env.PUBLIC_APP_URL ?? "",
    EMAIL_INTAKE_ADDRESS: this.env.EMAIL_INTAKE_ADDRESS ?? "",
    EMAIL_INTAKE_KEY: this.env.EMAIL_INTAKE_KEY ?? "",
    WORKOS_CLIENT_ID: this.env.WORKOS_CLIENT_ID ?? "",
    WORKOS_ORGANIZATION_ID: this.env.WORKOS_ORGANIZATION_ID ?? "",
    WORKOS_ISSUER: this.env.WORKOS_ISSUER ?? "",
    STAFF_EMAIL_DOMAIN: this.env.STAFF_EMAIL_DOMAIN ?? "",
    QBO_SANDBOX_REALM_ID: this.env.QBO_SANDBOX_REALM_ID ?? "",
    QBO_SANDBOX_ACCESS_TOKEN: this.env.QBO_SANDBOX_ACCESS_TOKEN ?? "",
    QBO_SANDBOX_ENTITY: this.env.QBO_SANDBOX_ENTITY ?? "",
    QBO_SANDBOX_PREPAID_ACCOUNT_ID:
      this.env.QBO_SANDBOX_PREPAID_ACCOUNT_ID ?? "",
    QBO_SANDBOX_EXPENSE_ACCOUNT_ID:
      this.env.QBO_SANDBOX_EXPENSE_ACCOUNT_ID ?? "",
    WORKOS_AUTHKIT_DOMAIN: this.env.WORKOS_AUTHKIT_DOMAIN ?? "",
    MCP_RESOURCE_URL: this.env.MCP_RESOURCE_URL ?? "",
    MCP_ALLOWED_ORIGINS: this.env.MCP_ALLOWED_ORIGINS ?? "",
    PRODUCTION_POSTING_KINDS: this.env.PRODUCTION_POSTING_KINDS ?? "",
    XERO_POSTING_KINDS: this.env.XERO_POSTING_KINDS ?? "",
    AUTH_SESSION: this.env.AUTH_SESSION ?? "",
  };
  // A sleeping API wakes in seconds, so it stays up through a working day: pages load in under 400 ms only from a
  // warm container with open database connections (docs/performance.md).
  sleepAfter = "24h";
}

// Containers reach private Worker bindings through a fixed virtual host.
CapabilityAPI.outboundByHost = {
  "context-machine.internal": async (request, env) => (env as Env).CONTEXT_MACHINE?.fetch(request) ?? new Response(null, { status: 503 }),
};

interface Env {
  TEXT_SCANNER_URL?: string;
  UPLOAD_SCAN_HOST?: string;
  UPLOAD_SCAN_KEY?: string;
  FRONTEND_ORIGIN?: string;
  DATABASE_URL?: string;
  OPENAI_API_KEY?: string;
  OPENAI_MODEL?: string;
  OPENAI_REASONING_EFFORT?: string;
  LLM_REASONING_EFFORTS?: string;
  LLM_MODELS?: string;
  LLM_PROVIDER?: string;
  LLM_ALLOW_UNCAPPED_OUTPUT_TOKENS?: string;
  CONTEXT_MACHINE_PROFILES?: string;
  CODEX_MODEL?: string;
  CLAUDE_MODEL?: string;
  CODEX_REASONING_EFFORT?: string;
  CLAUDE_REASONING_EFFORT?: string;

  CONTEXT_MACHINE_TOKEN?: string;
  CONTEXT_MACHINE_MODEL?: string;
  CONTEXT_MACHINE?: Fetcher;
  SECRETS_KEY?: string;
  PUBLIC_APP_URL?: string;
  EMAIL_INTAKE_ADDRESS?: string;
  EMAIL_INTAKE_KEY?: string;
  QBO_CLIENT_ID?: string;
  QBO_CLIENT_SECRET?: string;
  QBO_REDIRECT_URI?: string;
  QBO_WEBHOOK_VERIFIER?: string;
  QBO_PRODUCTION_CLIENT_ID?: string;
  QBO_PRODUCTION_CLIENT_SECRET?: string;
  QBO_PRODUCTION_REDIRECT_URI?: string;
  WORKOS_API_KEY?: string;
  XERO_CLIENT_ID?: string;
  XERO_CLIENT_SECRET?: string;
  XERO_REDIRECT_URI?: string;
  XERO_WEBHOOK_KEY?: string;
  WORKOS_CLIENT_ID?: string;
  WORKOS_ORGANIZATION_ID?: string;
  WORKOS_ISSUER?: string;
  STAFF_EMAIL_DOMAIN?: string;
  QBO_SANDBOX_REALM_ID?: string;
  QBO_SANDBOX_ACCESS_TOKEN?: string;
  QBO_SANDBOX_ENTITY?: string;
  QBO_SANDBOX_PREPAID_ACCOUNT_ID?: string;
  QBO_SANDBOX_EXPENSE_ACCOUNT_ID?: string;
  WORKOS_AUTHKIT_DOMAIN?: string;
  MCP_RESOURCE_URL?: string;
  MCP_ALLOWED_ORIGINS?: string;
  PRODUCTION_POSTING_KINDS?: string;
  XERO_POSTING_KINDS?: string;
  AUTH_SESSION?: string;
  // Notes from the public contact page are forwarded here (docs/marketing-site.md); unset, they are only kept.
  CONTACT_FORWARD_ADDRESS?: string;
  CONTACT_EMAIL?: SendEmail;
  API: DurableObjectNamespace<CapabilityAPI>;
  ASSETS: Fetcher;
}

// A note the API kept from the contact page goes on to our inbox. The visitor is answered at once and the email
// follows; a note that cannot be forwarded is still kept, and the failure is logged without its contents.
function forwardContact(response: Response, env: Env, ctx: ExecutionContext) {
  const to = env.CONTACT_FORWARD_ADDRESS;
  let host = "";
  try {
    host = new URL(env.PUBLIC_APP_URL ?? "").hostname;
  } catch {
    return;
  }
  if (!env.CONTACT_EMAIL || !to || !host) return;
  const binding = env.CONTACT_EMAIL, from = `website@${host}`;
  ctx.waitUntil(
    response.clone().json()
      .then(noteToForward)
      .then(async (note) => {
        if (note) await binding.send(new EmailMessage(from, to, contactMessage(note, from, to)));
      })
      .catch((error: unknown) => console.error("Contact note could not be forwarded:", error instanceof Error ? error.message : "unknown error")),
  );
}

export default {
  async email(message: ForwardableEmailMessage, env: Env): Promise<void> {
    const [base, domain] = (env.EMAIL_INTAKE_ADDRESS ?? "").toLowerCase().split("@");
    const recipient = message.to.toLowerCase();
    if (!base || !domain || !recipient.startsWith(`${base}+`) || !recipient.endsWith(`@${domain}`) || message.rawSize < 1 || message.rawSize > 6 * 1024 * 1024 || !env.EMAIL_INTAKE_KEY || env.EMAIL_INTAKE_KEY.length < 32) {
      message.setReject("Evidence intake address or message size is invalid");
      return;
    }
    const reply = recipient.startsWith(`${base}+reply-`);
    const response = await env.API.getByName("reference-api").fetch(new Request(reply ? "http://api/internal/email-reply" : "http://api/internal/forwarded-email", {
      method: "POST",
      headers: { "X-Close-Email-Key": env.EMAIL_INTAKE_KEY, "X-Close-Envelope-From": message.from, "X-Close-Envelope-To": message.to, "Content-Type": "message/rfc822" },
      body: await new Response(message.raw).arrayBuffer(),
    }));
    if (response.status === 413 || response.status === 422) {
      message.setReject(reply ? "Reply cannot be accepted; use the secure request link" : "Evidence message cannot be accepted; check the address and supported attachments");
      return;
    }
    if (!response.ok) throw new Error("Durable evidence intake unavailable");
  },
  async fetch(request: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
    const path = new URL(request.url).pathname;
    if (forwardsToAPI(path)) {
      if (needsWorkspaceAuth(path) && (!env.WORKOS_CLIENT_ID || !env.WORKOS_ISSUER)) {
        return new Response(
          "Hosted workspace authentication is not configured",
          { status: 503 },
        );
      }
      // ponytail: one API container; add bounded pooling after load measurement.
      const forwarded = uploadScanRequest(request, env.UPLOAD_SCAN_HOST, env.UPLOAD_SCAN_KEY);
      if (forwarded instanceof Response) return forwarded;
      const response = await env.API.getByName("reference-api").fetch(forwarded);
      if (path === "/v1/contact" && request.method === "POST" && response.status === 201) forwardContact(response, env, ctx);
      return response;
    }
    if (path.startsWith("/internal/")) {
      return new Response("Not found", { status: 404 });
    }
    // The staff admin panel, only for signed-in platform staff, is its own single-page app: its pages load its index
    // page, asked for as /admin/ (see adminAsset).
    if (adminPath(path)) {
      const refused = await staffGate(request, env.AUTH_SESSION, (check) => env.API.getByName("reference-api").fetch(check));
      if (refused) return secureAdmin(refused);
      const asset = adminAsset(path);
      return secureAdmin(await env.ASSETS.fetch(asset === path ? request : new Request(new URL(asset, request.url), request)));
    }
    // The home page comes with its words already in the HTML (apps/frontend/prerender.mjs); index.html stays the empty
    // shell every app route loads.
    if (path === "/") return env.ASSETS.fetch(new Request(new URL("/static-home", request.url), request));
    if (path === "/static-home") return Response.redirect(new URL("/" + new URL(request.url).search, request.url).href, 301);
    return env.ASSETS.fetch(request);
  },
} satisfies ExportedHandler<Env>;
