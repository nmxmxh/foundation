/**
 * trotters_v1 cases for the temporary style-equivalence snapshot (copied to src/__style_cases__.tsx).
 * App's routes are lazy(), and a server render only reaches the Suspense fallback, so each
 * page is rendered directly inside the providers App mounts, plus the app shell around a page.
 */
import type { ReactElement } from 'react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { minimalBaseTheme, minimalThemeToCSSVariables } from '/Users/okhai/Desktop/OVASABI STUDIOS/foundation/ui-minimal/ts/src/tokens.ts'
import { AppThemeProvider, GlobalStyles, appTheme } from './styles/theme'
import { TrottersDomainStyles } from './styles/GlobalStyle'
import { Loader } from './components/common/Loader'
import { GlobalUIHost } from './components/feedback/GlobalUIHost'
import { AppLayout } from './components/shell'
import { LandingPage } from './features/landing/LandingPage'
import { Auth } from './pages/Auth'
import { Onboarding } from './pages/Onboarding'
import { Home } from './pages/Home'
import { Schedule } from './pages/Schedule'
import { Rides } from './pages/Rides'
import { Dispatch } from './pages/Dispatch'
import { Chat } from './pages/Chat'
import { Intercity } from './pages/Intercity'
import { Wallet } from './pages/Wallet'
import { Profile } from './pages/Profile'

/** Attributes the migration adds in place of transient props; not part of the style contract. */
export const ignoreAttributes = / data-(active|selected|open|size|variant|tone|state|position|trotters-[\w-]+)(="[^"]*")?/g

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
  return Object.fromEntries(Object.entries(minimalThemeToCSSVariables(theme as never)).map(([k, v]) => [k, String(v)]))
}

const shell = (path: string, child: ReactElement) => (
  <AppThemeProvider theme={appTheme}>
    <GlobalStyles />
    <TrottersDomainStyles />
    <MemoryRouter initialEntries={[path]}>
      <GlobalUIHost />
      {child}
    </MemoryRouter>
  </AppThemeProvider>
)

export const cases = (): Record<string, ReactElement> => {
  const pages: Array<[string, string, ReactElement]> = [
    ['Landing', '/', <LandingPage />],
    ['Auth', '/auth', <Auth />],
    ['Auth register', '/auth/register', <Auth />],
    ['Onboarding', '/onboarding', <Onboarding />],
    ['Home', '/home', <Home />],
    ['Schedule', '/schedule', <Schedule />],
    ['Rides', '/rides', <Rides />],
    ['Dispatch', '/dispatch', <Dispatch />],
    ['Chat', '/chat', <Chat />],
    ['Intercity', '/intercity', <Intercity />],
    ['Wallet', '/wallet', <Wallet />],
    ['Profile', '/profile', <Profile />],
    ['Loader', '/', <Loader />],
  ]
  const out: Record<string, ReactElement> = {}
  for (const [name, path, element] of pages) out[`page ${name}`] = shell(path, element)
  out['shell AppLayout /home'] = shell(
    '/home',
    <Routes>
      <Route element={<AppLayout />}>
        <Route path="/home" element={<Home />} />
      </Route>
    </Routes>,
  )
  return out
}
