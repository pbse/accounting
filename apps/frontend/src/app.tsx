import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { Authentication } from "./features/auth/Auth";

// The providers every render shares, in the browser (main.tsx) and on the server (entry-server.tsx): the query cache,
// then sign-in (the client request page has none).
export function AppProviders({ queryClient, path, children }: { queryClient: QueryClient; path: string; children: ReactNode }) {
  return <QueryClientProvider client={queryClient}>{path === "/request" ? children : <Authentication>{children}</Authentication>}</QueryClientProvider>;
}

// The element that carries a server-rendered page's query cache to the browser.
export const queryStateId = "krith-query";
