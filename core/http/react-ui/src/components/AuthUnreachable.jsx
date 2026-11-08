import { useTranslation } from 'react-i18next'
import { useAuth } from '../context/AuthContext'
import { LoadError } from './ui'

// Shown in place of the app while the server cannot say who is signed in or
// whether sign-in is required. It never shows the app: that would treat the
// failure as "no sign-in needed". The provider keeps retrying; the button asks
// at once.
export default function AuthUnreachable() {
  const { t } = useTranslation('auth')
  const { retry } = useAuth()
  return (
    <div className="auth-unreachable" data-testid="auth-unreachable">
      <LoadError
        title={t('session.unreachable.title')}
        nothingChanged={false}
        onRetry={retry}
        testId="auth-unreachable-error"
      >
        <span>{t('session.unreachable.hint')}</span>
      </LoadError>
    </div>
  )
}
