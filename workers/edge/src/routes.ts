// Paths the API container serves; everything else is the workspace's static assets, and /internal/* stays private.
export function forwardsToAPI(path: string): boolean {
  return path === "/healthz" || path === "/healthz/worker" || path === "/readyz" || path === "/mcp" || path.startsWith("/v1/") ||
    path === "/.well-known/oauth-protected-resource" || path.startsWith("/.well-known/oauth-protected-resource/");
}

// What the API answers without a workspace sign-in, so it stays open while the sign-in settings are missing: health
// checks, the reference workpaper and the public contact form.
export function needsWorkspaceAuth(path: string): boolean {
  return path !== "/healthz" && path !== "/v1/reference/prepaid" && path !== "/v1/contact";
}

// A page of the staff admin panel (served its own index.html); its files (with an extension) are not.
export function adminPage(path: string): boolean {
  return /^\/admin(\/[^.]*)?$/.test(path);
}

// The asset an /admin request is served from. A page gets the console's index page, asked for by its directory:
// Cloudflare's static assets answer a request for .../index.html with a 307 to the directory, which the Worker would
// turn back into a request for index.html, sending the browser round in circles. Files are served as asked.
export function adminAsset(path: string): string {
  return adminPage(path) ? "/admin/" : path;
}

// Anything under /admin: the staff console's pages and files.
export function adminPath(path: string): boolean {
  return path === "/admin" || path.startsWith("/admin/");
}

const refuse = (status: number, message: string) => new Response(message, { status, headers: { "Content-Type": "text/plain; charset=utf-8", "Cache-Control": "no-store" } });

// staffGate lets a request for the staff console through (undefined) only when the API accepts its staff session:
// GET /v1/admin/me, asked with the request's cookies alone, needs a staff session for a verified address at
// STAFF_EMAIL_DOMAIN. Signed out, a page goes to staff sign-in and returns afterwards; a file is refused; nothing of the
// console is sent. Only AUTH_SESSION=server keeps a session the edge can see, so any other mode serves nothing.
export async function staffGate(request: Request, authSession: string | undefined, check: (request: Request) => Promise<Response>): Promise<Response | undefined> {
  if (authSession !== "server") return refuse(503, "The staff console is served only with AUTH_SESSION=server, so sign-in can be checked before it loads.");
  const url = new URL(request.url);
  let verdict: Response;
  try {
    verdict = await check(new Request(new URL("/v1/admin/me", url), { headers: { Cookie: request.headers.get("Cookie") ?? "" } }));
  } catch {
    return refuse(503, "Staff sign-in could not be checked. Try again shortly.");
  }
  if (verdict.ok) return undefined;
  if (verdict.status === 401 && adminPage(url.pathname) && (request.method === "GET" || request.method === "HEAD")) {
    const login = new URL("/v1/auth/login", url);
    login.search = new URLSearchParams({ staff: "1", return: url.pathname + url.search }).toString();
    return new Response(null, { status: 302, headers: { Location: login.toString(), "Cache-Control": "no-store" } });
  }
  if (verdict.status === 401) return refuse(401, "Sign in with your staff Google account.");
  if (verdict.status === 403) return refuse(403, "Your staff role does not include the staff console.");
  return refuse(503, "Staff sign-in could not be checked. Try again shortly.");
}

// The staff console runs only its own scripts and is never framed (so never clickjacked) or sniffed; what it serves
// is for the signed-in staff member alone, so no shared cache keeps it.
export function secureAdmin(response: Response): Response {
  const secured = new Response(response.body, response);
  if (response.ok) secured.headers.set("Cache-Control", "private, no-cache");
  secured.headers.set("Content-Security-Policy", "script-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'");
  secured.headers.set("X-Frame-Options", "DENY");
  secured.headers.set("X-Content-Type-Options", "nosniff");
  secured.headers.set("Referrer-Policy", "no-referrer");
  return secured;
}
