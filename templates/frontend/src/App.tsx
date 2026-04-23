import { Routes, Route } from 'react-router-dom'
import styled from 'styled-components'

const Container = styled.div`
  min-height: 100vh;
  display: flex;
  flex-direction: column;
`

const Main = styled.main`
  flex: 1;
  padding: ${({ theme }) => theme.spacing.lg};
`

function HomePage() {
  return (
    <div>
      <h1>Welcome to {{PROJECT_NAME}}</h1>
      <p>Your application is ready to build.</p>
    </div>
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
