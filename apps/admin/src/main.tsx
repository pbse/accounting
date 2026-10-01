import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { StaffAccess } from "./auth";
import { App } from "./App";
import "../../frontend/src/styles.css";
import "@close/ui/theme/tokens.css";
import "@close/ui/theme/shell.css";
import "./admin.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <StaffAccess>
      <App />
    </StaffAccess>
  </StrictMode>,
);
