import { useCallback, useEffect, useState } from "react";
import { authorization } from "./auth";

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

export async function get<T>(path: string): Promise<T> {
  const response = await fetch(path, { headers: await authorization() });
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

// useResource loads path and reloads it on demand; a newer load always wins.
export function useResource<T>(path: string) {
  const [state, setState] = useState<{ data?: T; error?: string; loading: boolean }>({ loading: true });
  const [version, setVersion] = useState(0);
  useEffect(() => {
    let current = true;
    setState((previous) => ({ ...previous, loading: true }));
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
