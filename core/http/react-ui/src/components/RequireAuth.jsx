import { Navigate } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'

export default function RequireAuth({ children }) {
  const { authEnabled, user, loading } = useAuth()
  if (loading) return null
  if (authEnabled && !user) return <Navigate to="/login" replace />
  return children
}
