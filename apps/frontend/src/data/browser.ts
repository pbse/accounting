import { useEffect, useState } from "react";
import { useLocation } from "react-router";

// useBrowserHash is the address's fragment, which only the browser knows (it never reaches the server): empty on the
// server and in the browser's first render alike, then the router's fragment, kept current as links change it. A
// provider's sign-in returns its answer there (QuickBooks, Xero), and a link can name a panel to open (#xero).
export function useBrowserHash(): string {
  const { hash } = useLocation();
  const [hydrated, setHydrated] = useState(false);
  useEffect(() => setHydrated(true), []);
  return hydrated ? hash : "";
}
