// The user-approved credential store is reachable only from the container's
// fixed outbound handler, never the Worker's public/service-binding fetch path.
const limit = 32 * 1024;
export function validAuth(raw: string): boolean {
  try {
    const auth = JSON.parse(raw);
    return new TextEncoder().encode(raw).length <= limit && auth.auth_mode === "chatgpt" &&
      ["access_token", "refresh_token", "id_token", "account_id"].every(k => typeof auth.tokens?.[k] === "string" && auth.tokens[k].length > 0);
  } catch { return false; }
}

type StoredAuth = { seed: string; json: string; lease?: string; expires?: number };
export async function credentialRequest(request: Request, token: string, seedJSON: string, storage: DurableObjectStorage): Promise<Response> {
  const reply = (status: number, body = "") => new Response(body, { status, headers: { "Cache-Control": "no-store" } });
  if (token.length < 32 || request.headers.get("Authorization") !== `Bearer ${token}`) return reply(401);
  if (new URL(request.url).pathname !== "/codex" || !["POST", "PUT"].includes(request.method)) return reply(404);
  if (!validAuth(seedJSON)) return reply(503);
  const hash = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(seedJSON));
  const seed = Array.from(new Uint8Array(hash), b => b.toString(16).padStart(2, "0")).join("");
  if (request.headers.get("X-Auth-Seed") !== seed) return reply(409);
  const lease = request.headers.get("X-Auth-Lease") ?? "";
  if (!/^[A-Za-z0-9_-]{20,128}$/.test(lease)) return reply(400);
  let json = "";
  if (request.method === "PUT") {
    const reader = request.body?.getReader();
    if (!reader) return reply(400);
    const chunks: Uint8Array[] = [];
    let length = 0;
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      length += value.length;
      if (length > limit) { await reader.cancel(); return reply(413); }
      chunks.push(value);
    }
    const bytes = new Uint8Array(length);
    let offset = 0;
    for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.length; }
    json = new TextDecoder().decode(bytes);
    if (!validAuth(json) || JSON.parse(json).tokens.account_id !== JSON.parse(seedJSON).tokens.account_id) return reply(400);
  }
  return storage.transaction(async tx => {
    let state = await tx.get<StoredAuth>("codex-auth");
    if (request.method === "POST") {
      // A changed seed is an explicit new login. Never overwrite refreshed auth
      // during routine restarts/deployments with the original seed.
      if (!state || state.seed !== seed) state = { seed, json: seedJSON };
      if (state.lease) return reply((state.expires ?? 0) > Date.now() ? 429 : 503);
      state.lease = lease;
      state.expires = Date.now() + 180_000;
      await tx.put("codex-auth", state);
      return reply(200, state.json);
    }
    if (!state || state.seed !== seed || state.lease !== lease || (state.expires ?? 0) <= Date.now()) return reply(409);
    state.json = json;
    if (request.headers.get("X-Auth-Release") === "true") { delete state.lease; delete state.expires; }
    await tx.put("codex-auth", state);
    return reply(200);
  });
}
