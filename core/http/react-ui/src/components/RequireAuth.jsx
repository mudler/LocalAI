import { Navigate } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'

export default function RequireAuth({ children }) {
  const { authEnabled, staticApiKeyRequired, user, loading } = useAuth()
  if (loading) return null
  if ((authEnabled || staticApiKeyRequired) && !user) return <Navigate to="/login" replace />
  return children
}
