import { Navigate } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'

export default function RequireFeature({ feature, children }) {
  const { isAdmin, hasFeature, authEnabled, user, loading } = useAuth()
  if (loading) return null
  if (authEnabled && !user) return <Navigate to="/login" replace />
  if (!isAdmin && !hasFeature(feature)) return <Navigate to="/app" replace />
  return children
}
