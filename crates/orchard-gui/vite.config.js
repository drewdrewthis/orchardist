import { defineConfig } from "vite";
import { sveltekit } from "@sveltejs/kit/vite";
import houdini from "houdini/vite";
import tailwindcss from "@tailwindcss/vite";

const host = process.env.TAURI_DEV_HOST;

// https://vite.dev/config/
// Plugin order: houdini → sveltekit → tailwindcss. See docs/adr/020-tailwind-css-adoption.md.
export default defineConfig(async () => ({
  plugins: [houdini(), sveltekit(), tailwindcss()],

  // Vite options tailored for Tauri development and only applied in `tauri dev` or `tauri build`
  //
  // 1. prevent Vite from obscuring rust errors
  clearScreen: false,
  // 2. tauri expects a fixed port, fail if that port is not available
  server: {
    port: 1420,
    strictPort: true,
    host: host || false,
    hmr: host
      ? {
          protocol: "ws",
          host,
          port: 1421,
        }
      : undefined,
    watch: {
      // 3. tell Vite to ignore watching `src-tauri`
      ignored: ["**/src-tauri/**"],
    },
    // Proxy daemon GraphQL through Vite during browser dev so the GUI can be
    // smoke-tested in a regular browser without Tauri's CORS-bypass. In Tauri,
    // `127.0.0.1:7777` is reached directly (and `csp: null` lets fetch through),
    // so this proxy is only consulted in browser dev.
    proxy: {
      "/__daemon": {
        target: "http://127.0.0.1:7777",
        changeOrigin: true,
        ws: true,
        rewrite: (/** @type {string} */ p) => p.replace(/^\/__daemon/, ""),
      },
    },
  },
}));
