import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { Authentication } from "./features/auth/Auth";
import { App } from "./App";
import "./styles.css";

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
