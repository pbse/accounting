// Paths the API container serves; everything else is the workspace's static assets, and /internal/* stays private.
export function forwardsToAPI(path: string): boolean {
  return path === "/healthz" || path === "/readyz" || path === "/mcp" || path.startsWith("/v1/") ||
    path === "/.well-known/oauth-protected-resource" || path.startsWith("/.well-known/oauth-protected-resource/");
}

// A page of the staff admin panel (served its own index.html); its files (with an extension) are not.
export function adminPage(path: string): boolean {
  return /^\/admin(\/[^.]*)?$/.test(path);
}

// The staff console runs only its own scripts and is never framed (so never clickjacked) or sniffed.
export function secureAdmin(response: Response): Response {
  const secured = new Response(response.body, response);
  secured.headers.set("Content-Security-Policy", "script-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'");
  secured.headers.set("X-Frame-Options", "DENY");
  secured.headers.set("X-Content-Type-Options", "nosniff");
  secured.headers.set("Referrer-Policy", "no-referrer");
  return secured;
}
