export type DeepPartial<T> = {
  [K in keyof T]?: T[K] extends Record<string, unknown> ? DeepPartial<T[K]> : T[K];
};

export type MinimalTone = "neutral" | "brand" | "info" | "success" | "warning" | "danger";
export type MinimalEmphasis = "soft" | "solid" | "outline";
export type MinimalSize = "sm" | "md" | "lg";
export type MinimalDensity = "compact" | "comfortable" | "relaxed";

export interface MinimalTheme {
  name: string;
  color: {
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
  };
  spacing: {
    xs: string;
    sm: string;
    md: string;
    lg: string;
    xl: string;
    "2xl": string;
  };
  radius: {
    sm: string;
    md: string;
    lg: string;
    xl: string;
    pill: string;
  };
  shadow: {
    subtle: string;
    medium: string;
    floating: string;
  };
  typography: {
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
  };
  motion: {
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
  };
  zIndex: {
    base: number;
    sticky: number;
    dropdown: number;
    overlay: number;
    modal: number;
    tooltip: number;
  };
}
