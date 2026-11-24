import { Navigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../context/AuthContext'
import AuthUnreachable from './AuthUnreachable'
import { Callout } from './ui'

export default function RequireAuth({ children }) {
  const { authEnabled, staticApiKeyRequired, user, loading, unreachable, stale } = useAuth()
  const { t } = useTranslation('auth')
  if (loading) return null
  // The server did not answer: say so. Do not guess that sign-in is off.
  if (unreachable) return <AuthUnreachable />
  if ((authEnabled || staticApiKeyRequired) && !user) return <Navigate to="/login" replace />
  // A refresh failed after the server had answered once: the app stays up with
  // what it knows, and says that the status is old.
  return (
    <>
      {stale && <Callout tone="warn" role="status" testId="auth-stale">{t('session.unreachable.title')}</Callout>}
      {children}
    </>
  )
}
