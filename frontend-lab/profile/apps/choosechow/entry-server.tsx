/**
 * Lab-only server entry for ChooseChow's signed-out landing page — the same
 * tree as chowdash_rider_v1/frontend/src/main.tsx, rendered at build time.
 * App modules are reached through the app's own `@/` alias, and bare imports
 * resolve through its aliases (see buildPrerender.mjs), so this file changes
 * nothing in the app.
 *
 * styled-components still styles most of the app: its styles are collected
 * during render and returned for the document head, where the client-side
 * library adopts them on hydration.
 */
import { StrictMode } from "react";
import { renderToString } from "react-dom/server";
import { StaticRouter } from "react-router-dom";
import { ServerStyleSheet } from "styled-components";
import App from "@/App";
import { EditionProvider } from "@/edition/EditionProvider";
import { FontFaces } from "@/styles/fonts";

export function render(url: string): { html: string; head: string } {
  const sheet = new ServerStyleSheet();
  try {
    const html = renderToString(
      sheet.collectStyles(
        <StrictMode>
          <FontFaces />
          <StaticRouter location={url}>
            <EditionProvider>
              <App />
            </EditionProvider>
          </StaticRouter>
        </StrictMode>,
      ),
    );
    return { html, head: sheet.getStyleTags() };
  } finally {
    sheet.seal();
  }
}
