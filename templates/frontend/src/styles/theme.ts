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
export const AppThemeProvider = MinimalThemeProvider

export type Theme = MinimalTheme

declare module 'styled-components' {
  export interface DefaultTheme extends MinimalTheme {}
}
