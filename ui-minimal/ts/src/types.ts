export type DeepPartial<T> = {
  [P in keyof T]?: T[P] extends object
    ? T[P] extends any[]
      ? T[P]
      : DeepPartial<T[P]>
    : T[P];
};

export type MinimalTone = "neutral" | "brand" | "info" | "success" | "warning" | "danger";
export type MinimalEmphasis = "soft" | "solid" | "outline";
export type MinimalSize = "sm" | "md" | "lg";
export type MinimalDensity = "compact" | "comfortable" | "relaxed";

export interface MinimalColorTheme {
  bgApp: string;
  bgSurface: string;
  bgSurfaceAlt: string;
  bgSurfaceHover: string;
  bgElevated: string;
  bgOverlay: string;
  textPrimary: string;
  textSecondary: string;
  textTertiary: string;
  textInverse: string;
  borderSubtle: string;
  borderStrong: string;
  borderFocus: string;
  brand: string;
  brandSoft: string;
  success: string;
  successSoft: string;
  warning: string;
  warningSoft: string;
  danger: string;
  dangerSoft: string;
  info: string;
  infoSoft: string;
}

/**
 * The spacing scale. Static steps on the 8px grid, at a ratio near 1.6.
 *
 * Static, not fluid, and that is the whole point. The scale this replaces was
 * six `clamp()` ranges, which sounds adaptive and is not: every token pinned to
 * its floor below ~400px and to its ceiling above ~1000px, so a phone and a
 * desktop each got a *fixed* scale and the `vw` term only did anything in the
 * band between them. What the ranges did accomplish was to squeeze the steps
 * together — `md` and `lg` resolved to 12px and 18px on a phone, a ratio of
 * 1.5 — and two spacings six pixels apart do not read as two categories of
 * relationship. They read as inconsistency. Six tokens resolving to four
 * distinguishable values is why layouts built on them looked uniform no matter
 * which property placed them.
 *
 * The steps here are far enough apart to read as decisions: every adjacent
 * pair is at least 1.5x the one below it. `styling_design_practices.md` §10.3
 * specified exactly this and the theme had never matched it.
 *
 * ## Small screens get fewer steps, not smaller ones
 *
 * A phone renders a section boundary at `lg` where a desktop renders `xl` — a
 * rung down the same scale. It does not render `xl` at 64% of itself. Scaling
 * every token by a proportion preserves the ratios and destroys the
 * *distinctions*, which is the uniformity this scale exists to end. Selecting
 * a different rung preserves the distinctions and loses only the largest one,
 * which is the right thing to lose on a 390px screen.
 *
 * `clamp()` stays where it belongs: typography, where the scaling is genuinely
 * continuous and no reader can perceive a step boundary.
 */
export interface MinimalSpaceTheme {
  /** 2px — hairline offsets and optical nudges. */
  "3xs": string;
  /** 4px */
  "2xs": string;
  /** 8px */
  xs: string;
  /** 16px */
  sm: string;
  /** 24px */
  md: string;
  /** 40px */
  lg: string;
  /** 64px */
  xl: string;
  /** 104px */
  "2xl": string;
  /** 168px — full section boundaries at desktop width. */
  "3xl": string;
}

/**
 * The previous scale's names, resolved onto the static steps.
 *
 * @deprecated Read `theme.space`. These six names are a compatibility shim for
 * code written against the fluid `clamp()` scale; each maps to the nearest step
 * on the new one, so a layout keeps roughly the air it had without keeping the
 * ranges. Note that the names are offset by design — `spacing.md` is
 * `space.sm` — because matching by name instead of by size would have roughly
 * doubled every existing layout's spacing.
 */
export interface MinimalSpacingTheme {
  xs: string;
  sm: string;
  md: string;
  lg: string;
  xl: string;
  "2xl": string;
}

/**
 * Named layout thresholds.
 *
 * Named for what the layout does at each one, not for a device — there is no
 * "tablet" width and there never was. Before these existed every media query
 * in the system was a hand-typed literal, nine distinct values across the app
 * and foundation, none of them shared. The concrete cost of that was a dead
 * band: a grid that collapsed at 900px sitting inside a container that kept
 * its wide padding until 640px, so every tablet in portrait and every large
 * phone in landscape rendered a stacked layout inside desktop margins. Nobody
 * chose it. It is what independent literals produce.
 *
 * Used through `from()`, which emits `min-width` — layouts are built up from
 * the small screen, not subtracted down from the large one.
 */
export interface MinimalBreakpointTheme {
  /** 30rem / 480px — one column; everything within thumb reach. */
  hand: string;
  /** 48rem / 768px — a second column becomes possible. */
  page: string;
  /** 64rem / 1024px — full measure plus margins. */
  desk: string;
  /** 90rem / 1440px — content stops growing; the page gutters absorb the rest. */
  wide: string;
}

export interface MinimalRadiusTheme {
  sm: string;
  md: string;
  lg: string;
  xl: string;
  pill: string;
}

export interface MinimalShadowTheme {
  subtle: string;
  medium: string;
  floating: string;
}

export interface MinimalFocusTheme {
  /** Spread of the focus ring (the `box-shadow` inflation on focus states). */
  ringWidth: string;
}

export interface MinimalControlHeightTheme {
  sm: string;
  md: string;
  lg: string;
}

/** Shared dimensions for inputs, buttons, toggles, and other direct controls. */
export interface MinimalControlTheme {
  /** Minimum pointer target, including compact controls used on touch screens. */
  minTargetSize: string;
  height: MinimalControlHeightTheme;
  iconSize: string;
}

/** Viewport-aware defaults shared by popovers, pickers, menus, and dialogs. */
export interface MinimalOverlayTheme {
  viewportGutter: string;
  anchoredOffset: string;
  maxHeight: string;
}

export interface MinimalTypographyTheme {
  displayFamily: string;
  bodyFamily: string;
  monoFamily: string;
  weightRegular: number;
  weightMedium: number;
  weightSemibold: number;
  weightBold: number;
  displaySize: string;
  h1Size: string;
  h2Size: string;
  bodySize: string;
  captionSize: string;
  metaSize: string;
  lineHeightTight: number;
  lineHeightBody: number;
}

export interface MinimalMotionTheme {
  microDuration: number;
  standardDuration: number;
  slowDuration: number;
  standardEase: [number, number, number, number];
  entranceEase: [number, number, number, number];
  exitEase: [number, number, number, number];
  springStiffness: number;
  springDamping: number;
  hoverLift: number;
  pageOffset: number;
}

export interface MinimalZIndexTheme {
  base: number;
  sticky: number;
  dock: number;
  globalHeader: number;
  dropdown: number;
  overlay: number;
  modal: number;
  tooltip: number;
}

export interface MinimalTheme {
  name: string;
  /** Optional on legacy full-theme literals; normalized by `createMinimalTheme`. */
  colorScheme?: "light" | "dark";
  color: MinimalColorTheme;
  /**
   * The previous fluid scale's names.
   *
   * @deprecated Read `space`. Kept required so existing full-theme literals
   * still satisfy the interface; `createMinimalTheme` resolves it either way.
   */
  spacing: MinimalSpacingTheme;
  /** Optional on legacy full-theme literals; normalized by `createMinimalTheme`. */
  space?: MinimalSpaceTheme;
  /** Optional on legacy full-theme literals; normalized by `createMinimalTheme`. */
  breakpoint?: MinimalBreakpointTheme;
  radius: MinimalRadiusTheme;
  shadow: MinimalShadowTheme;
  focus: MinimalFocusTheme;
  /** Optional on legacy full-theme literals; normalized by `createMinimalTheme`. */
  control?: MinimalControlTheme;
  /** Optional on legacy full-theme literals; normalized by `createMinimalTheme`. */
  overlay?: MinimalOverlayTheme;
  typography: MinimalTypographyTheme;
  motion: MinimalMotionTheme;
  zIndex: MinimalZIndexTheme;
}

/** Fully normalized theme returned by the provider and theme factory. */
export interface ResolvedMinimalTheme extends MinimalTheme {
  colorScheme: "light" | "dark";
  space: MinimalSpaceTheme;
  breakpoint: MinimalBreakpointTheme;
  control: MinimalControlTheme;
  overlay: MinimalOverlayTheme;
}
