import { useSyncExternalStore } from 'react'
import { Routes, Route } from 'react-router-dom'
import { styled } from 'styled-components'
import { offlinePrototypeRuntime } from './stores/prototype'

const Container = styled.div`
  min-height: 100vh;
  display: flex;
  flex-direction: column;
`

const Main = styled.main`
  flex: 1;
  padding: ${({ theme }) => theme.spacing.lg};
`

const Header = styled.header`
  display: grid;
  gap: ${({ theme }) => theme.spacing.sm};
  max-width: 960px;
`

const RuntimeGrid = styled.section`
  display: grid;
  gap: ${({ theme }) => theme.spacing.md};
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  margin-top: ${({ theme }) => theme.spacing.lg};
  max-width: 960px;
`

const RuntimePanel = styled.article`
  border: 1px solid ${({ theme }) => theme.color.borderSubtle};
  border-radius: ${({ theme }) => theme.radius.sm};
  padding: ${({ theme }) => theme.spacing.md};
`

const Metric = styled.p`
  font-size: ${({ theme }) => theme.typography.h2Size};
  font-weight: ${({ theme }) => theme.typography.weightSemibold};
  margin: 0;
`

const Label = styled.p`
  color: ${({ theme }) => theme.color.textSecondary};
  margin: 0 0 ${({ theme }) => theme.spacing.xs};
`

const EMPTY_SNAPSHOT = {
  byId: new Map<string, Record<string, unknown>>(),
  records: [] as readonly Record<string, unknown>[],
  version: 0,
}

const emptySubscribe = () => () => undefined

function HomePage() {
  const firstStore = offlinePrototypeRuntime.stores[0]
  const snapshot = useSyncExternalStore(
    firstStore?.subscribe ?? emptySubscribe,
    firstStore?.getSnapshot ?? (() => EMPTY_SNAPSHOT),
    firstStore?.getSnapshot ?? (() => EMPTY_SNAPSHOT),
  )
  const firstDomain = offlinePrototypeRuntime.domains[0]

  return (
    <>
      <Header>
        <h1>{{PROJECT_NAME}}</h1>
        <p>
          Prototype state is generated from protobuf contracts and is ready for
          offline fixtures or live Hermes projections.
        </p>
      </Header>
      <RuntimeGrid aria-label="prototype runtime status">
        <RuntimePanel>
          <Label>Runtime mode</Label>
          <Metric>{offlinePrototypeRuntime.mode}</Metric>
        </RuntimePanel>
        <RuntimePanel>
          <Label>Generated schemas</Label>
          <Metric>{offlinePrototypeRuntime.schemas.length}</Metric>
        </RuntimePanel>
        <RuntimePanel>
          <Label>Tenant stores</Label>
          <Metric>{offlinePrototypeRuntime.stores.length}</Metric>
        </RuntimePanel>
        <RuntimePanel>
          <Label>Fixture records</Label>
          <Metric>{snapshot.records.length}</Metric>
        </RuntimePanel>
      </RuntimeGrid>
      {firstDomain ? (
        <RuntimeGrid aria-label="first generated domain">
          <RuntimePanel>
            <Label>First domain</Label>
            <Metric>{firstDomain.constants.domain}</Metric>
          </RuntimePanel>
          <RuntimePanel>
            <Label>Collection</Label>
            <Metric>{firstDomain.constants.collection}</Metric>
          </RuntimePanel>
        </RuntimeGrid>
      ) : null}
    </>
  )
}

function App() {
  return (
    <Container>
      <Main>
        <Routes>
          <Route path="/" element={<HomePage />} />
        </Routes>
      </Main>
    </Container>
  )
}

export default App
