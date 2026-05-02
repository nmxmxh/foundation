import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import App from './App'
import { AppThemeProvider, GlobalStyles, appTheme } from './styles/theme'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <AppThemeProvider theme={appTheme}>
      <GlobalStyles />
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </AppThemeProvider>
  </StrictMode>
)
