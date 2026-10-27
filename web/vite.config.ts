import { resolve } from "node:path";
import { defineConfig } from "vite";

// In development the Go service runs on :8790 (server/cmd/vigiax); in production
// nginx serves both under vigiax.bukivo.com. Two pages: the landing (/) and the
// digital twin (/twin/), which is the only one that loads Three.js.
export default defineConfig({
  server: { proxy: { "/api": { target: "http://127.0.0.1:8790", changeOrigin: false } } },
  build: {
    rollupOptions: {
      input: {
        landing: resolve(import.meta.dirname, "index.html"),
        twin: resolve(import.meta.dirname, "twin/index.html"),
      },
    },
  },
});
