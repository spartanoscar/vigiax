import { defineConfig } from "vite";

// In development the Go service runs on :8790 (server/cmd/vigiax); in production
// nginx serves both under vigiax.bukivo.com.
export default defineConfig({
  server: { proxy: { "/api": { target: "http://127.0.0.1:8790", changeOrigin: false } } },
});
