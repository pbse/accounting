// Paths the API container serves; everything else is the workspace's static assets, and /internal/* stays private.
export function forwardsToAPI(path: string): boolean {
  return path === "/healthz" || path === "/readyz" || path === "/mcp" || path.startsWith("/v1/") ||
    path === "/.well-known/oauth-protected-resource" || path.startsWith("/.well-known/oauth-protected-resource/");
}
