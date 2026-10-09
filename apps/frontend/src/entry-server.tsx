import { dehydrate } from "@tanstack/react-query";
import { renderToReadableStream } from "react-dom/server";
import { createStaticHandler, createStaticRouter, RouterContextProvider, StaticRouterProvider } from "react-router";
import { AppProviders } from "./app";
import { appData, newQueryClient, type Read } from "./data/queries";
import { clockKey } from "./data/time";
import { sessionKey, type SessionState } from "./features/auth/Auth";
import { routes } from "./routes";

// The server half of the workspace (docs/server-rendering.md). The edge Worker calls render for a workspace page: the
// matched routes' loaders prefetch their queries through api.read, React renders the page from that cache, and the
// page goes out with the cache and the loaders' results for the browser to hydrate from.
export type ServerApi = {
  // A GET of the closes API (path relative to /v1/closes), as the signed-in person: the parsed answer, or a rejection.
  read: Read;
  // Who is signed in, as GET /v1/auth/session says.
  session: () => Promise<SessionState>;
};

export type Rendered =
  // The page, its status, and the query cache to embed (already safe inside a script element).
  | { kind: "page"; status: number; html: string; queryState: string }
  // A loader answered with a response of its own, such as a redirect.
  | { kind: "response"; response: Response }
  // Nobody is signed in (or sign-in is unavailable): the browser opens the page and signs in.
  | { kind: "signed-out" };

const handler = createStaticHandler(routes);

// nonce marks the router's inline hydration script for the page's Content-Security-Policy.
export async function render(request: Request, api: ServerApi, nonce?: string): Promise<Rendered> {
  const queryClient = newQueryClient();
  const context = new RouterContextProvider();
  context.set(appData, { queryClient, read: api.read });
  const [session, result] = await Promise.all([api.session(), handler.query(request, { requestContext: context })]);
  if (!session.user) return { kind: "signed-out" };
  if (result instanceof Response) return { kind: "response", response: result };
  queryClient.setQueryData(sessionKey, session);
  // The render's clock, sent with the page so the browser's first render reads the same instant.
  queryClient.setQueryData(clockKey, Date.now());
  // A read that failed is not rendered as failed: the browser asks again and shows that section's own error, so the
  // server leaves it pending, as the browser's first render will.
  queryClient.removeQueries({ predicate: (query) => query.state.status !== "success" });
  const router = createStaticRouter(handler.dataRoutes, result);
  let failure: unknown;
  const stream = await renderToReadableStream(
    <AppProviders queryClient={queryClient} path={new URL(request.url).pathname}>
      <StaticRouterProvider router={router} context={result} nonce={nonce} />
    </AppProviders>,
    // The page is sent whole, once every part has rendered, so nothing is held back behind a fallback; React's few
    // inline scripts carry the page's nonce.
    { nonce, progressiveChunkSize: Number.POSITIVE_INFINITY, onError: (error) => void (failure ??= error) },
  );
  await stream.allReady;
  const html = await new Response(stream).text();
  if (failure) throw failure;
  return { kind: "page", status: result.statusCode, html, queryState: safeJSON(dehydrate(queryClient)) };
}

// JSON that cannot end the script element it sits in, nor start a comment or a CDATA section there.
export function safeJSON(value: unknown) {
  return JSON.stringify(value).replace(/</g, "\\u003c").replace(/>/g, "\\u003e").replace(/&/g, "\\u0026").replace(/\u2028/g, "\\u2028").replace(/\u2029/g, "\\u2029");
}
