import type { Rendered, ServerApi, SessionState } from "./ssr";

// Workspace pages are rendered here, at the edge, with their data (docs/server-rendering.md): the page's routes run
// their loaders against the API as the signed-in person, React renders the page, and the browser hydrates it. Without
// a session cookie, or if anything fails, the browser gets the ordinary app page and renders it itself.

export const workspacePage = (path: string) => path === "/dashboard" || path.startsWith("/dashboard/");

export type WorkspaceDeps = {
  // Server rendering is on (the cookie session, AUTH_SESSION=server).
  enabled: boolean;
  // The workspace's server render.
  render: (request: Request, api: ServerApi, nonce: string) => Promise<Rendered>;
  // The app's own page for a request (the static index.html the browser renders from).
  asset: (request: Request) => Promise<Response>;
  // A GET of the API at path ("/v1/...") with the person's cookie.
  call: (path: string, cookie: string) => Promise<Response>;
  // Reports a failed render, without the page's data.
  log?: (message: string) => void;
};

// serverApi reads the API for a render as the person whose cookie came with the request.
export function serverApi(call: WorkspaceDeps["call"], cookie: string): ServerApi {
  return {
    async read(path) {
      const response = await call(`/v1/closes${path}`, cookie);
      const body = (await response.json().catch(() => ({}))) as { message?: string };
      if (!response.ok) throw Object.assign(new Error(body.message ?? "The workspace is unavailable."), { status: response.status });
      return body;
    },
    async session(): Promise<SessionState> {
      const response = await call("/v1/auth/session", cookie);
      if (response.ok) return { user: ((await response.json()) as { user: SessionState["user"] }).user };
      return response.status === 401 ? { user: null } : { user: null, unavailable: true };
    },
  };
}

function nonce() {
  const bytes = new Uint8Array(18);
  crypto.getRandomValues(bytes);
  return btoa(String.fromCharCode(...bytes));
}

const root = '<div id="root"></div>';

// pageResponse puts the rendered page and its query cache into the app's page. It is the person's own data, so it is
// never cached or framed, and only the app's scripts and the router's hydration script (by its nonce) run.
export function pageResponse(template: string, page: { status: number; html: string; queryState: string }, scriptNonce: string): Response | undefined {
  if (!template.includes(root)) return undefined;
  const html = template.replace(root, `<div id="root">${page.html}</div><script id="krith-query" type="application/json">${page.queryState}</script>`);
  return new Response(html, {
    status: page.status,
    headers: {
      "Content-Type": "text/html; charset=utf-8",
      "Cache-Control": "private, no-store",
      Vary: "Cookie",
      "Content-Security-Policy": `script-src 'self' 'nonce-${scriptNonce}'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'`,
      "X-Frame-Options": "DENY",
      "X-Content-Type-Options": "nosniff",
      "Referrer-Policy": "no-referrer",
    },
  });
}

export async function serveWorkspace(request: Request, deps: WorkspaceDeps): Promise<Response> {
  const cookie = request.headers.get("Cookie") ?? "";
  if (!deps.enabled || request.method !== "GET" || !cookie) return deps.asset(request);
  try {
    const scriptNonce = nonce();
    const [rendered, shell] = await Promise.all([deps.render(request, serverApi(deps.call, cookie), scriptNonce), deps.asset(request)]);
    if (rendered.kind === "response") return rendered.response;
    if (rendered.kind === "signed-out" || !shell.ok) return shell;
    return pageResponse(await shell.text(), rendered, scriptNonce) ?? deps.asset(request);
  } catch (error) {
    deps.log?.(`Server rendering failed for ${new URL(request.url).pathname}: ${error instanceof Error ? error.message : "unknown error"}`);
    return deps.asset(request);
  }
}
