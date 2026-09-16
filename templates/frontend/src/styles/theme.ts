import {
  MinimalGlobalStyles,
  MinimalThemeProvider,
  createMinimalTheme,
  type DeepPartial,
  type MinimalTheme,
} from '@ovasabi/ui-minimal'

export const appTheme: DeepPartial<MinimalTheme> = {
  name: '{{PROJECT_NAME}}',
}

export const theme = createMinimalTheme(appTheme)
export const GlobalStyles = MinimalGlobalStyles
/**
 * The app's styles are Linaria like ui-minimal's, so they read tokens through
 * `minimalVars` (CSS variables) at build time and never a runtime theme object.
 * Code that needs a resolved value in script uses `useMinimalTheme()`.
 */
export const AppThemeProvider = MinimalThemeProvider

export type Theme = MinimalTheme

