import { Navigate } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'

export default function RequireAdmin({ children }) {
  const { isAdmin, authEnabled, user, loading } = useAuth()
  if (loading) return null
  if (authEnabled && !user) return <Navigate to="/login" replace />
  if (!isAdmin) return <Navigate to="/app" replace />
  return children
}
