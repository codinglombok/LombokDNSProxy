/**
 * LombokDNSProxy — browser extension build config
 *
 * Builds both targets from one command:
 *   npm run build                  → Chrome/Edge/Brave (MV3), dist/
 *   BROWSER=firefox npm run build  → Firefox (MV2),        dist-firefox/
 *
 * `vite-plugin-web-extension` reads the manifest, treats every file it
 * references as an entry point, compiles the TypeScript, and rewrites the
 * `.ts` paths to `.js` in the emitted manifest — which is why the source
 * manifests point at `background.ts` / `content.ts` rather than `.js`.
 *
 * Part of the Lombok Ecosystem · Apache 2.0 · @codinglombok
 */

import { defineConfig } from "vite";
import webExtension from "vite-plugin-web-extension";
import { resolve } from "node:path";

const isFirefox = process.env.BROWSER === "firefox";

export default defineConfig({
  root: __dirname,

  // Icons and the declarativeNetRequest rule file are static — the plugin only
  // treats scripts and HTML as entry points, so anything the manifest points at
  // by path has to come through publicDir, which Vite copies to the dist root.
  // That's why they live under public/ and the manifest still says
  // "icons/icon16.png" / "rules/blocklist.json".
  publicDir: resolve(__dirname, "public"),

  build: {
    outDir: isFirefox ? "dist-firefox" : "dist",
    emptyOutDir: true,
    // Extensions are loaded from disk, not served — readable output is worth
    // more than a few saved kilobytes when debugging a rejected store review.
    minify: false,
    sourcemap: true,
  },

  plugins: [
    webExtension({
      manifest: resolve(
        __dirname,
        isFirefox ? "firefox/manifest.json" : "manifest.json",
      ),

      browser: isFirefox ? "firefox" : "chrome",

      // The MV2 background script lives under firefox/ and is only reachable
      // through that manifest, so it needs declaring explicitly for the
      // Chrome build not to drop it and for watch mode to track it.
      additionalInputs: isFirefox ? ["firefox/background-mv2.ts"] : [],

      // CI has no browser to launch into, and a build that tries to open one
      // hangs the job rather than failing it.
      disableAutoLaunch: true,

      watchFilePaths: [
        resolve(__dirname, "manifest.json"),
        resolve(__dirname, "firefox/manifest.json"),
      ],

      printSummary: true,
    }),
  ],
});
