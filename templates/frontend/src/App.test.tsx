import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import App from './App'
import { AppThemeProvider, appTheme } from './styles/theme'

describe('App', () => {
  it('renders the default scaffold message', () => {
    render(
      <AppThemeProvider theme={appTheme}>
        <MemoryRouter>
          <App />
        </MemoryRouter>
      </AppThemeProvider>,
    )

    expect(screen.getByText(/welcome to/i)).toBeInTheDocument()
    expect(screen.getByText(/application is ready to build/i)).toBeInTheDocument()
  })
})
