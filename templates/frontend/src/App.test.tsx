import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import App from './App'
import { AppThemeProvider, appTheme } from './styles/theme'

describe('App', () => {
  it('renders the generated prototype runtime status', () => {
    render(
      <AppThemeProvider theme={appTheme}>
        <MemoryRouter>
          <App />
        </MemoryRouter>
      </AppThemeProvider>,
    )

    expect(screen.getByText('{{PROJECT_NAME}}')).toBeInTheDocument()
    expect(screen.getByText(/runtime mode/i)).toBeInTheDocument()
    expect(screen.getByText(/generated schemas/i)).toBeInTheDocument()
    expect(screen.getByText(/fixture records/i)).toBeInTheDocument()
  })
})
