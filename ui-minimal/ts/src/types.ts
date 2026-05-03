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

export interface MinimalSpacingTheme {
  xs: string;
  sm: string;
  md: string;
  lg: string;
  xl: string;
  "2xl": string;
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
  dropdown: number;
  overlay: number;
  modal: number;
  tooltip: number;
}

export interface MinimalTheme {
  name: string;
  color: MinimalColorTheme;
  spacing: MinimalSpacingTheme;
  radius: MinimalRadiusTheme;
  shadow: MinimalShadowTheme;
  typography: MinimalTypographyTheme;
  motion: MinimalMotionTheme;
  zIndex: MinimalZIndexTheme;
}
