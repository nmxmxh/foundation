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
  // styled-components requires interface augmentation for DefaultTheme.
  // eslint-disable-next-line @typescript-eslint/no-empty-object-type
  export interface DefaultTheme extends MinimalTheme {}
}
