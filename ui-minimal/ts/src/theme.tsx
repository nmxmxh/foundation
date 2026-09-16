import type { CSSProperties, HTMLAttributes, PropsWithChildren } from "react";
import { createContext, useContext, useMemo } from "react";

import type { DeepPartial, MinimalTheme, ResolvedMinimalTheme } from "./types.ts";
import { createMinimalTheme, mergeRecord, minimalBaseTheme, minimalThemeToCSSVariables } from "./tokens.ts";
import { BASE_DECLARATIONS, declarations } from "./globalStyles.ts";

export { minimalGlobalStylesClass } from "./globalStyles.ts";

export { createMinimalTheme, from, minimalBaseTheme, minimalThemeToCSSVariables, minimalVars, until } from "./tokens.ts";
export type { MinimalThemeVars } from "./tokens.ts";

const MinimalThemeContext = createContext<ResolvedMinimalTheme>(minimalBaseTheme);

/**
 * Writes a non-base theme's variables onto `:root`.
 *
 * The base theme's are already in the extracted stylesheet, so for the default
 * theme this renders nothing at all. A custom theme adds one `<style>` with its
 * declarations — server-rendered with the markup, so there is no flash.
 */
export const MinimalGlobalStyles = () => {
  const theme = useContext(MinimalThemeContext);
  const block = useMemo(() => {
    const own = declarations(theme);
    const scheme = theme.colorScheme ?? "light";
    if (own === BASE_DECLARATIONS && scheme === (minimalBaseTheme.colorScheme ?? "light")) return null;
    return `:root{color-scheme:${scheme};${own}}`;
  }, [theme]);
  return block ? <style data-minimal-theme={theme.name}>{block}</style> : null;
};

export const MinimalThemeProvider = ({
  theme,
  children,
}: PropsWithChildren<{ theme?: DeepPartial<MinimalTheme> }>) => {
  const mergedTheme = useMemo(() => createMinimalTheme(theme), [theme]);
  return <MinimalThemeContext.Provider value={mergedTheme}>{children}</MinimalThemeContext.Provider>;
};

export interface MinimalThemeScopeProps extends HTMLAttributes<HTMLDivElement> {
  themeOverride?: DeepPartial<MinimalTheme>;
}

/**
 * Applies a nested token override to its subtree. Use this for edition panels,
 * embedded widgets, and previews that must not rewrite the document-level
 * `:root` variables. It is the only way a nested override reaches `minimalVars`
 * consumers, which is every ui-minimal component.
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
    [parentTheme, themeOverride],
  );
  const variables = minimalThemeToCSSVariables(scopedTheme);

  return (
    <MinimalThemeContext.Provider value={scopedTheme}>
      <div
        data-minimal-theme-scope={scopedTheme.name}
        style={{ ...variables, colorScheme: scopedTheme.colorScheme, ...style } as CSSProperties}
        {...props}
      >
        {children}
      </div>
    </MinimalThemeContext.Provider>
  );
};

/** The resolved theme for this subtree; the base theme outside any provider. */
export const useMinimalTheme = (): ResolvedMinimalTheme => useContext(MinimalThemeContext);
