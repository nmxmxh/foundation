import { fileURLToPath } from "node:url";
import { playwright } from "@vitest/browser-playwright";
import wyw from "@wyw-in-js/vite";
import { defineConfig } from "vitest/config";

const here = (path: string) => fileURLToPath(new URL(path, import.meta.url));

/*
 * The lab tests Foundation's own sources, not published builds, so each package
 * is aliased to its `src/index.ts`. Those sources sit outside this directory and
 * would otherwise resolve `react` and `styled-components` from their own
 * package's node_modules — a second React breaks every hook, and a second
 * styled-components splits the theme context. `dedupe` pins them to this lab's
 * single copy.
 */
const shared = {
  /*
   * ui-minimal's styles are extracted at build time by Linaria (research doc
   * 14.7), so every lane transforms its sources through wyw-in-js: Linaria's
   * `styled` throws if it ever runs untransformed. Vendor prefixes are off —
   * every engine the products target reads unprefixed properties, and the
   * prefixes were pure bytes.
   */
  plugins: [
    wyw({
      include: ["**/ui-minimal/ts/src/**/*.{ts,tsx}", "**/frontend-lab/linaria/**/*.{ts,tsx}"],
      prefixer: false,
    }),
  ],
  resolve: {
    // Exact matches: a prefix alias for the package would swallow its sub-entries.
    alias: [
      { find: /^@ovasabi\/ui-minimal$/, replacement: here("../ui-minimal/ts/src/index.ts") },
      { find: /^@ovasabi\/ui-minimal\/tokens$/, replacement: here("../ui-minimal/ts/src/tokens.ts") },
      { find: /^@ovasabi\/ui-minimal\/styled-components$/, replacement: here("../ui-minimal/ts/src/styledComponents.tsx") },
      { find: /^@ovasabi\/runtime-browser$/, replacement: here("../runtime-sdk/ts/browser-host/src/index.ts") },
      { find: /^@ovasabi\/frontend-kit$/, replacement: here("../frontend-kit/ts/src/index.ts") },
    ],
    dedupe: ["react", "react-dom", "styled-components", "framer-motion", "@linaria/react", "@linaria/core"],
  },
  server: {
    fs: { allow: [here("..")] },
    /*
     * Cross-origin isolation, so the worker lanes (frame clock pulse, shared
     * arenas) exist in the browser lane exactly as they do in a production
     * page. Without it SharedArrayBuffer is absent and every worker path
     * silently takes its main-thread fallback — the failure the lab must catch.
     */
    headers: {
      "Cross-Origin-Opener-Policy": "same-origin",
      "Cross-Origin-Embedder-Policy": "require-corp",
    },
  },
};

export default defineConfig({
  ...shared,
  test: {
    projects: [
      {
        test: {
          name: "ssr",
          environment: "node",
          include: ["src/ssr/**/*.test.ts"],
          // Both evals build the kit into results/kit-build (emptyOutDir); run in
          // parallel, one file imports a build the other is still writing.
          fileParallelism: false,
          // A full sweep renders ~2,200 cases with styled-components SSR.
          testTimeout: 120_000,
        },
      },
      {
        test: {
          name: "dom",
          environment: "jsdom",
          include: ["src/dom/**/*.test.{ts,tsx}"],
        },
      },
      {
        test: {
          name: "browser",
          include: ["src/browser/**/*.test.{ts,tsx}"],
          browser: {
            enabled: true,
            headless: true,
            provider: playwright(),
            instances: [{ browser: "chromium" }],
          },
        },
      },
      {
        test: {
          name: "gpu",
          include: ["src/gpu/**/*.test.{ts,tsx}"],
          // Surfaces run for seconds to let the quality ladder act.
          testTimeout: 120_000,
          // One GPU, one test file at a time: parallel files would measure each other.
          fileParallelism: false,
          browser: {
            enabled: true,
            headless: true,
            /*
             * The real GPU. Headless Chromium's default backend is SwiftShader,
             * a CPU implementation of Vulkan with no WebGPU adapter, so the
             * `browser` lane cannot say anything about graphics hardware. ANGLE
             * on Metal gives WebGL2 and a non-fallback WebGPU adapter on the
             * host GPU. macOS only; the capability test records the adapter so a
             * run on other hardware says what it measured.
             */
            provider: playwright({ launchOptions: { args: ["--use-angle=metal", "--enable-unsafe-webgpu"] } }),
            instances: [{ browser: "chromium" }],
          },
        },
      },
    ],
  },
});
