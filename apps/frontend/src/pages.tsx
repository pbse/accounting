import { lazy, useRef, type ComponentType } from "react";

// A workspace page whose code loads on first use, and can be asked for ahead (load). A page whose code is already here
// when it first renders draws at once. React.lazy alone would suspend it anyway, and React waits up to 300 ms before
// revealing what was suspended, so a launch that lazily loaded the layout and then the page lost most of a second.
function page<P extends object>(load: () => Promise<{ default: ComponentType<P> }>) {
  let ready: ComponentType<P> | undefined;
  let loading: Promise<{ default: ComponentType<P> }> | undefined;
  const start = () =>
    (loading ??= load().then(
      (module) => ((ready = module.default), module),
      (error) => {
        loading = undefined;
        throw error;
      },
    ));
  const Lazy = lazy(start);
  function Page(props: P) {
    // Decided once per mount, so a page never switches between the two and loses its state.
    const direct = useRef(ready);
    const Ready = direct.current;
    return Ready ? <Ready {...props} /> : <Lazy {...props} />;
  }
  return Object.assign(Page, { load: start });
}

export const pages = {
  layout: page(() => import("./features/dashboard/Layout")),
  dashboard: page(() => import("./features/dashboard/Dashboard")),
  closes: page(() => import("./features/closes/ClosesList")),
  close: page(() => import("./features/closes/ClientClose")),
  workpaper: page(() => import("./features/closes/WorkpaperView")),
  preparation: page(async () => ({ default: (await import("./features/closes/Work")).Work })),
  settings: page(() => import("./features/settings/Settings")),
  measure: page(() => import("./features/closes/Measure")),
};
