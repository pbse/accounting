// The workspace's page code, loaded on first use. A page's code is also asked for ahead (prefetchRoute), and a module
// asked for twice loads once.
export const pages = {
  layout: () => import("./features/dashboard/Layout"),
  dashboard: () => import("./features/dashboard/Dashboard"),
  closes: () => import("./features/closes/ClosesList"),
  close: () => import("./features/closes/ClientClose"),
  workpaper: () => import("./features/closes/WorkpaperView"),
  preparation: () => import("./features/closes/Work").then((m) => ({ default: m.Work })),
  settings: () => import("./features/settings/Settings"),
  measure: () => import("./features/closes/Measure"),
};
