import { type CSSProperties, type HTMLAttributes, PropsWithChildren, useMemo } from "react";
import { ThemeProvider, createGlobalStyle, useTheme as useStyledTheme, type DefaultTheme } from "styled-components";

import type {
  DeepPartial,
  MinimalBreakpointTheme,
  MinimalSpaceTheme,
  MinimalTheme,
  ResolvedMinimalTheme,
} from "./types";

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

const mergeRecord = <T extends object>(base: T, override?: DeepPartial<T>): T => {
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

export const minimalThemeToCSSVariables = (theme: ResolvedMinimalTheme): Record<string, string | number> => {
  // Defensive: an application that builds its own theme literal against the
  // pre-`space` interface reaches this function through the global styles, and
  // a crash inside `createGlobalStyle` takes the whole page rather than one
  // token. Same reason `control` and `overlay` were made optional.
  const steps = theme.space ?? space;
  const stops = theme.breakpoint ?? breakpoint;
  return {
  "--minimal-bg-app": theme.color.bgApp,
  "--minimal-bg-surface": theme.color.bgSurface,
  "--minimal-bg-surface-alt": theme.color.bgSurfaceAlt,
  "--minimal-bg-surface-hover": theme.color.bgSurfaceHover,
  "--minimal-bg-elevated": theme.color.bgElevated,
  "--minimal-bg-overlay": theme.color.bgOverlay,
  "--minimal-text-primary": theme.color.textPrimary,
  "--minimal-text-secondary": theme.color.textSecondary,
  "--minimal-text-tertiary": theme.color.textTertiary,
  "--minimal-text-inverse": theme.color.textInverse,
  "--minimal-border-subtle": theme.color.borderSubtle,
  "--minimal-border-strong": theme.color.borderStrong,
  "--minimal-border-focus": theme.color.borderFocus,
  "--minimal-color-brand": theme.color.brand,
  "--minimal-color-brand-soft": theme.color.brandSoft,
  "--minimal-color-success": theme.color.success,
  "--minimal-color-success-soft": theme.color.successSoft,
  "--minimal-color-warning": theme.color.warning,
  "--minimal-color-warning-soft": theme.color.warningSoft,
  "--minimal-color-danger": theme.color.danger,
  "--minimal-color-danger-soft": theme.color.dangerSoft,
  "--minimal-color-info": theme.color.info,
  "--minimal-color-info-soft": theme.color.infoSoft,
  "--minimal-space-3xs": steps["3xs"],
  "--minimal-space-2xs": steps["2xs"],
  "--minimal-space-xs": steps.xs,
  "--minimal-space-sm": steps.sm,
  "--minimal-space-md": steps.md,
  "--minimal-space-lg": steps.lg,
  "--minimal-space-xl": steps.xl,
  "--minimal-space-2xl": steps["2xl"],
  "--minimal-space-3xl": steps["3xl"],
  /*
   * The deprecated names, under their own prefix.
   *
   * They cannot share `--minimal-space-*` with the scale above: the old names
   * are offset by one rung from the new ones, so `--minimal-space-md` would
   * have had to mean 24px to a stylesheet and 16px to TypeScript. A separate
   * prefix says which system a declaration belongs to, and makes the remaining
   * uses greppable when they are ready to be retired.
   */
  "--minimal-spacing-xs": theme.spacing.xs,
  "--minimal-spacing-sm": theme.spacing.sm,
  "--minimal-spacing-md": theme.spacing.md,
  "--minimal-spacing-lg": theme.spacing.lg,
  "--minimal-spacing-xl": theme.spacing.xl,
  "--minimal-spacing-2xl": theme.spacing["2xl"],
  "--minimal-bp-hand": stops.hand,
  "--minimal-bp-page": stops.page,
  "--minimal-bp-desk": stops.desk,
  "--minimal-bp-wide": stops.wide,
  "--minimal-radius-sm": theme.radius.sm,
  "--minimal-radius-md": theme.radius.md,
  "--minimal-radius-lg": theme.radius.lg,
  "--minimal-radius-xl": theme.radius.xl,
  "--minimal-radius-pill": theme.radius.pill,
  "--minimal-shadow-subtle": theme.shadow.subtle,
  "--minimal-shadow-medium": theme.shadow.medium,
  "--minimal-shadow-floating": theme.shadow.floating,
  "--minimal-focus-ring-width": theme.focus.ringWidth,
  "--minimal-control-min-target": theme.control.minTargetSize,
  "--minimal-control-height-sm": theme.control.height.sm,
  "--minimal-control-height-md": theme.control.height.md,
  "--minimal-control-height-lg": theme.control.height.lg,
  "--minimal-control-icon-size": theme.control.iconSize,
  "--minimal-overlay-viewport-gutter": theme.overlay.viewportGutter,
  "--minimal-overlay-anchored-offset": theme.overlay.anchoredOffset,
  "--minimal-overlay-max-height": theme.overlay.maxHeight,
  "--minimal-font-display-family": theme.typography.displayFamily,
  "--minimal-font-body-family": theme.typography.bodyFamily,
  "--minimal-font-mono-family": theme.typography.monoFamily,
  "--minimal-font-display-size": theme.typography.displaySize,
  "--minimal-font-h1-size": theme.typography.h1Size,
  "--minimal-font-h2-size": theme.typography.h2Size,
  "--minimal-font-body-size": theme.typography.bodySize,
  "--minimal-font-caption-size": theme.typography.captionSize,
  "--minimal-font-meta-size": theme.typography.metaSize,
  "--minimal-font-weight-regular": theme.typography.weightRegular,
  "--minimal-font-weight-medium": theme.typography.weightMedium,
  "--minimal-font-weight-semibold": theme.typography.weightSemibold,
  "--minimal-font-weight-bold": theme.typography.weightBold,
  "--minimal-line-height-tight": theme.typography.lineHeightTight,
  "--minimal-line-height-body": theme.typography.lineHeightBody,
  "--minimal-motion-micro": `${theme.motion.microDuration}s`,
  "--minimal-motion-standard": `${theme.motion.standardDuration}s`,
  "--minimal-motion-slow": `${theme.motion.slowDuration}s`,
  "--minimal-z-base": theme.zIndex.base,
  "--minimal-z-sticky": theme.zIndex.sticky,
  "--minimal-z-dock": theme.zIndex.dock,
  "--minimal-z-global-header": theme.zIndex.globalHeader,
  "--minimal-z-dropdown": theme.zIndex.dropdown,
  "--minimal-z-overlay": theme.zIndex.overlay,
  "--minimal-z-modal": theme.zIndex.modal,
  "--minimal-z-tooltip": theme.zIndex.tooltip,
  };
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

const GlobalStyles = createGlobalStyle`
  :root {
    color-scheme: ${({ theme }) => theme.colorScheme ?? "light"};
    ${({ theme }) =>
      Object.entries(minimalThemeToCSSVariables(theme as ResolvedMinimalTheme)).map(([key, value]) => `${key}: ${value};`).join("\n")}
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
    background: ${({ theme }) => theme.color.bgApp};
    color: ${({ theme }) => theme.color.textPrimary};
    font-family: ${({ theme }) => theme.typography.bodyFamily};
    font-size: ${({ theme }) => theme.typography.bodySize};
    line-height: ${({ theme }) => theme.typography.lineHeightBody};
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
    background: ${({ theme }) => theme.color.brandSoft};
    color: ${({ theme }) => theme.color.textPrimary};
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
`;

export const MinimalGlobalStyles = () => <GlobalStyles />;

export const MinimalThemeProvider = ({
  theme,
  children,
}: PropsWithChildren<{ theme?: DeepPartial<MinimalTheme> }>) => {
  const mergedTheme = useMemo(() => createMinimalTheme(theme), [theme]);
  return <ThemeProvider theme={mergedTheme as unknown as DefaultTheme}>{children}</ThemeProvider>;
};

export interface MinimalThemeScopeProps extends HTMLAttributes<HTMLDivElement> {
  themeOverride?: DeepPartial<MinimalTheme>;
}

/**
 * Applies a nested token override to both styled-components and CSS-variable
 * consumers. Use this for edition panels, embedded widgets, and previews that
 * must not rewrite the document-level `:root` variables.
 */
export const MinimalThemeScope = ({
  themeOverride,
  children,
  style,
  ...props
}: PropsWithChildren<MinimalThemeScopeProps>) => {
  const parentTheme = useMinimalTheme();
  const scopedTheme = useMemo(
    () => mergeRecord(parentTheme, themeOverride as DeepPartial<ResolvedMinimalTheme>),
    [parentTheme, themeOverride]
  );
  const variables = minimalThemeToCSSVariables(scopedTheme);

  return (
    <ThemeProvider theme={scopedTheme as unknown as DefaultTheme}>
      <div
        data-minimal-theme-scope={scopedTheme.name}
        style={{ ...variables, colorScheme: scopedTheme.colorScheme, ...style } as CSSProperties}
        {...props}
      >
        {children}
      </div>
    </ThemeProvider>
  );
};

export const useMinimalTheme = (): ResolvedMinimalTheme => {
  const theme = useStyledTheme() as ResolvedMinimalTheme | undefined;
  if (theme && typeof theme.name === "string") {
    return theme;
  }
  return minimalBaseTheme;
};
