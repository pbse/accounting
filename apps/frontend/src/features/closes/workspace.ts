import { useEffect, useState, useSyncExternalStore } from "react";
import { authenticationHeaders, authKitClientId } from "../auth/Auth";
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
// onTokenChange calls back whenever the local development token changes, so cached reads made with another can go.
export function onTokenChange(callback: () => void) {
  listeners.add(callback);
}
export function useUnlocked() {
  return useSyncExternalStore(
    (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    () => token !== "",
    // Nothing is unlocked on the server, nor while the browser hydrates its page.
    () => false,
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
// Each caller gets its own copy; a finished read is forgotten at once, so a refresh always asks the server, and a
// change made through this helper or a new token forgets every read. Pages that read through queries
// (src/data/queries.ts) are cached there instead.
const reads = new Map<string, Promise<unknown>>();

function sharedRead(path: string): Promise<unknown> {
  let value = reads.get(path);
  if (!value) {
    const asked = send(path);
    value = asked;
    reads.set(path, asked);
    const forget = () => reads.get(path) === asked && reads.delete(path);
    asked.then(forget, forget);
  }
  return value.then((v) => structuredClone(v));
}

export async function workspaceRequest(
  path: string,
  body?: unknown,
  download = false,
  onEvent?: (event: AgentEvent) => void,
  // A request the caller may stop (an answer under way): aborting it ends it on the server too, where the handler's
  // context is cancelled, so nothing of a stopped answer is saved.
  signal?: AbortSignal,
) {
  if (body === undefined && !download && !onEvent) return sharedRead(path) as Promise<any>;
  reads.clear();
  try {
    return await send(path, body, download, onEvent, signal);
  } finally {
    reads.clear();
    // A change made anywhere (a month opened, a client saved, the agent acting) may change what any page shows, so
    // every cached read is marked stale: what is on screen reads again, and the rest on its next visit.
    // The 30-second effort heartbeat (Effort.tsx) records time and changes nothing a page shows, so it does not.
    if (body !== undefined && !heartbeat.test(path)) writeListeners.forEach((listener) => listener());
  }
}

const heartbeat = /^\/[^/]+\/effort$/;
const writeListeners = new Set<() => void>();
// onWorkspaceWrite calls back after every change made through workspaceRequest, succeeded or not.
export function onWorkspaceWrite(listener: () => void) {
  writeListeners.add(listener);
}

async function send(
  path: string,
  body?: unknown,
  download = false,
  onEvent?: (event: AgentEvent) => void,
  signal?: AbortSignal,
): Promise<any> {
  const response = await fetch(`${apiBaseUrl}/v1/closes${path}`, {
    method: body === undefined ? "GET" : "POST",
    signal,
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
    // A stream ends with a final document; one cut off before it is an interruption.
    if (!last) throw new Error("The answer was interrupted. Your conversation shows what was saved.");
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
