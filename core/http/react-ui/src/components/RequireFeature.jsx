import { Navigate } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'

// `disabled` is shown instead of the redirect when the viewer lacks the feature,
// for pages that should say what is off and what turns it on.
export default function RequireFeature({ feature, disabled, children }) {
  const { isAdmin, hasFeature, authEnabled, user, loading } = useAuth()
  if (loading) return null
  if (authEnabled && !user) return <Navigate to="/login" replace />
  if (!isAdmin && !hasFeature(feature)) return disabled || <Navigate to="/app" replace />
  return children
}
