import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  // The Tray design system both apps share (docs/design-system.md).
  resolve: { alias: { "@close/ui": fileURLToPath(new URL("../../packages/ui", import.meta.url)) } },
  server: {
    port: 5173,
    strictPort: true,
    proxy: { "/v1": process.env.API_ORIGIN ?? "http://127.0.0.1:8080" },
  },
});
