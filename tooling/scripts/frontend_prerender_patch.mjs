#!/usr/bin/env node
/**
 * Managed patch: adopt the build-time prerender in an existing app
 * (docs/frontend_paint_performance_handover.md, rules P1 and P2).
 *
 * The build renders the first screen to HTML and inlines the CSS that markup
 * uses, so the browser paints before any script runs; `mountRoot` then hydrates
 * the same tree instead of replacing it. Measured on a mid phone: FCP
 * 1,198 → 585 ms, and 5,024 → 664 ms on ChooseChow's real landing page.
 *
 * `src/entry-server.tsx` seeds through the ordinary update (create mode), but
 * `vite.config.ts` and `main.tsx` are project-owned, so neither can be
 * delivered by an update — this patch edits them in place, exactly as
 * frontend_linaria_patch.mjs had to for the wyw plugin.
 *
 * Deliberately narrow. A prerender is only correct when the server and client
 * render the same markup for a URL, and an app that has grown its own Vite
 * plugins has also grown routes whose first screen may read the clock, the
 * window or a session (handover §7, trap 1). Those are reported as manual
 * steps rather than patched: the edit is mechanical, the judgement is not.
 *
 * Idempotent: every edit is guarded on the text it inserts.
 *
 *   node tooling/scripts/frontend_prerender_patch.mjs <frontend-root>
 *
 * Output: one line per file, `patched <path> <what>` or `manual <path> <why>`.
 */
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { join, relative } from "node:path";

const root = process.argv[2];
if (!root || !existsSync(root)) process.exit(0);
const out = (kind, file, text) => console.log(`${kind} ${relative(join(root, ".."), file)} ${text}`);

const viteConfig = join(root, "vite.config.ts");
const mainEntry = join(root, "src/main.tsx");
const serverEntry = join(root, "src/entry-server.tsx");

// Nothing to hydrate without a server entry; the update seeds it in create mode.
if (!existsSync(serverEntry)) {
  out("manual", serverEntry, "missing: run a foundation update to seed the prerender entry, then re-run this patch");
  process.exit(0);
}

/** The index of the `]` that closes the `[` at `open`. */
const matchBracket = (code, open) => {
  let depth = 0;
  for (let i = open; i < code.length; i += 1) {
    if (code[i] === "[") depth += 1;
    else if (code[i] === "]") {
      depth -= 1;
      if (depth === 0) return i;
    }
  }
  return -1;
};

/* ── vite.config.ts: add the plugin ──────────────────────────────────── */

if (existsSync(viteConfig)) {
  let code = readFileSync(viteConfig, "utf8");
  if (code.includes("prerenderShell")) {
    // already adopted
  } else {
    const pluginsAt = code.indexOf("plugins:");
    const open = pluginsAt === -1 ? -1 : code.indexOf("[", pluginsAt);
    const close = open === -1 ? -1 : matchBracket(code, open);
    const reactImport = code.match(/^import react from ['"]@vitejs\/plugin-react['"];?\s*$/m);

    if (close === -1 || !reactImport) {
      out("manual", viteConfig, "add prerenderShell({ entry: 'src/entry-server.tsx', routes: ['/'], criticalCss: 'subset' }) to the plugins array (paint performance handover P1)");
    } else {
      const body = code.slice(open + 1, close);
      // Scaffold shape: react() and wyw() only. An app that has grown its own
      // plugins has grown routes whose first screen needs a human to confirm is
      // identical on both sides before it can be prerendered.
      const calls = body.match(/\b([A-Za-z_$][\w$]*)\s*\(/g) ?? [];
      const names = new Set(calls.map((c) => c.replace(/\s*\($/, "")));
      names.delete("react");
      names.delete("wyw");
      if (names.size > 0) {
        out("manual", viteConfig, `app-specific Vite plugins (${[...names].join(", ")}): add prerenderShell last in the plugins array and choose the routes whose first screen renders without a session (paint performance handover P1)`);
      } else {
        const semi = reactImport[0].trimEnd().endsWith(";") ? ";" : "";
        code =
          code.slice(0, close).replace(/,?\s*$/, "") +
          `,\n    // Paint before JavaScript: the built index.html carries the first screen's\n` +
          `    // markup and the CSS that markup uses, so the first paint needs neither\n` +
          `    // script nor a stylesheet round trip; main.tsx hydrates it. Add a route for\n` +
          `    // every page whose first screen renders without signed-in data.\n` +
          `    prerenderShell({ entry: 'src/entry-server.tsx', routes: ['/'], criticalCss: 'subset' }),\n  ` +
          code.slice(close);
        code = code.replace(
          reactImport[0],
          `${reactImport[0].trimEnd()}\nimport { prerenderShell } from '@ovasabi/frontend-kit/vite'${semi}`,
        );
        writeFileSync(viteConfig, code);
        out("patched", viteConfig, "prerenders the first screen at build time and inlines its critical CSS");
      }
    }
  }
}

/* ── main.tsx: hydrate what the build painted ────────────────────────── */

/**
 * `mountRoot` hydrates when the container already has children and
 * client-renders when it does not, so swapping it in ahead of the plugin is
 * normally a no-op. It is not a no-op when the app has hand-written markup
 * inside `#root` — pronto_v1 paints a splash screen there — because that markup
 * is not what React rendered, and hydrating it mismatches. Such an app has to
 * move the markup outside `#root` first.
 */
const rootHasMarkup = () => {
  const html = join(root, "index.html");
  if (!existsSync(html)) return false;
  const div = readFileSync(html, "utf8").match(/<div[^>]*id=["']root["'][^>]*>([\s\S]*?)<\/div>/);
  return Boolean(div && div[1].trim());
};

if (existsSync(mainEntry)) {
  let code = readFileSync(mainEntry, "utf8");
  if (!code.includes("mountRoot")) {
    // `createRoot(C).render(X)` and `mountRoot(C, X)` close the same number of
    // parens, so only the head of the call is rewritten.
    const mount = code.match(/(?:ReactDOM\.)?createRoot\(([\s\S]*?)\)\.render\(/);
    const namedImport = code.match(/^import \{\s*createRoot\s*\} from ['"]react-dom\/client['"];?\s*$/m);
    const defaultImport = code.match(/^import ReactDOM from ['"]react-dom\/client['"];?\s*$/m);
    const importLine = namedImport ?? defaultImport;

    // The default import can name more than the mount call, and dropping it
    // then would leave an undefined reference behind.
    const reactDomElsewhere = mount && /\bReactDOM\s*\./.test(code.replace(mount[0], ""));

    if (rootHasMarkup()) {
      out("manual", mainEntry, "index.html paints its own markup inside #root: move it outside the container before hydrating, or mountRoot will hydrate markup React never rendered (paint performance handover §7 trap 1)");
    } else if (!mount || !importLine) {
      out("manual", mainEntry, "replace the createRoot(...).render(...) call with mountRoot(container, element) from @ovasabi/frontend-kit (paint performance handover P1)");
    } else if (reactDomElsewhere) {
      out("manual", mainEntry, "ReactDOM is used beyond the mount call: swap the createRoot(...).render(...) call for mountRoot from @ovasabi/frontend-kit by hand and keep the existing import (paint performance handover P1)");
    } else {
      const semi = importLine[0].trimEnd().endsWith(";") ? ";" : "";
      code = code.replace(importLine[0], `import { mountRoot } from '@ovasabi/frontend-kit'${semi}`);
      code = code.replace(
        mount[0],
        `// Hydrates the markup prerenderShell wrote into index.html (see\n` +
          `// src/entry-server.tsx); client-renders when the root is empty, as in dev.\n` +
          `mountRoot(\n  ${mount[1].trim()},`,
      );
      writeFileSync(mainEntry, code);
      out("patched", mainEntry, "hydrates the prerendered markup instead of replacing it");
    }
  }
} else {
  out("manual", mainEntry, "no src/main.tsx: point the app's real entry at mountRoot from @ovasabi/frontend-kit (paint performance handover P1)");
}
