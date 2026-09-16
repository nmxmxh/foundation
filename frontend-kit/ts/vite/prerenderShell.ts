/**
 * Paint before JavaScript: write the app's first screen into the built HTML.
 *
 * A client-rendered app ships `<div id="root"></div>`, so first contentful
 * paint waits on the whole script graph to download, parse, execute and render
 * (research doc §15.3: FCP 2,035 ms on the floor emulation, all of it script).
 * Linaria's CSS is a static file Vite already links in the `<head>`, so if the
 * HTML also carries the markup, the browser paints as soon as HTML + CSS
 * arrive, and React hydrates the same DOM later instead of building it.
 *
 * After the client build, this plugin builds `entry` for SSR with the app's own
 * config (same aliases, same wyw transform, so class names match the client
 * build), calls its `render(url)` for every route, and writes the result into
 * the built HTML's root element. The entry owns rendering (renderToString,
 * `react-dom/static` prerender, routers), so the plugin stays React-agnostic.
 *
 *   // vite.config.ts
 *   prerenderShell({ entry: "src/entry-server.tsx", routes: ["/"] })
 *
 *   // src/entry-server.tsx
 *   export function render(url: string): string { ... }
 *
 * Pair it with `mountRoot` from @ovasabi/frontend-kit on the client, which
 * hydrates when the root already has markup and client-renders otherwise.
 *
 * Only render what is identical on server and client for that URL: no
 * `window`, time, randomness or signed-in state during render. Anything that
 * depends on those belongs in an effect, or behind a skeleton of the same size.
 */
import { mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { dirname, isAbsolute, join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import type { Plugin, ResolvedConfig } from "vite";
import { criticalCss } from "./criticalCss.ts";

/**
 * What `render(url)` returns: the root's markup, or the markup plus tags for
 * the document head — for example the `<style>` tags a runtime CSS-in-JS
 * library collects while rendering (styled-components' ServerStyleSheet), so
 * an app mid-migration to Linaria can still paint before JavaScript.
 */
export type PrerenderResult = string | { html: string; head?: string };

export interface PrerenderRoute {
  /** URL passed to `render`, e.g. "/" or "/pricing?plan=pro". */
  url: string;
  /** Built HTML file, relative to `build.outDir`, that receives the markup. Defaults from the URL path. */
  file?: string;
}

export interface PrerenderShellOptions {
  /** Server entry exporting `render(url): string | Promise<string>`. Relative to the Vite root. */
  entry: string;
  /** Routes to prerender. Defaults to ["/"]. */
  routes?: Array<string | PrerenderRoute>;
  /** Id of the element the client mounts into. Defaults to "root". */
  rootId?: string;
  /**
   * What the SSR build bundles rather than leaving for Node to resolve.
   * Defaults to `true` — everything.
   *
   * Anything half-bundled produces two copies of React: the scaffold aliases
   * `react` and `react-dom` to absolute paths, which Vite bundles, while a bare
   * import like `react-router-dom` stays external and loads React again from
   * node_modules. The renderer then sets the hook dispatcher on one copy and the
   * router reads it from the other, and the build dies on `Cannot read
   * properties of null (reading 'useContext')`. Narrow this only for a package
   * that genuinely cannot be bundled, and keep React in whichever half it lands.
   */
  noExternal?: true | Array<string | RegExp>;
  /**
   * Put the first screen's CSS in the HTML, so first paint needs no stylesheet
   * round trip. "subset": inline only the rules that can match the prerendered
   * markup (see criticalCss.ts) and load the full stylesheet at the end of
   * <body>, where it stays cacheable and no longer blocks the first screen.
   * "all": inline the whole stylesheet and drop the link. Needs a CSP that
   * allows inline styles (server-kit's does). Default: off.
   */
  criticalCss?: false | "subset" | "all";
  /**
   * Extra config for the SSR build, merged over the app's config file. The SSR
   * build starts from the config file, so anything passed inline to the client
   * build (aliases, defines) must be repeated here to reach the server render.
   */
  ssrConfig?: import("vite").InlineConfig;
}

const STYLESHEET_LINK = /<link\b[^>]*\brel=["']?stylesheet["']?[^>]*>/gi;

/** Promotes the preloaded full stylesheets in place; external, so a strict script-src allows it. */
export const PRERENDER_CSS_LOADER = "prerender-css.js";
const LOADER_SOURCE = `for (const link of document.querySelectorAll("link[data-prerender-css]")) link.rel = "stylesheet";\n`;

export async function inlineStylesheets(html: string, markup: string, outDir: string, base: string, mode: "subset" | "all") {
  let inlined = 0;
  let full = 0;
  let deferred = false;
  for (const link of html.match(STYLESHEET_LINK) ?? []) {
    const href = link.match(/\bhref=["']?([^"' >]+)/)?.[1];
    if (!href || /^(https?:)?\/\//.test(href)) continue;
    const relativeHref = href.startsWith(base) ? href.slice(base.length) : href.replace(/^\.?\//, "");
    const css = await readFile(join(outDir, relativeHref), "utf8");
    const inline = mode === "all" ? css : criticalCss(css, markup);
    inlined += inline.length;
    full += css.length;
    let replacement = `<style data-prerender-css="${mode}">${inline.replace(/<\/style/gi, "<\\/style")}</style>`;
    if (mode === "subset") {
      /*
       * The full stylesheet keeps the link's exact place in the head. Moving it
       * (e.g. to the end of <body>) puts it after every other style in the
       * document — a CSS-in-JS library's <style> tags included — and its rules
       * then win ties they used to lose: measured on ChooseChow as CLS 0.077 →
       * 0.143 when the late sheet landed. So it downloads now as a preload,
       * never blocking paint, and a deferred module promotes it in place.
       */
      const preload = link.replace(/\brel=["']?stylesheet["']?/i, 'rel="preload" as="style" data-prerender-css');
      replacement += `${preload}<noscript>${link}</noscript>`;
      deferred = true;
    }
    html = html.replace(link, () => replacement);
  }
  if (deferred) {
    await writeFile(join(outDir, PRERENDER_CSS_LOADER), LOADER_SOURCE);
    // Before the app's entry module: deferred modules run in document order, so
    // the full stylesheet is promoted before hydration renders anything new.
    const loader = `<script type="module" src="${base}${PRERENDER_CSS_LOADER}"></script>`;
    html = /<script\b[^>]*type=["']?module/i.test(html)
      ? html.replace(/<script\b[^>]*type=["']?module/i, (entry) => `${loader}\n    ${entry}`)
      : html.replace(/<\/head>/i, () => `${loader}\n  </head>`);
  }
  return { html, inlined, full };
}

const fileForUrl = (url: string) => {
  const path = url.split(/[?#]/)[0] ?? "/";
  if (path.endsWith(".html")) return path.replace(/^\//, "");
  return join(path.replace(/^\//, ""), "index.html");
};

export function prerenderShell(options: PrerenderShellOptions): Plugin {
  let config: ResolvedConfig;
  const rootId = options.rootId ?? "root";
  const routes = (options.routes ?? ["/"]).map((r) => (typeof r === "string" ? { url: r } : r));
  const empty = new RegExp(`<div id="${rootId}"></div>`);

  return {
    name: "ovasabi:prerender-shell",
    apply: (_config, env) => env.command === "build" && !env.isSsrBuild,
    configResolved(resolved) {
      config = resolved;
    },
    async closeBundle() {
      // The app's own Vite, not whichever copy sits next to this file: the
      // vendored kit has no node_modules of its own.
      const { build, mergeConfig } = await appVite(config.root);
      const outDir = resolve(config.root, config.build.outDir);
      const ssrOutDir = join(config.root, "node_modules/.ovasabi-prerender");
      const entry = isAbsolute(options.entry) ? options.entry : resolve(config.root, options.entry);
      const started = performance.now();

      const ssrBase: import("vite").InlineConfig = {
        configFile: config.configFile ?? false,
        root: config.root,
        mode: config.mode,
        logLevel: "warn",
        ssr: { noExternal: options.noExternal ?? true },
        // `apply` above skips the SSR pass, and it reads `isSsrBuild`, which Vite
        // derives from `build.ssr` at config-resolution time. The post plugin
        // below sets it too late for that: without this line the SSR build
        // re-reads the config file, applies this plugin again, and recurses
        // until the heap is gone.
        build: { ssr: entry },
      };
      await build({
        ...mergeConfig(ssrBase, options.ssrConfig ?? {}),
        plugins: [
          ...(options.ssrConfig?.plugins ?? []),
          {
            // Runs after the config file is merged: drop client-only output
            // settings (manualChunks cannot name SSR externals) and point the
            // build at the server entry.
            name: "ovasabi:prerender-shell:ssr-output",
            enforce: "post",
            config(user) {
              user.build = {
                ...user.build,
                ssr: entry,
                outDir: ssrOutDir,
                emptyOutDir: true,
                sourcemap: false,
                minify: false,
                copyPublicDir: false,
                rollupOptions: {
                  // Keep declared externals (e.g. from ssrConfig): Rollup-level externals
                  // skip resolution, which bare imports from outside the app root need.
                  external: user.build?.rollupOptions?.external,
                  input: entry,
                  output: { format: "es", entryFileNames: "entry-server.mjs" },
                },
              };
            },
          },
        ],
      });

      try {
        const module = (await import(`${pathToFileURL(join(ssrOutDir, "entry-server.mjs")).href}?t=${Date.now()}`)) as {
          render?: (url: string) => PrerenderResult | Promise<PrerenderResult>;
        };
        if (typeof module.render !== "function") throw new Error(`${options.entry} must export render(url)`);
        for (const route of routes) {
          const file = join(outDir, route.file ?? fileForUrl(route.url));
          // A route with no HTML of its own starts from the SPA's index.html.
          const template = await readFile((await exists(file)) ? file : join(outDir, "index.html"), "utf8");
          if (!empty.test(template)) throw new Error(`${file}: no empty <div id="${rootId}"></div> to fill`);
          const rendered = await module.render(route.url);
          const { html: markup, head = "" } = typeof rendered === "string" ? { html: rendered } : rendered;
          let html = template.replace(empty, () => `<div id="${rootId}">${markup}</div>`);
          if (head) html = html.replace(/<\/head>/i, () => `${head}\n  </head>`);
          if (options.criticalCss) {
            const result = await inlineStylesheets(html, markup, outDir, config.base, options.criticalCss);
            html = result.html;
            config.logger.info(`${route.url}: inlined ${result.inlined} of ${result.full} CSS bytes (${options.criticalCss})`);
          }
          await mkdir(dirname(file), { recursive: true });
          await writeFile(file, html);
        }
      } finally {
        // PRERENDER_KEEP_SSR=1 keeps the server bundle for debugging a failed render.
        if (!process.env.PRERENDER_KEEP_SSR) await rm(ssrOutDir, { recursive: true, force: true });
      }
      config.logger.info(`prerendered ${routes.length} route(s) in ${Math.round(performance.now() - started)} ms`);
    },
  };
}

async function appVite(root: string): Promise<Pick<typeof import("vite"), "build" | "mergeConfig">> {
  const require = createRequire(join(root, "package.json"));
  const manifestPath = require.resolve("vite/package.json");
  const manifest = JSON.parse(await readFile(manifestPath, "utf8")) as { exports: Record<string, unknown> };
  // Conditional exports nest arbitrarily ({ import: { default } }, { default }, "…").
  const pick = (target: unknown): string | undefined => {
    if (typeof target === "string") return target;
    if (!target || typeof target !== "object") return undefined;
    const conditions = target as Record<string, unknown>;
    return pick(conditions.import) ?? pick(conditions.node) ?? pick(conditions.default);
  };
  const entry = pick(manifest.exports["."]);
  if (!entry) throw new Error(`cannot find the ESM entry of ${manifestPath}`);
  const vite = (await import(pathToFileURL(join(dirname(manifestPath), entry)).href)) as typeof import("vite");
  return { build: vite.build, mergeConfig: vite.mergeConfig };
}

const exists = async (file: string) =>
  readFile(file).then(
    () => true,
    () => false,
  );
