import { authenticationHeaders, authKitClientId } from "../auth/Auth";
import { apiBaseUrl } from "../../lib/api";
// Kept in memory only. Refreshing requires unlocking again; never bake a token into frontend config.
let token = "";
export function unlock(value: string) {
  token = value;
}
export async function workspaceRequest(
  path: string,
  body?: unknown,
  download = false,
) {
  const response = await fetch(`${apiBaseUrl}/v1/closes${path}`, {
    method: body === undefined ? "GET" : "POST",
    headers: {
      ...(authKitClientId
        ? await authenticationHeaders()
        : { Authorization: `Bearer ${token}` }),
      ...(body === undefined || body instanceof FormData
        ? {}
        : { "Content-Type": "application/json" }),
    },
    ...(body === undefined
      ? {}
      : { body: body instanceof FormData ? body : JSON.stringify(body) }),
  });
  if (download && response.ok) return response.blob();
  const result = await response.json();
  if (!response.ok)
    throw new Error(result.message ?? "The workspace is unavailable.");
  return result;
}
