/**
 * ovasabi_v1 cases for the temporary style-equivalence snapshot (copied to src/__style_cases__.tsx).
 * Pages are lazy in App, and a server render only reaches the pending fallback, so each page is
 * rendered directly inside the providers entry-client.tsx mounts; the App shell is rendered at '/'.
 */
import type { ReactElement } from 'react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { minimalBaseTheme, minimalThemeToCSSVariables } from '/Users/okhai/Desktop/OVASABI STUDIOS/foundation/ui-minimal/ts/src/tokens.ts'
import { App } from './App'
import { RuntimeStyleProvider } from './styles/runtimeStyle'
import { AppThemeProvider, FontStyles, GlobalStyles, appTheme, createAppReactStyle } from './styles/theme'
import { GroundStyles } from './styles/ground'
import { TransitionStyles } from './styles/transitions'
import { AuthProvider } from './features/auth/authContext'
import { ARCHIVE } from './content/archive'
import { PROJECTS } from './content/projects'
import { HomePage } from './features/home/homePage'
import { NotFoundPage } from './features/home/notFoundPage'
import { StudioPage } from './features/studio/studioPage'
import { ContactPage } from './features/contact/contactPage'
import { BoothPage } from './features/booth/boothPage'
import { RegisterPage } from './features/auth/registerPage'
import { SignInPage } from './features/auth/signInPage'
import { ArchivePage } from './features/archive/archivePage'
import { EntryPage } from './features/archive/entryPage'
import { ProjectsPage, ProjectPage } from './features/projects/projectsPage'
import { PeoplePage } from './features/people/peoplePage'
import { ProfilePage } from './features/profile/profilePage'
import { ComingSoonPage } from './features/comingSoon/comingSoonPage'
import { DemoGateway } from './features/comingSoon/demoGateway'
import { ServicesPage } from './features/services/servicesPage'

/** Attributes the migration adds in place of transient props; not part of the style contract. */
export const ignoreAttributes = / data-(active|selected|open|size|variant|tone|state|position|emphasis|corner|action|live|animated|ovasabi-[\w-]+)(="[^"]*")?/g

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

const shell = (path: string, pattern: string, child: ReactElement) => (
  <AppThemeProvider theme={appTheme}>
    <RuntimeStyleProvider value={createAppReactStyle('csr')}>
      <FontStyles />
      <GlobalStyles />
      <GroundStyles />
      <TransitionStyles />
      <MemoryRouter initialEntries={[path]}>
        <AuthProvider>
          <Routes>
            <Route path={pattern} element={child} />
          </Routes>
        </AuthProvider>
      </MemoryRouter>
    </RuntimeStyleProvider>
  </AppThemeProvider>
)

export const cases = (): Record<string, ReactElement> => {
  const entrySlug = ARCHIVE[0]?.slug ?? 'missing'
  const projectSlug = PROJECTS[0]?.slug ?? 'missing'
  const pages: Array<[string, string, string, ReactElement]> = [
    ['Home', '/', '*', <HomePage />],
    ['Archive', '/blog', '*', <ArchivePage />],
    ['Entry', `/blog/${entrySlug}`, '/blog/:slug', <EntryPage />],
    ['Entry unknown', '/blog/not-an-entry', '/blog/:slug', <EntryPage />],
    ['Projects', '/projects', '*', <ProjectsPage />],
    ['Project', `/projects/${projectSlug}`, '/projects/:slug', <ProjectPage />],
    ['People', '/people', '*', <PeoplePage />],
    ['Services', '/services', '*', <ServicesPage />],
    ['Booth', '/booth', '*', <BoothPage />],
    ['Contact', '/contact', '*', <ContactPage />],
    ['Profile', '/profile', '*', <ProfilePage />],
    ['Studio', '/studio', '*', <StudioPage />],
    ['SignIn', '/sign-in', '*', <SignInPage />],
    ['Register', '/register', '*', <RegisterPage />],
    ['NotFound', '/nope', '*', <NotFoundPage />],
    ['ComingSoon', '/', '*', <ComingSoonPage />],
    ['DemoGateway', '/demo', '*', <DemoGateway />],
  ]
  const out: Record<string, ReactElement> = {}
  for (const [name, path, pattern, element] of pages) out[`page ${name}`] = shell(path, pattern, element)
  out['app shell /'] = (
    <AppThemeProvider theme={appTheme}>
      <RuntimeStyleProvider value={createAppReactStyle('csr')}>
        <FontStyles />
        <GlobalStyles />
        <GroundStyles />
        <TransitionStyles />
        <MemoryRouter initialEntries={['/']}>
          <App />
        </MemoryRouter>
      </RuntimeStyleProvider>
    </AppThemeProvider>
  )
  return out
}
