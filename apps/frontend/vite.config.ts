import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig(({ isSsrBuild }) => ({
  plugins: [react()],
  // A server build is one module: the edge Worker bundles it whole (src/entry-server.tsx, docs/server-rendering.md).
  build: isSsrBuild ? { copyPublicDir: false, rollupOptions: { output: { inlineDynamicImports: true } } } : {},
  // The Tray design system both apps share (docs/design-system.md).
  resolve: { alias: { "@close/ui": fileURLToPath(new URL("../../packages/ui", import.meta.url)) } },
  server: {
    port: 5173,
    strictPort: true,
    proxy: { "/v1": process.env.API_ORIGIN ?? "http://127.0.0.1:8080" },
  },
}));
