#!/usr/bin/env node
/**
 * Build ChooseChow's production frontend with the landing page prerendered, as
 * a load-profile variant — without editing the app.
 *
 *   node profile/apps/choosechow/buildPrerender.mjs            # prerender + critical CSS
 *   CHOW_CRITICAL=0 node profile/apps/choosechow/buildPrerender.mjs   # prerender only
 *
 * Uses the app's own vite.config.ts and node_modules, plus two lab plugins:
 * prerenderShell (frontend-kit) with profile/apps/choosechow/entry-server.tsx,
 * and a transform that makes src/main.tsx hydrate prerendered markup instead
 * of replacing it (the app's main.tsx calls createRoot). Output goes to
 * results/apps/choosechow/<variant>/.
 *
 * Side effect to know about: the app's routePagesPlugin always rewrites
 * chowdash_rider_v1/frontend/dist (its own gitignored build output) from the
 * dist/index.html already there; the content it writes is unchanged.
 */
import { readFileSync, rmSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { prerenderShell } from "../../../../frontend-kit/ts/vite/prerenderShell.ts";

const here = (p) => fileURLToPath(new URL(p, import.meta.url));
const app = here("../../../../../chowdash_rider_v1/frontend/");
// CHOW_CRITICAL: 0 (none), subset (default), all (whole stylesheet inlined).
const criticalMode = { 0: false, subset: "subset", 1: "subset", all: "all" }[process.env.CHOW_CRITICAL ?? "subset"];
if (criticalMode === undefined) throw new Error(`CHOW_CRITICAL must be 0, subset or all`);
const variant = { false: "prerender", subset: "prerender-critical", all: "prerender-inline" }[String(criticalMode)];
// CHOW_FONT_FALLBACK=1: size-adjusted fallback faces from results/fonts/choosechow-fallbacks.css.
const fontFallback = process.env.CHOW_FONT_FALLBACK === "1";
// CHOW_STATIC_FONTS=1: @font-face moves out of styled-components into a static <style> in the head.
const staticFonts = process.env.CHOW_STATIC_FONTS === "1";
const outDir = here(`../../../results/apps/choosechow/${variant}${fontFallback ? "-fonts" : ""}${staticFonts ? "-static" : ""}/`);

// The app's Vite, not the lab's.
const require = createRequire(join(app, "package.json"));
const { build } = await import(pathToFileURL(join(dirname(require.resolve("vite/package.json")), "dist/node/index.js")).href);

const MOUNT_FROM = "createRoot(document.getElementById('root')!).render(";
const hydrateMain = {
  name: "lab:choosechow-hydrate-main",
  enforce: "pre",
  transform(code, id) {
    if (!id.endsWith("/src/main.tsx")) return null;
    if (!code.includes(MOUNT_FROM)) throw new Error("src/main.tsx no longer mounts the way this transform expects");
    return code
      .replace("import { createRoot } from 'react-dom/client'", "import { createRoot, hydrateRoot } from 'react-dom/client'")
      .replace(
        MOUNT_FROM,
        // Leading semicolon: the app's source has none, and a line starting with
        // `(` would otherwise call the previous statement's result
        // (`sharedUiQuality()(...)` — "vC(...) is not a function" after minify).
        ";((element) => { const container = document.getElementById('root')!; if (container.hasChildNodes()) hydrateRoot(container, element); else createRoot(container).render(element) })(",
      );
  },
};

// The lab entry lives outside the app, so its bare imports cannot resolve from
// its own directory. Left external, they are resolved by Node at render time
// from the server bundle's location — inside the app's node_modules — so the
// app's own copies load through their real ESM entries. Two things that did
// not work: aliasing them to the package directories (Vite bundled
// react-router-dom's CommonJS build, which then failed to import
// react-router/dom), and `ssr.external` (Vite resolves the package from the
// importer before honouring it, and the importer is outside the app). Rollup's
// own `external` skips resolution. Only react-router-dom: styled-components is
// already reachable from any importer through the app's own alias, and as an
// external it loads through its CommonJS entry, where the default import is the
// exports object (`styled.nav is not a function`). Bundled, it is one ESM
// instance shared by the app's components and ServerStyleSheet.
// React and ReactDOM must be external with the router: the app aliases them to
// absolute paths, so Vite bundles them, while an external react-router loads
// Node's copy — two Reacts, and the router's hooks read a null dispatcher
// (`Cannot read properties of null (reading 'useContext')`).
const labEntryExternals = {
  build: { rollupOptions: { external: [/^react($|\/)/, /^react-dom($|\/)/, /^react-router(-dom)?($|\/)/] } },
};

/*
 * Size-adjusted fallback faces, inserted into the app's own font stacks — a lab
 * transform, so the app's files stay untouched. The faces come from
 * profile/fontFallbackMetrics.mjs. Applied to the client build and to the
 * server render (passed through ssrConfig), or hydration would disagree.
 */
const DISPLAY_STACK = `displayFamily: '"Plus Jakarta Sans", "Outfit", "Instrument Sans", -apple-system, sans-serif',`;
const FONT_FACES = "export const FontFaces = createGlobalStyle`";
const fontFallbackPlugin = {
  name: "lab:choosechow-font-fallback",
  enforce: "pre",
  transform(code, id) {
    if (id.endsWith("/src/styles/fonts.ts")) {
      if (!code.includes(FONT_FACES)) throw new Error("src/styles/fonts.ts no longer declares FontFaces the way this transform expects");
      const faces = readFileSync(here("../../../results/fonts/choosechow-fallbacks.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
      return code.replace(FONT_FACES, `${FONT_FACES}\n${faces}\n`);
    }
    if (id.endsWith("/src/styles/theme.ts")) {
      if (!code.includes(DISPLAY_STACK)) throw new Error("src/styles/theme.ts displayFamily changed; update the lab transform");
      // Each fallback sits after the web families it stands in for, before the system stack.
      // The body stack is ui-minimal's default (ChooseChow does not override it) plus its fallback.
      return code.replace(
        DISPLAY_STACK,
        `displayFamily: '"Plus Jakarta Sans", "Outfit", "Instrument Sans", "Plus Jakarta Sans Fallback", "Plus Jakarta Sans Fallback Roboto", -apple-system, sans-serif',\n` +
          `    bodyFamily: '"Instrument Sans", "Instrument Sans Fallback", "Instrument Sans Fallback Roboto", "Inter", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',`,
      );
    }
    return null;
  },
};

/*
 * Fonts as static CSS. styled-components moves server-rendered <style
 * data-styled> rules into its own sheet on hydration and removes the originals;
 * removing a sheet destroys its @font-face rules, so every web font downloads a
 * second time and the text swaps twice (measured: both fonts arrive at ~1.25 s
 * and again at ~5.9 s on the small phone). Here FontFaces renders nothing on
 * server and client, and its CSS (plus any fallback faces the plugin above
 * added) goes into the HTML head, where no library will ever remove it.
 */
let staticFontCss = "";
const staticFontsPlugin = {
  name: "lab:choosechow-static-fonts",
  enforce: "pre",
  transform(code, id) {
    if (!id.endsWith("/src/styles/fonts.ts")) return null;
    const start = code.indexOf(FONT_FACES);
    if (start === -1) throw new Error("src/styles/fonts.ts no longer declares FontFaces the way this transform expects");
    const end = code.indexOf("`", start + FONT_FACES.length);
    const css = code.slice(start + FONT_FACES.length, end);
    if (css.includes("${")) throw new Error("FontFaces interpolates values; a static copy would drop them");
    staticFontCss = css;
    return `${code.slice(0, start)}export const FontFaces = () => null${code.slice(end + 1)}`;
  },
  transformIndexHtml: {
    order: "post",
    handler(html) {
      if (!staticFontCss) throw new Error("FontFaces CSS was never captured");
      return html.replace("</head>", () => `<style data-static-fonts>${staticFontCss}</style>\n  </head>`);
    },
  },
};

rmSync(outDir, { recursive: true, force: true });
await build({
  configFile: join(app, "vite.config.ts"),
  root: app,
  logLevel: "info",
  plugins: [
    hydrateMain,
    ...(fontFallback ? [fontFallbackPlugin] : []),
    // After the fallback plugin, so the captured CSS includes the fallback faces.
    ...(staticFonts ? [staticFontsPlugin] : []),
    prerenderShell({
      entry: here("./entry-server.tsx"),
      routes: ["/"],
      criticalCss: criticalMode,
      noExternal: [/^@ovasabi\//, /[\\/]frontend-lab[\\/]/],
      ssrConfig: {
        ...labEntryExternals,
        plugins: [...(fontFallback ? [fontFallbackPlugin] : []), ...(staticFonts ? [staticFontsPlugin] : [])],
      },
    }),
  ],
  build: { outDir, emptyOutDir: true, sourcemap: false },
});
console.log(`built ${variant} → ${outDir}`);
