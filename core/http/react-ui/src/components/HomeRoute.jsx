import { lazy, Suspense } from 'react'
import { Navigate } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'
import { useDeployment } from '../contexts/DeploymentContext'
import { resolveHome } from '../utils/resolveHome'
import RouteFallback from './RouteFallback'

const Home = lazy(() => import('../pages/Home'))

// Index-route element. Waits for auth + deployment signals to load (so we never
// flash the wrong landing), then either renders Home or redirects to the cell's
// landing page. Redirecting (rather than rendering Nodes/Chat inline at /app)
// keeps each target's own route guard, active-nav state, and deep-linkability.
export default function HomeRoute() {
  const { isAdmin, loading: authLoading } = useAuth()
  const { distributed, p2pEnabled, loading: deployLoading } = useDeployment()

  if (authLoading || deployLoading) return <RouteFallback />

  const target = resolveHome({ isAdmin, distributed, p2pEnabled })
  if (target) return <Navigate to={target} replace />

  return (
    <Suspense fallback={<RouteFallback />}>
      <Home />
    </Suspense>
  )
}
