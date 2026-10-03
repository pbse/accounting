import { useEffect, useState, useSyncExternalStore } from "react";
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
// With onDelta, a streamed reply (application/x-ndjson) hands each {"delta"} line to onDelta and resolves with its last
// line, which must carry the conversation; any other ending is an error, so provisional text is never kept.
export async function workspaceRequest(
  path: string,
  body?: unknown,
  download = false,
  onDelta?: (delta: string) => void,
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
  if (onDelta && response.ok && response.body && response.headers.get("Content-Type")?.includes("application/x-ndjson")) {
    const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
    let buffered = "";
    let last: { messages?: unknown; message?: string } | undefined;
    for (;;) {
      const { value, done } = await reader.read();
      buffered += value ?? "";
      const lines = buffered.split("\n");
      buffered = done ? "" : (lines.pop() ?? "");
      for (const line of lines.filter((l) => l.trim())) {
        const item = JSON.parse(line);
        if (typeof item.delta === "string") onDelta(item.delta);
        else last = item;
      }
      if (done) break;
    }
    if (!last?.messages) throw new Error(last?.message ?? "The answer was interrupted. Your conversation shows what was saved.");
    return last;
  }
  const result = await response.json();
  if (!response.ok)
    throw new Error(result.message ?? "The workspace is unavailable.");
  return result;
}

// Matches the server: "$12.00" in US dollars, "12.00 CAD" otherwise.
export const money = (amount = "", currency = "USD") =>
  currency === "USD" ? `$${amount}` : `${amount} ${currency}`;

// A decimal string as a statement shows it: thousands separators and a true minus, without converting to a float.
export const amount = (value: string, currency = "USD") => {
  const [whole, cents] = value.replace("-", "").split(".");
  return (value.startsWith("-") ? "−" : "") + money(`${whole.replace(/\B(?=(\d{3})+(?!\d))/g, ",")}${cents === undefined ? "" : `.${cents}`}`, currency);
};

// The client's home currency as its connected ledger reports it; USD until known.
export function useCurrency(close: string | null) {
  const [currency, setCurrency] = useState("USD");
  useEffect(() => {
    if (close)
      workspaceRequest(`/${encodeURIComponent(close)}`).then(
        (data) => setCurrency(data.close?.profile?.currency ?? "USD"),
        () => undefined,
      );
  }, [close]);
  return currency;
}
