import { StrictMode } from "react";
import { createRoot, hydrateRoot } from "react-dom/client";
import { hydrate, type DehydratedState } from "@tanstack/react-query";
import { createBrowserRouter, matchRoutes, RouterContextProvider, RouterProvider, type HydrationState } from "react-router";
import { AppProviders, queryStateId } from "./app";
import { appData, browserRead, newQueryClient } from "./data/queries";
import { routes } from "./routes";
import { onTokenChange, onWorkspaceWrite } from "./features/closes/workspace";
import "./styles.css";
import "@close/ui/theme/tokens.css";
import "@close/ui/theme/shell.css";
import "./theme/workspace.css";

declare global {
  interface Window {
    __staticRouterHydrationData?: HydrationState;
  }
}

const queryClient = newQueryClient();
// A different local development token may see different data: nothing read with the last one is kept.
onTokenChange(() => queryClient.clear());
// Any change made through the app marks every workspace read stale (data/queries.ts).
onWorkspaceWrite(() => void queryClient.invalidateQueries({ queryKey: ["workspace"] }));
// A page the edge rendered (docs/server-rendering.md) carries its query cache and its loaders' results; the browser
// starts from both instead of asking again.
const state = document.getElementById(queryStateId)?.textContent;
const rendered = !!state && !!window.__staticRouterHydrationData;
if (state) {
  try {
    hydrate(queryClient, JSON.parse(state) as DehydratedState);
  } catch {
    // An unreadable cache only means the page asks for its data again.
  }
}

async function start() {
  if (rendered) {
    // The server rendered the page with its code; the browser loads the same code before it hydrates, or the first
    // render would differ from the HTML.
    const lazy = matchRoutes(routes, window.location)?.filter((match) => match.route.lazy) ?? [];
    await Promise.all(
      lazy.map(async ({ route }) => {
        const resolved = await (route.lazy as () => Promise<object>)();
        Object.assign(route, { ...resolved, lazy: undefined });
      }),
    );
  }
  const router = createBrowserRouter(routes, {
    hydrationData: rendered ? window.__staticRouterHydrationData : undefined,
    getContext() {
      const context = new RouterContextProvider();
      context.set(appData, { queryClient, read: browserRead });
      return context;
    },
  });
  const tree = (
    <StrictMode>
      <AppProviders queryClient={queryClient} path={window.location.pathname}>
        <RouterProvider router={router} />
      </AppProviders>
    </StrictMode>
  );
  const root = document.getElementById("root")!;
  if (rendered) hydrateRoot(root, tree);
  else createRoot(root).render(tree);
}

void start();
