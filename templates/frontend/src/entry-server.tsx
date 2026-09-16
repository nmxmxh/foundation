import { StrictMode } from 'react'
import { renderToString } from 'react-dom/server'
import { StaticRouter } from 'react-router-dom'
import App from './App'
import { AppThemeProvider, GlobalStyles, appTheme } from './styles/theme'

/*
 * Build-time render of the first screen (prerenderShell in vite.config.ts).
 * The HTML arrives with this markup and the extracted CSS, so the browser
 * paints before any script runs; main.tsx then hydrates the same tree.
 * Keep it identical to the client: no window, time, randomness or signed-in
 * state during render (Foundation research doc §15.4).
 */
export function render(url: string): string {
  return renderToString(
    <StrictMode>
      <AppThemeProvider theme={appTheme}>
        <GlobalStyles />
        <StaticRouter location={url}>
          <App />
        </StaticRouter>
      </AppThemeProvider>
    </StrictMode>,
  )
}
