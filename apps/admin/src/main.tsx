import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { StaffAccess } from "./auth";
import { App } from "./App";
import "../../frontend/src/styles.css";
import "./admin.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <StaffAccess>
      <App />
    </StaffAccess>
  </StrictMode>,
);
