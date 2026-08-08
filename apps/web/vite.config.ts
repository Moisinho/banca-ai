/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath, URL } from "node:url";

export default defineConfig({
  plugins: [react(), tailwindcss()],

  resolve: {
    alias: {
      // This file is an ES module, where __dirname does not exist.
      // import.meta.url is the ESM equivalent.
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },

  server: {
    host: "0.0.0.0",
    port: 5173,

    watch: {
      // Docker on Windows does not propagate filesystem events through bind
      // mounts, so the native watcher never fires. Polling is slower but it is
      // the only thing that reliably detects changes in this setup.
      usePolling: true,
      interval: 300,
    },

    hmr: {
      // The browser connects from the host, so the HMR websocket must point at
      // the published port rather than the container's internal hostname.
      clientPort: 5173,
    },
  },

  test: {
    globals: true,
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
  },
});
