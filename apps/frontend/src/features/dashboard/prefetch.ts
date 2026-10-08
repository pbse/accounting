import { pages } from "../../pages";
import { prefetchWorkspace } from "../closes/workspace";

// prefetchRoute starts what a workspace page needs, its code and the reads it makes first, before the page asks: when
// the app starts on that page, and when the pointer rests on a link to it. The page's own requests then share these
// (see workspaceRequest), so the code and the data arrive together instead of one after the other.
// Prefetching only saves time, so nothing it meets (a malformed address among them) may stop the page it prepares.
// launch is true only for the page the app opens on, whose layout (and its unread count) mounts with it.
export function prefetchRoute(to: string, launch = false) {
  try {
    startRoute(to, launch);
  } catch {
    // The page itself handles what could not be prepared.
  }
}

function startRoute(to: string, launch: boolean) {
  const url = new URL(to, window.location.origin);
  const path = url.pathname.replace(/\/+$/, "");
  if (!path.startsWith("/dashboard")) return;
  void pages.layout().catch(() => undefined);
  const load = (page: () => Promise<unknown>) => void page().catch(() => undefined);
  const close = path.match(/^\/dashboard\/closes\/([^/]+)$/);
  if (path === "/dashboard") {
    load(pages.dashboard);
    prefetchWorkspace("/home", "/review-queue?client=");
  } else if (path === "/dashboard/closes") {
    load(pages.closes);
    const query = url.searchParams.get("q");
    prefetchWorkspace("", "/roster", ...(query ? [`?q=${encodeURIComponent(query)}`] : []));
  } else if (close) {
    load(pages.close);
    const id = decodeURIComponent(close[1]);
    // The brief is not read ahead: it is stamped with the time it arrives, so it must arrive when it is shown.
    prefetchWorkspace(`/${encodeURIComponent(id)}`);
  } else if (path.startsWith("/dashboard/closes/")) load(pages.workpaper);
  else if (path === "/dashboard/work") load(pages.preparation);
  else if (path === "/dashboard/settings") load(pages.settings);
  else if (path === "/dashboard/measure") load(pages.measure);
  // The unread count in the top bar mounts once with the layout and then polls on its own.
  if (launch && path !== "/dashboard/executive" && !path.startsWith("/dashboard/executive/")) prefetchWorkspace("/attention");
}

// Any workspace link the pointer rests on (a sixth of a second) or the keyboard focuses starts its page.
export function prefetchOnIntent(root: Document = document) {
  let timer: number | undefined;
  const target = (event: Event) => {
    const link = (event.target as Element | null)?.closest?.("a[href]") as HTMLAnchorElement | null;
    return link && link.origin === window.location.origin && link.pathname.startsWith("/dashboard") ? link : null;
  };
  root.addEventListener("mouseover", (event) => {
    const link = target(event);
    window.clearTimeout(timer);
    if (link) timer = window.setTimeout(() => prefetchRoute(link.pathname + link.search), 160);
  });
  root.addEventListener("focusin", (event) => {
    const link = target(event);
    if (link) prefetchRoute(link.pathname + link.search);
  });
}
