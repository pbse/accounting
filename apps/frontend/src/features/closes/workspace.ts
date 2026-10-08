import { useEffect, useState, useSyncExternalStore } from "react";
import { authenticationHeaders, authKitClientId, serverSession } from "../auth/Auth";
import { apiBaseUrl } from "../../lib/api";
import { RequestError } from "./requestError";
// Kept in memory only. Refreshing requires unlocking again; never bake a token into frontend config.
let token = "";
const listeners = new Set<() => void>();
export function unlock(value: string) {
  token = value;
  reads.clear();
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
// An agent event: a step the agent is making (running, then ok, refused, unavailable, omitted or proposed), the card
// it proposed, the answer's text once it is checked, or an error that ends the attempt.
export type AgentEvent =
  | { type: "step"; id: string; tool: string; title: string; state: string; size?: number; truncated?: boolean }
  | { type: "card"; action: string; target_close_id?: string }
  | { type: "text"; body: string; citations: { label: string; href: string }[] }
  | { type: "error"; message: string };

// With onEvent, a streamed reply (application/x-ndjson) hands each event line to onEvent and resolves with its last
// line, which must carry the conversation; any other ending is an error, so provisional text is never kept.
// A read asked for again while it is still under way shares it: a page's parts often ask for the same data at once.
// A read started ahead of a page (prefetchWorkspace) is also kept for the page's first ask within ten seconds, and
// then forgotten. Any other finished read is forgotten at once, so a refresh always asks the server. Each caller gets
// its own copy; failures are never shared; a change made through this helper, or a new token, forgets every read.
const keptFor = 10_000;
type SharedRead = { value: Promise<unknown>; settled: boolean; prefetched: boolean };
const reads = new Map<string, SharedRead>();

function sharedRead(path: string, prefetch = false): Promise<unknown> {
  const hit = reads.get(path);
  if (hit && (!hit.settled || hit.prefetched)) {
    // A page's ask uses up a prefetched read: finished, it is forgotten now; still under way, as soon as it ends.
    if (!prefetch) {
      if (hit.settled) reads.delete(path);
      hit.prefetched = false;
    }
    return hit.value.then((v) => structuredClone(v));
  }
  const entry: SharedRead = { value: send(path), settled: false, prefetched: prefetch };
  reads.set(path, entry);
  const forget = () => reads.get(path) === entry && reads.delete(path);
  entry.value.then(
    () => {
      entry.settled = true;
      if (entry.prefetched) window.setTimeout(forget, keptFor);
      else forget();
    },
    forget,
  );
  return entry.value.then((v) => structuredClone(v));
}

// prefetchWorkspace starts reads a page is about to make, so they run while its code loads or while the pointer rests
// on its link. Only a cookie session can ask before the page itself has signed in.
export function prefetchWorkspace(...paths: string[]) {
  if (!serverSession) return;
  for (const path of paths) if (!reads.has(path)) sharedRead(path, true).catch(() => undefined);
}

export async function workspaceRequest(
  path: string,
  body?: unknown,
  download = false,
  onEvent?: (event: AgentEvent) => void,
) {
  if (body === undefined && !download && !onEvent) return sharedRead(path) as Promise<any>;
  reads.clear();
  try {
    return await send(path, body, download, onEvent);
  } finally {
    reads.clear();
  }
}

async function send(
  path: string,
  body?: unknown,
  download = false,
  onEvent?: (event: AgentEvent) => void,
): Promise<any> {
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
  if (onEvent && response.ok && response.body && response.headers.get("Content-Type")?.includes("application/x-ndjson")) {
    const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
    let buffered = "";
    let last: { messages?: unknown; message?: string } | undefined;
    let failed = "";
    for (;;) {
      const { value, done } = await reader.read();
      buffered += value ?? "";
      const lines = buffered.split("\n");
      buffered = done ? "" : (lines.pop() ?? "");
      for (const line of lines.filter((l) => l.trim())) {
        const item = JSON.parse(line);
        if (typeof item.type === "string") {
          if (item.type === "error") failed = item.message ?? "The agent could not finish.";
          onEvent(item as AgentEvent);
        } else last = item;
      }
      if (done) break;
    }
    if (failed) throw new Error(failed);
    if (!last?.messages) throw new Error(last?.message ?? "The answer was interrupted. Your conversation shows what was saved.");
    return last;
  }
  const result = await response.json();
  if (!response.ok)
    throw new RequestError(result.message ?? "The workspace is unavailable.", response.status);
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
