import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The staff admin panel (docs/admin-panel.md). The edge Worker serves it under /admin from the same
// assets as the firm app, so it builds into that app's dist. It borrows the firm app's base stylesheet and
// shares the Tray design system with it (packages/ui, docs/design-system.md).
export default defineConfig({
  base: "/admin/",
  plugins: [react()],
  resolve: { alias: { "@close/ui": fileURLToPath(new URL("../../packages/ui", import.meta.url)) } },
  build: { outDir: "../frontend/dist/admin", emptyOutDir: true },
  server: {
    port: 5174,
    strictPort: true,
    proxy: { "/v1": process.env.API_ORIGIN ?? "http://127.0.0.1:8080" },
    fs: { allow: ["../.."] },
  },
});
