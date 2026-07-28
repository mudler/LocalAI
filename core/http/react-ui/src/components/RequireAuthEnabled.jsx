import { Navigate } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'

// RequireAuthEnabled gates routes that only make sense when auth is on.
// User management is the canonical example: in single-user (no-auth)
// mode there is exactly one synthetic local user, so the page would
// either be empty or expose admin tools that have nothing to manage.
//
// We redirect to /app rather than render a "not available" page so that
// stale bookmarks don't leave the user on a dead-end screen.
export default function RequireAuthEnabled({ children }) {
  const { authEnabled, loading } = useAuth()
  if (loading) return null
  if (!authEnabled) return <Navigate to="/app" replace />
  return children
}
