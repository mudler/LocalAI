import { Navigate } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'
import AuthUnreachable from './AuthUnreachable'

export default function RequireAuth({ children }) {
  const { authEnabled, staticApiKeyRequired, user, loading, unreachable } = useAuth()
  if (loading) return null
  // The server did not answer: say so. Do not guess that sign-in is off.
  if (unreachable) return <AuthUnreachable />
  if ((authEnabled || staticApiKeyRequired) && !user) return <Navigate to="/login" replace />
  return children
}
