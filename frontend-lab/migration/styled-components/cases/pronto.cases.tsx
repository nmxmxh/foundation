/**
 * pronto_v1 cases for the temporary style-equivalence snapshot (copied to src/__style_cases__.tsx).
 * Every route rendered through the real App inside the providers main.tsx mounts, plus the
 * auth-gated pages rendered directly (App would only render their redirect).
 */
import type { ReactElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import App from './App'
import { BoxyControls } from './styles/boxy'
import { AppThemeProvider, GlobalStyles, appTheme } from './styles/theme'
import { PRODUCT_NAMES } from './lib/products'
import { minimalBaseTheme, minimalThemeToCSSVariables } from '/Users/okhai/Desktop/OVASABI STUDIOS/foundation/ui-minimal/ts/src/tokens.ts'
import { CheckoutPage } from './features/billing/CheckoutPage'
import { BillingDashboard } from './features/billing/BillingDashboard'
import { PublishPage } from './features/publish/PublishPage'
import { PayoutConsole } from './features/operator/PayoutConsole'


/** Attributes the migration adds in place of transient props; not part of the style contract. */
export const ignoreAttributes = / data-(active|accent|size|alpha|highlight|pronto-[\w-]+)(="[^"]*")?/g

type Tree = Record<string, unknown>
const merge = (base: Tree, over: Tree): Tree => {
  const out: Tree = { ...base }
  for (const [k, v] of Object.entries(over ?? {})) {
    out[k] = v && typeof v === 'object' && !Array.isArray(v) ? merge((base[k] as Tree) ?? {}, v as Tree) : v
  }
  return out
}

export const themeVariables = async (): Promise<Record<string, string>> => {
  const theme = merge(minimalBaseTheme as unknown as Tree, appTheme as Tree)
  return Object.fromEntries(
    Object.entries(minimalThemeToCSSVariables(theme as never)).map(([k, v]) => [k, String(v)]),
  )
}

const shell = (path: string, child: ReactElement) => (
  <AppThemeProvider theme={appTheme}>
    <GlobalStyles />
    <BoxyControls />
    <MemoryRouter initialEntries={[path]}>{child}</MemoryRouter>
  </AppThemeProvider>
)

export const cases = (): Record<string, ReactElement> => {
  const productId = Object.keys(PRODUCT_NAMES)[0]
  const routes = [
    '/', '/about', '/docs', '/catalog', '/pricing', '/stats', '/login', '/register',
    '/forgot-password', '/reset-password', '/claim', '/checkout', '/billing', '/operator/payouts',
    '/publish', '/hub/macro', '/country/za', `/product/${productId}`, `/fusion/${productId}`, '/not-a-page',
  ]
  const out: Record<string, ReactElement> = {}
  for (const path of routes) out[`route ${path}`] = shell(path, <App />)
  out['direct CheckoutPage'] = shell('/checkout', <CheckoutPage />)
  out['direct BillingDashboard'] = shell('/billing', <BillingDashboard />)
  out['direct PublishPage'] = shell('/publish', <PublishPage />)
  out['direct PayoutConsole'] = shell('/operator/payouts', <PayoutConsole />)
  return out
}
