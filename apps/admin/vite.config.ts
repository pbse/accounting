import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The staff admin panel (docs/admin-panel.md). The edge Worker serves it under /admin from the same
// assets as the firm app, so it builds into that app's dist; it borrows the firm app's stylesheet.
export default defineConfig({
  base: "/admin/",
  plugins: [react()],
  build: { outDir: "../frontend/dist/admin", emptyOutDir: true },
  server: {
    port: 5174,
    strictPort: true,
    proxy: { "/v1": process.env.API_ORIGIN ?? "http://127.0.0.1:8080" },
    fs: { allow: [".."] },
  },
});
