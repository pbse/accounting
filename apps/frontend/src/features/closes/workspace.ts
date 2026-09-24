import { useSyncExternalStore } from "react";
import { authenticationHeaders, authKitClientId } from "../auth/Auth";
import { apiBaseUrl } from "../../lib/api";
// Kept in memory only. Refreshing requires unlocking again; never bake a token into frontend config.
let token = "";
const listeners = new Set<() => void>();
export function unlock(value: string) {
  token = value;
  listeners.forEach((listener) => listener());
}
export function useUnlocked() {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    () => token !== "",
  );
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
