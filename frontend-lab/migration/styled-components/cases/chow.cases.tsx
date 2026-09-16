/**
 * chowdash_rider_v1 (ChooseChow) cases for the temporary style-equivalence snapshot
 * (copied to src/__style_cases__.tsx). Pages are eager in ChooseChowApp / ChowDashApp, so
 * every case renders the real App inside EditionProvider at a route, the way App.test.tsx
 * does; a setup hook seeds the session and the active edition before each render.
 */
import type { ReactElement } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { minimalBaseTheme, minimalThemeToCSSVariables } from '/Users/okhai/Desktop/OVASABI STUDIOS/foundation/ui-minimal/ts/src/tokens.ts'
import App from './App'
import { EditionProvider } from './edition/EditionProvider'
import { setActiveEdition, type Edition } from './lib/edition'
import { editionThemeOverride } from './styles/editionTheme'

/** Attributes the migration adds in place of transient props; not part of the style contract. */
export const ignoreAttributes = / data-(active|selected|open|size|variant|tone|state|position|embedded|fluid|hoverable|sold-out|dark|i|chow-[\w-]+)(="[^"]*")?/g

type Tree = Record<string, unknown>
const merge = (base: Tree, over: Tree): Tree => {
  const out: Tree = { ...base }
  for (const [k, v] of Object.entries(over ?? {})) {
    out[k] = v && typeof v === 'object' && !Array.isArray(v) ? merge((base[k] as Tree) ?? {}, v as Tree) : v
  }
  return out
}

/** Variables for the ChooseChow edition; ChowDash cases differ only in the brand accent. */
export const themeVariables = async (): Promise<Record<string, string>> => {
  const theme = merge(minimalBaseTheme as unknown as Tree, editionThemeOverride('choosechow') as Tree)
  return Object.fromEntries(Object.entries(minimalThemeToCSSVariables(theme as never)).map(([k, v]) => [k, String(v)]))
}

type Role = 'customer' | 'chef' | null

const session = (role: Role, edition: Edition) => () => {
  try {
    localStorage.clear()
    sessionStorage.clear()
  } catch {
    /* noop */
  }
  if (role) {
    localStorage.setItem('chow_session', JSON.stringify({ displayName: 'Test', email: 'test@chow.dev', role }))
    localStorage.setItem('auth_token', 'test-token')
  }
  setActiveEdition(edition)
}

const at = (path: string, edition: Edition, role: Role) => ({
  element: (
    <MemoryRouter initialEntries={[`${path}?edition=${edition}`]}>
      <EditionProvider>
        <App />
      </EditionProvider>
    </MemoryRouter>
  ),
  setup: session(role, edition),
})

export const cases = (): Record<string, { element: ReactElement; setup: () => void }> => {
  const out: Record<string, { element: ReactElement; setup: () => void }> = {}
  for (const path of ['/', '/login', '/register', '/privacy', '/terms', '/delete-account']) {
    out[`public ${path}`] = at(path, 'choosechow', null)
  }
  for (const path of ['/dashboard', '/chefs', '/dishes', '/market', '/orders', '/profile', '/subscriptions', '/mealplan', '/allergies', '/memberships', '/wallet', '/inbox', '/help', '/cart']) {
    out[`customer ${path}`] = at(path, 'choosechow', 'customer')
  }
  for (const path of ['/dashboard', '/schedule', '/orders', '/menu', '/menu/new', '/wallet', '/profile', '/inbox', '/help']) {
    out[`chef ${path}`] = at(path, 'choosechow', 'chef')
  }
  return out
}
