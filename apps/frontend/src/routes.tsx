import { attentionPath } from "./features/closes/Attention";
import type { ComponentType, ReactNode } from "react";
import { createRoutesFromElements, Navigate, redirect, Route, useSearchParams, type LoaderFunctionArgs, type RouteObject } from "react-router";
import { serverSession } from "./features/auth/Auth";
import { Login } from "./features/auth/Login";
import { EffortTracker } from "./features/closes/Effort";
import { WorkspaceGate } from "./features/dashboard/Access";
import { marketingRoutes } from "./features/marketing/routes";
import { cashPlanQuery } from "./data/cashPlan";
import { clientPath } from "./data/clients";
import { closeTab, type CloseTab } from "./data/closeTabs";
import { appData, effortPath, prefetch, prefetchLater, workPath, workspaceKey, workViews } from "./data/queries";

// The app's routes, as React Router data routes: the browser runs them (main.tsx) and so does the edge, which renders a
// workspace page on the server with its data (entry-server.tsx, docs/server-rendering.md). A page's code loads with
// `lazy`, in parallel with its loader; a loader prefetches the queries its page reads, so the page renders with them.

function Gated({ children }: { children: ReactNode }) {
  return <WorkspaceGate>{children}</WorkspaceGate>;
}
function CloseScoped({ children }: { children: ReactNode }) {
  const [params] = useSearchParams();
  const close = params.get("close");
  return (
    <Gated>
      {close && <EffortTracker closeId={close} activity="prepare" />}
      {children}
    </Gated>
  );
}

type Module<P> = { default: ComponentType<P> };
// page turns a module into a route's lazy Component, inside the workspace gate (or another wrapper) with fixed props.
function page<P extends object>(load: () => Promise<Module<P>>, props?: P, wrap: (node: ReactNode) => ReactNode = (node) => <Gated>{node}</Gated>) {
  return async () => {
    const { default: Page } = await load();
    return { Component: () => wrap(<Page {...(props as P)} />) };
  };
}
const closeScoped = (node: ReactNode) => <CloseScoped>{node}</CloseScoped>;
const bare = (node: ReactNode) => node;

// Loaders prefetch only with the cookie session, which a request carries on the server as in the browser. The local
// development token and the WorkOS browser session exist only once the page is open, so there each page reads on its
// own as it renders.
const prefetching = serverSession;

// Every workspace page shows the layout: whether the person is an executive (the layout shows nothing before it
// knows), whether the firm has the worked example, the unread count, and the person's settings (their time zone sets
// how dates read).
// An executive is sent to their own pages from the server, before anything of the firm's workspace renders. A page's
// own reads run alongside this one rather than after it, so a firm member's page waits on no extra round trip; an
// executive's refused reads cost them nothing either, since they run in parallel.
async function workspaceLoader({ context, request }: LoaderFunctionArgs) {
  if (!prefetching) return null;
  await prefetch(context, "/executive", "/examples", attentionPath, "/me");
  const executive = context.get(appData).queryClient.getQueryData<{ executive?: boolean }>(workspaceKey("/executive"))?.executive;
  if (executive && !new URL(request.url).pathname.startsWith("/dashboard/executive")) throw redirect("/dashboard/executive");
  return null;
}

// Home reads its counts and every section under them; the filters start empty.
async function homeLoader({ context }: LoaderFunctionArgs) {
  if (prefetching) {
    // The name in the greeting comes from the sign-in provider: it never holds the page, which greets without it.
    prefetchLater(context, "/me/profile");
    await prefetch(context, "/home", "/portfolio", "/setup", "/goals?client=");
  }
  return null;
}

// Closes reads what its sections show (the counts and decisions in their snapshot, the review queue, the dependency
// lists, the daily exceptions, the delegated closes), so the page arrives with its data.
async function closesLoader({ context }: LoaderFunctionArgs) {
  if (prefetching) await prefetch(context, "/home", "/review-queue?client=", "/dependencies?client=", "/daily?client=", "/goals?client=");
  return null;
}

// Clients reads the recent closes and the roster, and a search's matches.
async function clientsLoader({ context, request }: LoaderFunctionArgs) {
  if (!prefetching) return null;
  const query = new URL(request.url).searchParams.get("q") ?? "";
  await prefetch(context, "", "/roster", ...(query ? [`?q=${encodeURIComponent(query)}`] : []));
  return null;
}

// A client's page reads the client, in one request.
async function clientLoader({ context, params }: LoaderFunctionArgs) {
  if (prefetching && params.client) await prefetch(context, clientPath(params.client));
  return null;
}

// A close reads itself and its drafts; its Overview also reads the agent's goal (with the plan's checks), where the close
// stands, the preparation coordinator and the work from its evidence. Other tabs read as they render.
async function closeLoader({ context, request, params }: LoaderFunctionArgs) {
  if (!prefetching || !params.id) return null;
  const url = new URL(request.url);
  const id = params.id;
  const tab = closeTab(url.searchParams.get("tab"));
  const reads: Partial<Record<CloseTab, string[]>> = {
    overview: [`/${id}/brief`, `/${id}/goal?verify=1`, `/${id}/prepare-close`, `/${id}/coverage`, workPath(id, "all", "", 0, url.searchParams.get("job") ?? "")],
    books: [`/${id}/quickbooks`, `/${id}/quickbooks/entities`, `/${id}/xero`, `/${id}/tie-out`, `/${id}/quickbooks/exceptions`, `/${id}/flux`, `/${id}/coverage`, `/${id}/aging`, `/${id}/open-items`],
    evidence: [`/${id}/evidence?checks=1`, `/${id}/preparations`, `/${id}/jobs`, `/${id}/stripe`],
    requests: [`/${id}/requests`, `/${id}/email-replies`],
    reports: [`/${id}/management-pack`, `/${id}/management-pack/publications`, `/${id}/budget`],
    signoff: [`/${id}/takeover-setup`, `/${id}/opening-baseline`, `/${id}/readiness`, `/${id}/effort`],
    settings: [`/${id}/memory`, `/${id}/replay`],
  };
  // The cash plan's three reads are one view (data/cashPlan.ts). The brief, the page's sentence and its one action,
  // is Overview's to wait for; another tab starts it without waiting, and the sentence names the close until it comes.
  const { queryClient, read } = context.get(appData);
  if (tab !== "overview") prefetchLater(context, `/${id}/brief`);
  await Promise.all([
    prefetch(context, `/${encodeURIComponent(id)}`, ...(reads[tab] ?? [])),
    tab === "cash" ? queryClient.prefetchQuery(cashPlanQuery(id, "", false, read)) : null,
  ]);
  return null;
}

// Preparation opens the view a link names, or Active.
async function preparationLoader({ context, request }: LoaderFunctionArgs) {
  if (!prefetching) return null;
  const asked = new URL(request.url).searchParams.get("view") ?? "";
  await prefetch(context, workPath(undefined, workViews.some(([key]) => key === asked) ? asked : "active"));
  return null;
}

// Measure reads the month's effort (or the latest months') and the track record.
async function measureLoader({ context, request }: LoaderFunctionArgs) {
  if (!prefetching) return null;
  await prefetch(context, effortPath(new URL(request.url).searchParams.get("period") ?? ""), "/track-record");
  return null;
}

// A workpaper reads its close and the close's latest drafts, the workpaper among them; its documents load in the
// browser, as files.
async function workpaperLoader({ context, params }: LoaderFunctionArgs) {
  if (!prefetching || !params.id || !params.paperId) return null;
  const paper = `/${params.id}/workpapers/${params.paperId}`;
  const closePath = `/${encodeURIComponent(params.id)}`;
  await prefetch(context, closePath, `${paper}/reviews`, `${paper}/changes`, `${paper}/since-review`, `${paper}/comments`, `${paper}/standing-approval`);
  // A draft older than the close's latest hundred is read by itself, only then.
  const listed = context.get(appData).queryClient.getQueryData<{ close?: { id: string }; workpapers?: { id: string }[] }>(workspaceKey(closePath));
  if (listed?.close && !listed.workpapers?.some((w) => w.id === params.paperId)) await prefetch(context, `/${listed.close.id}/workpapers/${params.paperId}?record=1`);
  return null;
}

// Settings reads what its open section shows. Sections not listed read as they render.
const settingsReads: Record<string, string[]> = {
  connections: ["/agent-access"],
  firm: ["/email"],
  people: ["/members/people", "/members/roles", "/members/invitations", "/security/clients", "/access", "/members"],
  clients: ["/roster"],
  security: ["/security"],
};
const settingsLater: Record<string, string[]> = { profile: ["/me/profile"], connections: ["/me/drive"] };
async function settingsLoader({ context, request }: LoaderFunctionArgs) {
  if (!prefetching) return null;
  const tab = new URL(request.url).searchParams.get("tab") ?? "profile";
  // What the sign-in provider and Google hold loads on its own, so a slow provider never holds the page.
  prefetchLater(context, ...(settingsLater[tab] ?? []));
  await prefetch(context, "/organization/policy", "/me", ...(settingsReads[tab] ?? []));
  return null;
}

function Opening() {
  return (
    <main>
      <p role="status">Opening your workspace…</p>
    </main>
  );
}

function Failed() {
  return (
    <main>
      <p role="alert">This page could not open. Reload to try again.</p>
    </main>
  );
}

export const routes: RouteObject[] = createRoutesFromElements(
  <Route HydrateFallback={Opening} ErrorBoundary={Failed}>
    <Route path="request" lazy={page(() => import("./features/closes/ClientRequest"), {}, bare)} />
    {marketingRoutes(<Route path="login" element={<Login />} />)}
    <Route path="dashboard" id="workspace" loader={workspaceLoader} lazy={page(() => import("./features/dashboard/Layout"), {}, bare)}>
      <Route index loader={homeLoader} lazy={page(() => import("./features/dashboard/Dashboard"), { view: "work" as const })} />
      <Route path="close" lazy={page(() => import("./features/dashboard/Dashboard"), { view: "close" as const })} />
      <Route path="review" lazy={page(() => import("./features/dashboard/Dashboard"), { view: "review" as const })} />
      <Route path="clients" loader={clientsLoader} lazy={page(() => import("./features/closes/ClosesList"))} />
      <Route path="clients/:client" loader={clientLoader} lazy={page(() => import("./features/clients/ClientPage"))} />
      <Route path="closes" loader={closesLoader} lazy={page(() => import("./features/closes/Closes"))} />
      <Route path="closes/:id" loader={closeLoader} lazy={page(() => import("./features/closes/ClientClose"))} />
      <Route path="closes/:id/workpapers/:paperId" loader={workpaperLoader} lazy={page(() => import("./features/closes/WorkpaperView"))} />
      <Route path="firm" element={<Navigate to="/dashboard/settings?tab=firm" replace />} />
      <Route path="attention" element={<Navigate to="/dashboard" replace />} />
      <Route path="work" loader={preparationLoader} lazy={page(async () => ({ default: (await import("./features/closes/Work")).Work }))} />
      <Route path="security" element={<Navigate to="/dashboard/settings?tab=security" replace />} />
      <Route path="settings" loader={settingsLoader} lazy={page(() => import("./features/settings/Settings"))} />
      <Route path="measure" loader={measureLoader} lazy={page(() => import("./features/closes/Measure"))} />
      <Route path="executive" lazy={page(() => import("./features/executive/Executive"), { view: "overview" as const })} />
      <Route path="executive/reports" lazy={page(() => import("./features/executive/Executive"), { view: "reports" as const })} />
      <Route path="executive/cash" lazy={page(() => import("./features/executive/Executive"), { view: "cash" as const })} />
      <Route path="executive/decisions" lazy={page(() => import("./features/executive/Executive"), { view: "decisions" as const })} />
      <Route path="reconciliations/new" lazy={page(() => import("./features/reconciliation/Prepare"), {}, closeScoped)} />
      <Route path="settlements/new" lazy={page(() => import("./features/settlements/Prepare"), {}, closeScoped)} />
      <Route path="accruals/new" lazy={page(() => import("./features/accruals/Prepare"), {}, closeScoped)} />
      <Route path="loans/new" lazy={page(() => import("./features/loans/Prepare"), {}, closeScoped)} />
      <Route path="owners/new" lazy={page(() => import("./features/owners/Prepare"), {}, closeScoped)} />
      <Route path="payroll/new" lazy={page(() => import("./features/payroll/Prepare"), {}, closeScoped)} />
      <Route path="prepaids/new" lazy={page(() => import("./features/prepaids/Prepare"), {}, closeScoped)} />
    </Route>
  </Route>,
);
