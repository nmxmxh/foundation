import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { ThemeProvider } from 'styled-components'
import { describe, expect, it } from 'vitest'
import App from './App'
import { theme } from './styles/theme'

describe('App', () => {
  it('renders the default scaffold message', () => {
    render(
      <ThemeProvider theme={theme}>
        <MemoryRouter>
          <App />
        </MemoryRouter>
      </ThemeProvider>,
    )

    expect(screen.getByText(/welcome to/i)).toBeInTheDocument()
    expect(screen.getByText(/application is ready to build/i)).toBeInTheDocument()
  })
})
