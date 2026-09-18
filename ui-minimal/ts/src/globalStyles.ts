import { css } from "@linaria/core";

import type { ResolvedMinimalTheme } from "./types.ts";
import { minimalBaseTheme, minimalThemeToCSSVariables, minimalVars } from "./tokens.ts";

/*
 * The global stylesheet as a runtime-free module: Linaria evaluates this file at
 * build time, and an app building a vendored kit cannot resolve React from the
 * kit's real path (ChooseChow pilot, 2026-09-15). No React imports here.
 */

export const declarations = (theme: ResolvedMinimalTheme) =>
  Object.entries(minimalThemeToCSSVariables(theme))
    .map(([key, value]) => `${key}: ${value};`)
    .join("\n");

export const BASE_DECLARATIONS = declarations(minimalBaseTheme);

/*
 * The global stylesheet, extracted at build time.
 *
 * It used to be a styled-components `createGlobalStyle` that rebuilt and
 * re-injected this whole block on the main thread whenever the theme object
 * changed identity. Now the base theme's variables, the quality tiers and the
 * reset are a static file the browser can parse before any script runs — the
 * precondition for painting before JavaScript (research doc section 15.4).
 * A non-base theme adds one small `<style>` with only its own variables
 * (`MinimalGlobalStyles`), so the default path injects nothing at runtime.
 *
 * Referenced by nothing but its module: importing ui-minimal includes it.
 */
export const minimalGlobalStylesClass = css`
  :global() {
    :root {
      color-scheme: ${minimalBaseTheme.colorScheme ?? "light"};
      ${BASE_DECLARATIONS}
    }

    /*
     * Quality tiers: data-ui-tier, written by browser-host's createUiQuality.
     *
     * Detail only. Every ui-minimal surface reads these tokens, so this block is
     * the whole swap.
     *
     * ## Why the cheap tiers use ZERO blur, not a small blur
     *
     * Measured on a device (Android 16 WebView 133, ChooseChow Orders, per-layer
     * paint replay via LayerTree.profileSnapshot; research doc §13 finding 12):
     * a blurred shadow behind a ROUNDED rect costs ~25 ms of raster per screen,
     * while the same shadow on a square rect costs ~1 ms — the rounded case
     * loses Skia's fast path, and every shadow layer pays it again. Blur radius
     * adds on top of that penalty, but it is not the penalty:
     *
     *   two-layer (the ChooseChow card)  68 ms      20px blur        41 ms
     *   8px blur                         32 ms      3px blur         30 ms
     *   1px blur                         53 ms      0 blur hairline   6.9 ms
     *   no shadow                         5.8 ms    no shadow+border  5.2 ms
     *
     * So "a few pixels of blur instead of 28" — what this block used to do — was
     * still paying the whole penalty (30 ms against 6.9). The cheap tiers now
     * draw elevation with an unblurred offset edge, which keeps a visible edge
     * on every surface and stays on the fast path. Rounded corners themselves
     * are free and are untouched.
     *
     * A MinimalThemeScope writes its tokens inline on its own element, which wins
     * over :root for that subtree; scopes are small embeds, and that is accepted.
     */
    :root[data-ui-tier="low_power"] {
      --minimal-shadow-subtle: 0 1px 0 rgba(28, 28, 30, 0.16);
      --minimal-shadow-medium: 0 1px 0 rgba(28, 28, 30, 0.22);
      --minimal-shadow-floating: 0 2px 0 rgba(28, 28, 30, 0.26);
      --minimal-backdrop-filter: none;
    }

    /*
     * balanced: the rung most mid-range phones sit on. It keeps blur where a
     * design reads as flat without it, but collapses multi-layer shadows to one
     * layer — measured 68 ms → 32 ms on the same screen, since each additional
     * shadow layer repeats the rounded-rect penalty above.
     */
    :root[data-ui-tier="balanced"] {
      --minimal-shadow-subtle: 0 2px 6px rgba(28, 28, 30, 0.1);
      --minimal-shadow-medium: 0 3px 10px rgba(28, 28, 30, 0.14);
      --minimal-shadow-floating: 0 6px 16px rgba(28, 28, 30, 0.18);
    }

    :root[data-ui-tier="reduced_motion"] {
      --minimal-backdrop-filter: none;
    }

    *,
    *::before,
    *::after {
      box-sizing: border-box;
    }

    body {
      margin: 0;
      min-width: 320px;
      min-height: 100dvh;
      position: relative;
      background: ${minimalVars.color.bgApp};
      color: ${minimalVars.color.textPrimary};
      font-family: ${minimalVars.typography.bodyFamily};
      font-size: ${minimalVars.typography.bodySize};
      line-height: ${minimalVars.typography.lineHeightBody};
      -webkit-font-smoothing: antialiased;
      -moz-osx-font-smoothing: grayscale;
    }

    #root {
      isolation: isolate;
      min-height: 100dvh;
    }

    button,
    input,
    select,
    textarea {
      font: inherit;
    }

    ::selection {
      background: ${minimalVars.color.brandSoft};
      color: ${minimalVars.color.textPrimary};
    }

    [data-theme-switching] *,
    [data-theme-switching] *::before,
    [data-theme-switching] *::after {
      transition: none !important;
    }

    @media (prefers-reduced-motion: reduce) {
      *,
      *::before,
      *::after {
        animation-duration: 0.01ms !important;
        animation-iteration-count: 1 !important;
        scroll-behavior: auto !important;
        transition-duration: 0.01ms !important;
      }
    }

    @media (forced-colors: active) {
      :focus-visible {
        outline: 2px solid CanvasText !important;
        outline-offset: 2px;
      }
    }
  }
`;

