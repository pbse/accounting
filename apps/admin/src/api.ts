import { useCallback, useEffect, useRef, useState } from "react";
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
// another path's data.
export function useResource<T>(path: string) {
  const [state, setState] = useState<{ data?: T; error?: string; loading: boolean }>({ loading: true });
  const [version, setVersion] = useState(0);
  const loaded = useRef(path);
  useEffect(() => {
    let current = true;
    const samePath = loaded.current === path;
    loaded.current = path;
    setState((previous) => (samePath ? { ...previous, loading: true } : { loading: true }));
    get<T>(path).then(
      (data) => current && setState({ data, loading: false }),
      (error: Error) => current && setState({ error: error.message, loading: false }),
    );
    return () => {
      current = false;
    };
  }, [path, version]);
  const reload = useCallback(() => setVersion((v) => v + 1), []);
  return { ...state, reload };
}

export function when(value?: string | null): string {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}
