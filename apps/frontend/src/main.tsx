import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { Authentication } from "./features/auth/Auth";
import { App } from "./App";
import { prefetchOnIntent, prefetchRoute } from "./features/dashboard/prefetch";
import "./styles.css";
import "@close/ui/theme/tokens.css";
import "@close/ui/theme/shell.css";
import "./theme/workspace.css";

// A workspace page's code and first data are asked for now, while React starts.
prefetchRoute(window.location.pathname + window.location.search, true);
prefetchOnIntent();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    {window.location.pathname === "/request" ? (
      <App />
    ) : (
      <Authentication>
        <App />
      </Authentication>
    )}
  </StrictMode>,
);
