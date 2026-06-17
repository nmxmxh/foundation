import { PropsWithChildren, useMemo } from "react";
import { ThemeProvider, createGlobalStyle, useTheme as useStyledTheme, type DefaultTheme } from "styled-components";

import type { DeepPartial, MinimalTheme } from "./types";

const isPlainObject = (value: unknown): value is object =>
  typeof value === "object" && value !== null && !Array.isArray(value);

export const minimalBaseTheme: MinimalTheme = {
  name: "ovasabi-minimal",
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
  spacing: {
    xs: "clamp(2px, 0.5vw, 4px)",
    sm: "clamp(6px, 1vw, 10px)",
    md: "clamp(12px, 2vw, 20px)",
    lg: "clamp(18px, 3vw, 28px)",
    xl: "clamp(32px, 5vw, 48px)",
    "2xl": "clamp(48px, 8vw, 80px)",
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
  typography: {
    displayFamily: "\"Fraunces\", Georgia, serif",
    bodyFamily: "\"Instrument Sans\", \"Inter\", -apple-system, BlinkMacSystemFont, \"Segoe UI\", sans-serif",
    monoFamily: "\"IBM Plex Mono\", \"SFMono-Regular\", monospace",
    weightRegular: 400,
    weightMedium: 500,
    weightSemibold: 600,
    weightBold: 700,
    displaySize: "clamp(2rem, 4vw, 3rem)",
    h1Size: "clamp(1.25rem, 2.5vw, 1.75rem)",
    h2Size: "clamp(1rem, 2vw, 1.25rem)",
    bodySize: "clamp(0.875rem, 1.5vw, 1rem)",
    captionSize: "clamp(0.75rem, 1vw, 0.875rem)",
    metaSize: "clamp(0.625rem, 0.8vw, 0.75rem)",
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

export const createMinimalTheme = (overrides?: DeepPartial<MinimalTheme>): MinimalTheme =>
  mergeRecord(minimalBaseTheme, overrides);

export const minimalThemeToCSSVariables = (theme: MinimalTheme): Record<string, string | number> => ({
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
  "--minimal-space-xs": theme.spacing.xs,
  "--minimal-space-sm": theme.spacing.sm,
  "--minimal-space-md": theme.spacing.md,
  "--minimal-space-lg": theme.spacing.lg,
  "--minimal-space-xl": theme.spacing.xl,
  "--minimal-space-2xl": theme.spacing["2xl"],
  "--minimal-radius-sm": theme.radius.sm,
  "--minimal-radius-md": theme.radius.md,
  "--minimal-radius-lg": theme.radius.lg,
  "--minimal-radius-xl": theme.radius.xl,
  "--minimal-radius-pill": theme.radius.pill,
  "--minimal-shadow-subtle": theme.shadow.subtle,
  "--minimal-shadow-medium": theme.shadow.medium,
  "--minimal-shadow-floating": theme.shadow.floating,
  "--minimal-font-display-family": theme.typography.displayFamily,
  "--minimal-font-body-family": theme.typography.bodyFamily,
  "--minimal-font-mono-family": theme.typography.monoFamily,
  "--minimal-font-display-size": theme.typography.displaySize,
  "--minimal-font-h1-size": theme.typography.h1Size,
  "--minimal-font-h2-size": theme.typography.h2Size,
  "--minimal-font-body-size": theme.typography.bodySize,
  "--minimal-font-caption-size": theme.typography.captionSize,
  "--minimal-font-meta-size": theme.typography.metaSize,
  "--minimal-z-base": theme.zIndex.base,
  "--minimal-z-sticky": theme.zIndex.sticky,
  "--minimal-z-dock": theme.zIndex.dock,
  "--minimal-z-global-header": theme.zIndex.globalHeader,
  "--minimal-z-dropdown": theme.zIndex.dropdown,
  "--minimal-z-overlay": theme.zIndex.overlay,
  "--minimal-z-modal": theme.zIndex.modal,
  "--minimal-z-tooltip": theme.zIndex.tooltip,
});

const GlobalStyles = createGlobalStyle`
  :root {
    ${({ theme }) =>
      Object.entries(minimalThemeToCSSVariables(theme)).map(([key, value]) => `${key}: ${value};`).join("\n")}
  }

  *,
  *::before,
  *::after {
    box-sizing: border-box;
  }

  body {
    background: ${({ theme }) => theme.color.bgApp};
    color: ${({ theme }) => theme.color.textPrimary};
    font-family: ${({ theme }) => theme.typography.bodyFamily};
    font-size: ${({ theme }) => theme.typography.bodySize};
    line-height: ${({ theme }) => theme.typography.lineHeightBody};
    -webkit-font-smoothing: antialiased;
    -moz-osx-font-smoothing: grayscale;
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
`;

export const MinimalGlobalStyles = () => <GlobalStyles />;

export const MinimalThemeProvider = ({
  theme,
  children,
}: PropsWithChildren<{ theme?: DeepPartial<MinimalTheme> }>) => {
  const mergedTheme = useMemo(() => createMinimalTheme(theme), [theme]);
  return <ThemeProvider theme={mergedTheme as unknown as DefaultTheme}>{children}</ThemeProvider>;
};

export const useMinimalTheme = (): MinimalTheme => {
  const theme = useStyledTheme() as MinimalTheme | undefined;
  if (theme && typeof theme.name === "string") {
    return theme;
  }
  return minimalBaseTheme;
};
