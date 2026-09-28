import { useCallback, useEffect, useState } from "react";
import { authorization } from "./auth";

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

export async function get<T>(path: string, headers: Record<string, string> = {}): Promise<T> {
  const response = await fetch(path, { headers: { ...(await authorization()), ...headers } });
  if (!response.ok) {
    let message = response.statusText;
    try {
      message = (await response.json()).message ?? message;
    } catch {
      // keep the status text
    }
    throw new ApiError(response.status, message);
  }
  return response.json();
}

export async function post<T>(path: string, body: unknown): Promise<T> {
  const response = await fetch(path, {
    method: "POST",
    headers: { ...(await authorization()), "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!response.ok) {
    let message = response.statusText;
    try {
      message = (await response.json()).message ?? message;
    } catch {
      // keep the status text
    }
    throw new ApiError(response.status, message);
  }
  return response.json();
}

// useResource loads path and reloads it on demand; a newer load always wins. Reloading the same path
// keeps its data on screen until the new answer; a different path, or a failed load, never shows
// another path's data. With a reason, it asks for client content revealed (an audited read); a
// reveal counts as a different path, so hiding it clears the content at once. A null path loads nothing.
export function useResource<T>(path: string | null, revealReason?: string) {
  const key = path === null ? "" : revealReason === undefined ? path : `${path} revealed for ${revealReason}`;
  const [state, setState] = useState<{ key: string; data?: T; error?: string; loading: boolean }>({ key, loading: true });
  const [version, setVersion] = useState(0);
  useEffect(() => {
    let current = true;
    if (path === null) {
      setState({ key, loading: false });
      return;
    }
    setState((previous) => (previous.key === key ? { ...previous, loading: true } : { key, loading: true }));
    const request = revealReason === undefined
      ? get<T>(path)
      : get<T>(`${path}${path.includes("?") ? "&" : "?"}reveal=1`, { "X-Access-Reason": revealReason });
    request.then(
      (data) => current && setState({ key, data, loading: false }),
      (error: Error) => current && setState({ key, error: error.message, loading: false }),
    );
    return () => {
      current = false;
    };
  }, [key, version]); // path and revealReason are in key
  const reload = useCallback(() => setVersion((v) => v + 1), []);
  // What belongs to another path or reveal is never returned, not even for the render before the effect.
  const fresh = state.key === key;
  return { data: fresh ? state.data : undefined, error: fresh ? state.error : undefined, loading: fresh ? state.loading : true, reload };
}

export function when(value?: string | null): string {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}
