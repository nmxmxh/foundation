/*
 * ui-minimal's design tokens, with no React and no styling runtime.
 *
 * Everything here is plain data and pure functions: the base theme, theme
 * merging, the CSS-variable map, `minimalVars` (each token as a `var()` with its
 * base-theme fallback) and the breakpoint helpers. That is what a build-time
 * CSS extractor (Linaria / wyw-in-js, research doc sections 8 and 14.7) has to
 * evaluate, and evaluating `theme.tsx` instead pulled React and
 * styled-components into the build. Import tokens from here, or from
 * `@ovasabi/ui-minimal/tokens`, anywhere that should stay runtime-free.
 */
import type {
  DeepPartial,
  MinimalBreakpointTheme,
  MinimalSpaceTheme,
  MinimalTheme,
  ResolvedMinimalTheme,
} from "./types.ts";

const isPlainObject = (value: unknown): value is object =>
  typeof value === "object" && value !== null && !Array.isArray(value);

/**
 * The spacing steps. Static, on the 8px grid, at a ratio near 1.6.
 *
 * Ratios, rung to rung: 2.0, 2.0, 2.0, 1.5, 1.67, 1.6, 1.63, 1.62. A reader
 * parses a step as *deliberate* somewhere around 1.6; below about 1.5 it reads
 * as drift. The scale this replaces had `md` and `lg` at 1.4-1.5 apart, which
 * is why six tokens only ever produced four distinguishable spacings.
 *
 * Declared separately from the theme so the deprecated `spacing` names can be
 * resolved from it rather than restated beside it. See `MinimalSpaceTheme`.
 */
const space: MinimalSpaceTheme = {
  "3xs": "2px",
  "2xs": "4px",
  xs: "8px",
  sm: "16px",
  md: "24px",
  lg: "40px",
  xl: "64px",
  "2xl": "104px",
  "3xl": "168px",
};

/**
 * Named layout thresholds, in `rem` so they follow the user's own text size.
 *
 * A viewer who has set a larger default font gets the wider layout later, at
 * the point their text actually needs the room — which is what a breakpoint in
 * `px` silently refuses to do.
 */
const breakpoint: MinimalBreakpointTheme = {
  hand: "30rem",
  page: "48rem",
  desk: "64rem",
  wide: "90rem",
};

export const minimalBaseTheme: ResolvedMinimalTheme = {
  name: "ovasabi-minimal",
  colorScheme: "light",
  color: {
    bgApp: "#faf9f6",
    bgSurface: "#ffffff",
    bgSurfaceAlt: "#f5f4ef",
    bgSurfaceHover: "#ece9e1",
    bgElevated: "#fffdf9",
    bgOverlay: "rgba(28, 28, 30, 0.56)",
    textPrimary: "#1c1c1e",
    textSecondary: "#5f6268",
    textTertiary: "#85888f",
    textInverse: "#faf9f6",
    borderSubtle: "#e5e2d8",
    borderStrong: "#cbc7ba",
    borderFocus: "#2b303b",
    brand: "#2b303b",
    brandSoft: "rgba(43, 48, 59, 0.12)",
    success: "#238c52",
    successSoft: "rgba(35, 140, 82, 0.12)",
    warning: "#f2a900",
    warningSoft: "rgba(242, 169, 0, 0.16)",
    danger: "#e33e47",
    dangerSoft: "rgba(227, 62, 71, 0.12)",
    info: "#3b82f6",
    infoSoft: "rgba(59, 130, 246, 0.12)",
  },
  space,
  breakpoint,
  /*
   * Deprecated. Each old name resolves to the step nearest the size it used to
   * produce, not to the step of the same name: matching by name would have put
   * `spacing.md` at 24px where it used to render 12-20px, roughly doubling the
   * air in every layout already written against it. Matching by size keeps
   * those layouts recognisable while moving them onto the real scale.
   */
  spacing: {
    xs: space["2xs"],
    sm: space.xs,
    md: space.sm,
    lg: space.md,
    xl: space.lg,
    "2xl": space.xl,
  },
  radius: {
    sm: "6px",
    md: "12px",
    lg: "18px",
    xl: "24px",
    pill: "999px",
  },
  shadow: {
    subtle: "0 10px 28px -18px rgba(28, 28, 30, 0.22)",
    medium: "0 18px 48px -22px rgba(28, 28, 30, 0.26)",
    floating: "0 28px 80px -28px rgba(28, 28, 30, 0.34)",
  },
  focus: {
    ringWidth: "3px",
  },
  control: {
    minTargetSize: "44px",
    height: {
      sm: "36px",
      md: "44px",
      lg: "52px",
    },
    iconSize: "20px",
  },
  overlay: {
    viewportGutter: "16px",
    anchoredOffset: "8px",
    maxHeight: "min(70dvh, 480px)",
  },
  typography: {
    displayFamily: "\"Fraunces\", Georgia, serif",
    bodyFamily: "\"Instrument Sans\", \"Inter\", -apple-system, BlinkMacSystemFont, \"Segoe UI\", sans-serif",
    monoFamily: "\"IBM Plex Mono\", \"SFMono-Regular\", monospace",
    weightRegular: 400,
    weightMedium: 500,
    weightSemibold: 600,
    weightBold: 700,
    // Fluid scale, but floored so small-screen sizes stay legible in dense app
    // UIs (the old floors collapsed h1 to 1.25rem / body to 0.875rem on phones,
    // which every serious app ended up overriding — see chowdash theme.ts).
    displaySize: "clamp(2rem, 4vw, 3rem)",
    h1Size: "clamp(1.5rem, 2.5vw, 1.75rem)",
    h2Size: "clamp(1.125rem, 2vw, 1.25rem)",
    bodySize: "clamp(0.9375rem, 1.5vw, 1rem)",
    captionSize: "clamp(0.8125rem, 1vw, 0.875rem)",
    metaSize: "clamp(0.6875rem, 0.8vw, 0.75rem)",
    lineHeightTight: 1.15,
    lineHeightBody: 1.55,
  },
  motion: {
    microDuration: 0.18,
    standardDuration: 0.3,
    slowDuration: 0.5,
    standardEase: [0.4, 0, 0.2, 1],
    entranceEase: [0, 0, 0.2, 1],
    exitEase: [0.4, 0, 1, 1],
    springStiffness: 320,
    springDamping: 28,
    hoverLift: -2,
    pageOffset: 10,
  },
  zIndex: {
    base: 1,
    sticky: 10,
    dock: 50,
    globalHeader: 100,
    dropdown: 200,
    overlay: 300,
    modal: 301,
    tooltip: 400,
  },
};

export const mergeRecord = <T extends object>(base: T, override?: DeepPartial<T>): T => {
  if (!override) {
    return { ...base };
  }

  const next = { ...base } as T;
  for (const key of Object.keys(override) as Array<keyof T>) {
    const overrideValue = override[key];
    if (overrideValue === undefined) {
      continue;
    }
    const baseValue = base[key];
    if (isPlainObject(baseValue) && isPlainObject(overrideValue)) {
      next[key] = mergeRecord(baseValue, overrideValue as DeepPartial<typeof baseValue>) as T[keyof T];
      continue;
    }
    next[key] = overrideValue as T[keyof T];
  }
  return next;
};

export const createMinimalTheme = (overrides?: DeepPartial<MinimalTheme>): ResolvedMinimalTheme =>
  mergeRecord(minimalBaseTheme, overrides as DeepPartial<ResolvedMinimalTheme>);

/**
 * Every theme token that reaches CSS, by the custom property that carries it.
 *
 * This one table is the only place a variable name is written. The `:root`
 * declarations (`minimalThemeToCSSVariables`) and the references components use
 * (`minimalVars`) are both derived from it, so a stylesheet can never read a
 * name that nothing sets.
 *
 * The deprecated `spacing` names keep their own `--minimal-spacing-*` prefix:
 * they are offset by one rung from `space`, so sharing `--minimal-space-md`
 * would have meant 24px to a stylesheet and 16px to TypeScript. A separate
 * prefix says which system a declaration belongs to, and makes the remaining
 * uses greppable when they are ready to be retired.
 */
const cssVariableNames = {
  color: {
    bgApp: "--minimal-bg-app",
    bgSurface: "--minimal-bg-surface",
    bgSurfaceAlt: "--minimal-bg-surface-alt",
    bgSurfaceHover: "--minimal-bg-surface-hover",
    bgElevated: "--minimal-bg-elevated",
    bgOverlay: "--minimal-bg-overlay",
    textPrimary: "--minimal-text-primary",
    textSecondary: "--minimal-text-secondary",
    textTertiary: "--minimal-text-tertiary",
    textInverse: "--minimal-text-inverse",
    borderSubtle: "--minimal-border-subtle",
    borderStrong: "--minimal-border-strong",
    borderFocus: "--minimal-border-focus",
    brand: "--minimal-color-brand",
    brandSoft: "--minimal-color-brand-soft",
    success: "--minimal-color-success",
    successSoft: "--minimal-color-success-soft",
    warning: "--minimal-color-warning",
    warningSoft: "--minimal-color-warning-soft",
    danger: "--minimal-color-danger",
    dangerSoft: "--minimal-color-danger-soft",
    info: "--minimal-color-info",
    infoSoft: "--minimal-color-info-soft",
  },
  space: {
    "3xs": "--minimal-space-3xs",
    "2xs": "--minimal-space-2xs",
    xs: "--minimal-space-xs",
    sm: "--minimal-space-sm",
    md: "--minimal-space-md",
    lg: "--minimal-space-lg",
    xl: "--minimal-space-xl",
    "2xl": "--minimal-space-2xl",
    "3xl": "--minimal-space-3xl",
  },
  spacing: {
    xs: "--minimal-spacing-xs",
    sm: "--minimal-spacing-sm",
    md: "--minimal-spacing-md",
    lg: "--minimal-spacing-lg",
    xl: "--minimal-spacing-xl",
    "2xl": "--minimal-spacing-2xl",
  },
  breakpoint: {
    hand: "--minimal-bp-hand",
    page: "--minimal-bp-page",
    desk: "--minimal-bp-desk",
    wide: "--minimal-bp-wide",
  },
  radius: {
    sm: "--minimal-radius-sm",
    md: "--minimal-radius-md",
    lg: "--minimal-radius-lg",
    xl: "--minimal-radius-xl",
    pill: "--minimal-radius-pill",
  },
  shadow: {
    subtle: "--minimal-shadow-subtle",
    medium: "--minimal-shadow-medium",
    floating: "--minimal-shadow-floating",
  },
  focus: {
    ringWidth: "--minimal-focus-ring-width",
  },
  control: {
    minTargetSize: "--minimal-control-min-target",
    height: {
      sm: "--minimal-control-height-sm",
      md: "--minimal-control-height-md",
      lg: "--minimal-control-height-lg",
    },
    iconSize: "--minimal-control-icon-size",
  },
  overlay: {
    viewportGutter: "--minimal-overlay-viewport-gutter",
    anchoredOffset: "--minimal-overlay-anchored-offset",
    maxHeight: "--minimal-overlay-max-height",
  },
  typography: {
    displayFamily: "--minimal-font-display-family",
    bodyFamily: "--minimal-font-body-family",
    monoFamily: "--minimal-font-mono-family",
    displaySize: "--minimal-font-display-size",
    h1Size: "--minimal-font-h1-size",
    h2Size: "--minimal-font-h2-size",
    bodySize: "--minimal-font-body-size",
    captionSize: "--minimal-font-caption-size",
    metaSize: "--minimal-font-meta-size",
    weightRegular: "--minimal-font-weight-regular",
    weightMedium: "--minimal-font-weight-medium",
    weightSemibold: "--minimal-font-weight-semibold",
    weightBold: "--minimal-font-weight-bold",
    lineHeightTight: "--minimal-line-height-tight",
    lineHeightBody: "--minimal-line-height-body",
  },
  zIndex: {
    base: "--minimal-z-base",
    sticky: "--minimal-z-sticky",
    dock: "--minimal-z-dock",
    globalHeader: "--minimal-z-global-header",
    dropdown: "--minimal-z-dropdown",
    overlay: "--minimal-z-overlay",
    modal: "--minimal-z-modal",
    tooltip: "--minimal-z-tooltip",
  },
} as const;

/**
 * Motion is the one group whose CSS form is not its theme form: durations are
 * numeric seconds in the theme and need a unit here, and easings are bezier
 * tuples that CSS spells as `cubic-bezier()`. So its variables are named, not
 * mirrored, and `minimalVars.motion` uses CSS-shaped keys that cannot be
 * mistaken for the numeric theme fields.
 */
const motionVariableNames = {
  micro: "--minimal-motion-micro",
  standard: "--minimal-motion-standard",
  slow: "--minimal-motion-slow",
  easeStandard: "--minimal-ease-standard",
  easeEntrance: "--minimal-ease-entrance",
  easeExit: "--minimal-ease-exit",
} as const;

type MotionCSSValues = Record<keyof typeof motionVariableNames, string>;

const cubicBezier = (ease: readonly number[]): string => `cubic-bezier(${ease.join(", ")})`;

const motionCSSValues = (motion: ResolvedMinimalTheme["motion"] | undefined): MotionCSSValues => {
  const m = motion ?? minimalBaseTheme.motion;
  return {
    micro: `${m.microDuration}s`,
    standard: `${m.standardDuration}s`,
    slow: `${m.slowDuration}s`,
    easeStandard: cubicBezier(m.standardEase),
    easeEntrance: cubicBezier(m.entranceEase),
    easeExit: cubicBezier(m.exitEase),
  };
};

type VariableTree = { readonly [key: string]: string | VariableTree };
type TokenTree = { readonly [key: string]: unknown };

type VarRefs<T> = { readonly [K in keyof T]: T[K] extends string ? string : VarRefs<T[K]> };

/** `minimalVars`: the theme's shape, with each leaf a `var()` reference. */
export type MinimalThemeVars = VarRefs<typeof cssVariableNames> & {
  readonly motion: Readonly<MotionCSSValues>;
};

const childOf = (tree: unknown, key: string): TokenTree | undefined =>
  isPlainObject(tree) ? ((tree as TokenTree)[key] as TokenTree | undefined) : undefined;

const collectVariables = (
  names: VariableTree,
  values: unknown,
  fallback: unknown,
  out: Record<string, string | number>
) => {
  for (const key of Object.keys(names)) {
    const name = names[key];
    if (typeof name === "string") {
      const value = childOf(values, key) ?? childOf(fallback, key);
      if (value !== undefined) {
        out[name] = value as unknown as string | number;
      }
      continue;
    }
    collectVariables(name, childOf(values, key), childOf(fallback, key), out);
  }
};

const buildVarRefs = (names: VariableTree, fallback: unknown): Record<string, unknown> => {
  const refs: Record<string, unknown> = {};
  for (const key of Object.keys(names)) {
    const name = names[key];
    refs[key] =
      typeof name === "string"
        ? `var(${name}, ${String(childOf(fallback, key))})`
        : buildVarRefs(name, childOf(fallback, key));
  }
  return refs;
};

/**
 * The theme as CSS custom properties, for `:root` or a scoped element.
 *
 * Defensive by construction: a token the theme lacks falls back to the base
 * theme's value. An application that builds its own theme literal against an
 * older interface reaches this function through the global styles, and a
 * crash inside `createGlobalStyle` takes the whole page rather than one token.
 */
export const minimalThemeToCSSVariables = (theme: ResolvedMinimalTheme): Record<string, string | number> => {
  const variables: Record<string, string | number> = {};
  collectVariables(cssVariableNames, theme, minimalBaseTheme, variables);
  const motion = motionCSSValues(theme.motion);
  for (const key of Object.keys(motionVariableNames) as Array<keyof typeof motionVariableNames>) {
    variables[motionVariableNames[key]] = motion[key];
  }
  return variables;
};

/**
 * Theme tokens as CSS variable references, for use inside styled templates.
 *
 * ```ts
 * const Panel = styled.div`
 *   background: ${minimalVars.color.bgSurface};
 *   padding: ${minimalVars.space.sm};
 * `;
 * ```
 *
 * Why not `${({ theme }) => theme.color.bgSurface}`: a function interpolation
 * makes the template dynamic, so styled-components evaluates it on every
 * render and a theme change regenerates a class for every component on the
 * page. A `var()` reference is a static string. The rule is built once, and a
 * theme change rewrites only the variables on `:root` (or on a
 * `MinimalThemeScope`), which the browser restyles without any JavaScript.
 * Static templates are also what a build-time extractor needs.
 *
 * Each reference carries the base theme's value as its fallback, so a
 * component rendered without `MinimalGlobalStyles` (a test, an embed) still
 * looks like the base theme instead of losing its colours.
 *
 * Consequence: a nested `MinimalThemeProvider` alone no longer restyles
 * components beneath it, because it sets no variables. Nested overrides go
 * through `MinimalThemeScope`, which sets both.
 */
export const minimalVars: MinimalThemeVars = {
  ...(buildVarRefs(cssVariableNames, minimalBaseTheme) as VarRefs<typeof cssVariableNames>),
  motion: (() => {
    const fallback = motionCSSValues(minimalBaseTheme.motion);
    const refs = {} as Record<keyof typeof motionVariableNames, string>;
    for (const key of Object.keys(motionVariableNames) as Array<keyof typeof motionVariableNames>) {
      refs[key] = `var(${motionVariableNames[key]}, ${fallback[key]})`;
    }
    return refs;
  })(),
};

/**
 * A mobile-first media query at a named threshold.
 *
 * `min-width`, always. Every media query in the system before this was
 * `max-width`, which means each layout was defined by *subtraction* — the
 * desktop arrangement with values taken away — and combined with a spacing
 * scale that floored on small screens, that is exactly why the phone view read
 * as cramped rather than as composed. Building up from the small screen states
 * the small screen's design first and adds to it.
 *
 * ```ts
 * const Grid = styled.div`
 *   display: grid;
 *   ${from("page")} { grid-template-columns: 1fr 1fr; }
 * `;
 * ```
 */
export const from = (stop: keyof MinimalBreakpointTheme, theme?: ResolvedMinimalTheme): string =>
  `@media (min-width: ${(theme?.breakpoint ?? breakpoint)[stop]})`;

/**
 * The exception: a rule that applies *below* a threshold.
 *
 * Reach for it only where the small screen genuinely needs something the large
 * one must not have — phone-only chrome, a dock that becomes a sidebar — never
 * to undo a desktop rule. The `calc()` is not decoration: `max-width: 48rem`
 * and `min-width: 48rem` both match at exactly 48rem, so a pair written the
 * obvious way applies both rules on that one width.
 */
export const until = (stop: keyof MinimalBreakpointTheme, theme?: ResolvedMinimalTheme): string =>
  `@media (max-width: calc(${(theme?.breakpoint ?? breakpoint)[stop]} - 0.02px))`;
