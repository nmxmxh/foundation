import { StrictMode } from 'react'
import { BrowserRouter } from 'react-router-dom'
import { mountRoot } from '@ovasabi/frontend-kit'
import App from './App'
import { AppThemeProvider, GlobalStyles, appTheme } from './styles/theme'

// Hydrates the markup prerenderShell wrote into index.html (see
// src/entry-server.tsx); client-renders when the root is empty, as in dev.
mountRoot(
  document.getElementById('root')!,
  <StrictMode>
    <AppThemeProvider theme={appTheme}>
      <GlobalStyles />
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </AppThemeProvider>
  </StrictMode>
)
