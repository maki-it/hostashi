// SPDX-License-Identifier: AGPL-3.0-or-later

import { writeFileSync } from "node:fs";
import { resolve } from "node:path";
import react from "@vitejs/plugin-react";
import type { Plugin } from "vite";
import { defineConfig } from "vitest/config";

// The Go package internal/ui embeds this directory. It must exist in a fresh
// checkout so that Go builds without Node, hence the committed .gitkeep,
// which emptyOutDir deletes and this plugin puts back.
const outDir = resolve(import.meta.dirname, "../internal/ui/dist");

function keepDir(): Plugin {
  return {
    name: "hostashi-keep-dist",
    apply: "build",
    closeBundle() {
      writeFileSync(resolve(outDir, ".gitkeep"), "");
    },
  };
}

export default defineConfig({
  plugins: [react(), keepDir()],
  build: {
    outDir,
    emptyOutDir: true,
    sourcemap: false,
    // Everything the panel needs comes from its own origin; no CDN.
    assetsInlineLimit: 0,
  },
  server: {
    // npm run dev: the API runs as "hostashi serve" on loopback.
    proxy: { "/api": { target: "http://127.0.0.1:8080" } },
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test-setup.ts"],
  },
});
