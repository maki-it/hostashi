// SPDX-License-Identifier: AGPL-3.0-or-later

import { existsSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
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

// The bundle carries code and fonts from npm packages whose licenses ask
// that their notice travels with them (MIT, OFL-1.1). This plugin writes
// the license texts of every bundled package (non-dev in package-lock.json,
// as checked by scripts/check-licenses.mjs) to third-party-licenses.txt,
// which the panel serves next to its assets.
function licenseNotices(): Plugin {
  return {
    name: "hostashi-license-notices",
    apply: "build",
    generateBundle() {
      const root = import.meta.dirname;
      const lock = JSON.parse(readFileSync(resolve(root, "package-lock.json"), "utf8")) as {
        packages: Record<string, { dev?: boolean; devOptional?: boolean; version?: string; license?: string }>;
      };
      const parts = ["Third-party software bundled in the Hostashi web interface.\n"];
      for (const [path, pkg] of Object.entries(lock.packages)) {
        if (path === "" || pkg.dev || pkg.devOptional) continue;
        const dir = resolve(root, path);
        const file = existsSync(dir) ? readdirSync(dir).find((f) => /^(licen[cs]e|copying)(\.|$)/i.test(f)) : undefined;
        if (!file) throw new Error(`no license file in ${path}`);
        const name = path.replace(/^(.*\/)?node_modules\//, "");
        parts.push(`${"=".repeat(72)}\n${name} ${pkg.version ?? ""} (${pkg.license ?? "unknown"})\n${"=".repeat(72)}\n`);
        parts.push(readFileSync(resolve(dir, file), "utf8").trim() + "\n");
      }
      this.emitFile({ type: "asset", fileName: "third-party-licenses.txt", source: parts.join("\n") });
    },
  };
}

export default defineConfig({
  plugins: [react(), keepDir(), licenseNotices()],
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
