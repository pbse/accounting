import { QueryClient, queryOptions, type QueryKey } from "@tanstack/react-query";
import { createContext, type RouterContextProvider } from "react-router";
import { workspaceRequest } from "../features/closes/workspace";

// The workspace's data layer (docs/server-rendering.md). Every read of the API is a query keyed by its path, kept in
// one TanStack Query cache. A route's loader prefetches the queries its page needs, so the page renders with its
// data; components read the same queries with useQuery. On the server the edge renders the page from that cache and
// sends the cache with it, and the browser starts from it.

// read is how a query reaches the API: in the browser, workspaceRequest (the cookie session or the local token); on
// the server, the edge's binding to the API with the signed-in person's cookie. It resolves with the parsed answer or
// rejects with a RequestError, as workspaceRequest does.
export type Read = (path: string) => Promise<unknown>;

type AppData = { queryClient: QueryClient; read: Read };

// Set per request on the server and once in the browser; loaders take the query client and read from it.
export const appData = createContext<AppData>();

export const browserRead: Read = (path) => workspaceRequest(path);

export function newQueryClient() {
  return new QueryClient({
    defaultOptions: {
      // A page's data, once read, is shown without asking again for half a minute; a change made through the app
      // invalidates what it changed. Failures are not retried behind the person's back.
      queries: { staleTime: 30_000, retry: false, refetchOnWindowFocus: false },
    },
  });
}

// Active decisions poll within the five-second visibility target, leaving time for the read and render.
export const decisionRefreshMs = 3000;

export const workspaceKey = (path: string): QueryKey => ["workspace", path];

// workspaceQuery is the closes API's GET path (path is relative to /v1/closes, as workspaceRequest takes it).
export function workspaceQuery<T>(path: string, read: Read = browserRead) {
  return queryOptions({ queryKey: workspaceKey(path), queryFn: () => read(path) as Promise<T> });
}

// prefetch fills the cache with a page's reads before it renders. A read that fails is left for the page, which
// shows its own error and its own way to try again; a loader never fails a whole page for one section.
export async function prefetch(context: Readonly<RouterContextProvider>, ...paths: string[]) {
  const { queryClient, read } = context.get(appData);
  await Promise.all(paths.map((path) => queryClient.prefetchQuery(workspaceQuery(path, read))));
}

// workPath is the Work list's read (features/closes/Work.tsx): a close's or the firm's, by view, client, page and
// linked job. Its loader and the list build it the same way, so the prefetched read is the one the list asks for.
export const workPath = (closeId: string | undefined, view: string, client = "", offset = 0, job = "") =>
  `${closeId ? `/${closeId}/work` : "/work"}?view=${view}&client=${encodeURIComponent(client)}&offset=${offset}${job ? `&jobs=${encodeURIComponent(job)}` : ""}`;

// prefetchLater starts reads that come from a slow outside provider (WorkOS, Google) without waiting for them: in the
// browser they start as the page opens, so the page never waits on the provider; the server renders without them and
// their sections load on their own.
export function prefetchLater(context: Readonly<RouterContextProvider>, ...paths: string[]) {
  if (typeof document === "undefined") return;
  const { queryClient, read } = context.get(appData);
  for (const path of paths) void queryClient.prefetchQuery(workspaceQuery(path, read));
}

// The Preparation page's views (features/closes/Work.tsx); its loader opens the one a link names, or Active.
export const workViews: [string, string][] = [
  ["active", "Active"],
  ["mine", "Mine"],
  ["needs_confirmation", "Decisions"],
  ["blocked", "Blockers"],
  ["running", "Running"],
  ["waiting_client", "Client requests"],
  ["completed", "Prepared"],
  ["all", "All"],
];

// effortPath is Measure's effort read for a month, or the latest months.
export const effortPath = (period: string) => `/effort${period ? `?period=${period}` : ""}`;
