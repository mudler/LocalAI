import { useState, useEffect } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../context/AuthContext'
import { useBranding } from '../contexts/BrandingContext'
import { apiUrl } from '../utils/basePath'
import './auth.css'

export default function Login() {
  const navigate = useNavigate()
  const { t } = useTranslation('auth')
  const { code: urlInviteCode } = useParams()
  const [searchParams] = useSearchParams()
  const { authEnabled, staticApiKeyRequired, user, loading: authLoading, refresh } = useAuth()
  const branding = useBranding()
  const [providers, setProviders] = useState([])
  const [hasUsers, setHasUsers] = useState(true)
  const [registrationMode, setRegistrationMode] = useState('open')
  const [statusLoading, setStatusLoading] = useState(true)

  // Form state
  const [mode, setMode] = useState('login') // 'login' or 'register'
  const [email, setEmail] = useState('')
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [inviteCode, setInviteCode] = useState('')
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [showTokenLogin, setShowTokenLogin] = useState(false)
  const [token, setToken] = useState('')

  const extractError = (data, fallback) => {
    if (!data) return fallback
    if (typeof data.error === 'string') return data.error
    if (data.error && typeof data.error === 'object') return data.error.message || fallback
    if (typeof data.message === 'string') return data.message
    return fallback
  }

  // Pre-fill invite code from URL and switch to register mode
  useEffect(() => {
    if (urlInviteCode) {
      setInviteCode(urlInviteCode)
      setMode('register')
    }
  }, [urlInviteCode])

  // Show error from OAuth redirect (e.g. invite_required)
  useEffect(() => {
    const errorParam = searchParams.get('error')
    if (errorParam === 'invite_required') {
      setError(t('login.errors.inviteRequired'))
    }
  }, [searchParams, t])

  useEffect(() => {
    fetch(apiUrl('/api/auth/status'))
      .then(r => r.json())
      .then(data => {
        setProviders(data.providers || [])
        setHasUsers(data.hasUsers !== false)
        setRegistrationMode(data.registrationMode || 'open')
        if (!data.hasUsers) setMode('register')
        setStatusLoading(false)
      })
      .catch(() => setStatusLoading(false))
  }, [])

  // Redirect if auth is disabled or user is already logged in
  useEffect(() => {
    if (!authLoading && ((!authEnabled && !staticApiKeyRequired) || user)) {
      navigate('/app', { replace: true })
    }
  }, [authLoading, authEnabled, user, navigate])

  const handleEmailLogin = async (e) => {
    e.preventDefault()
    setError('')
    setMessage('')
    setSubmitting(true)

    try {
      const res = await fetch(apiUrl('/api/auth/login'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ email, password }),
      })
      const data = await res.json()

      if (!res.ok) {
        setError(extractError(data, t('login.errors.loginFailed')))
        setSubmitting(false)
        return
      }

      await refresh()
    } catch {
      setError(t('login.errors.networkError'))
      setSubmitting(false)
    }
  }

  const handleRegister = async (e) => {
    e.preventDefault()
    setError('')
    setMessage('')

    if (password !== confirmPassword) {
      setError(t('login.errors.passwordsDoNotMatch'))
      return
    }

    setSubmitting(true)

    try {
      const body = { email, password, name }
      if (inviteCode) {
        body.inviteCode = inviteCode
      }

      const res = await fetch(apiUrl('/api/auth/register'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      const data = await res.json()

      if (!res.ok) {
        setError(extractError(data, t('login.errors.registrationFailed')))
        setSubmitting(false)
        return
      }

      if (data.pending) {
        setMessage(data.message || t('login.messages.registrationPending'))
        setSubmitting(false)
        return
      }

      // Full reload so the auth provider picks up the new session cookie
      window.location.href = '/app'
      return
    } catch {
      setError(t('login.errors.networkError'))
      setSubmitting(false)
    }
  }

  const handleTokenLogin = async (e) => {
    e.preventDefault()
    if (!token.trim()) {
      setError(t('login.errors.enterToken'))
      return
    }
    setError('')
    setSubmitting(true)

    try {
      const res = await fetch(apiUrl('/api/auth/token-login'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ token: token.trim() }),
      })
      const data = await res.json()

      if (!res.ok) {
        setError(extractError(data, t('login.errors.invalidToken')))
        setSubmitting(false)
        return
      }

      await refresh()
    } catch {
      setError(t('login.errors.networkError'))
      setSubmitting(false)
    }
  }

  if (authLoading || statusLoading) return null

  // Legacy API key-only mode: show a simplified login with just the token input
  if (staticApiKeyRequired && !authEnabled) {
    return (
      <div className="login-page">
        <div className="card login-card">
          <div className="login-header">
            <img src={apiUrl(branding.logoUrl)} alt={branding.instanceName} className="login-logo" />
            <h1 className="login-title">{branding.instanceName}</h1>
            {branding.instanceTagline && <p className="login-tagline">{branding.instanceTagline}</p>}
            <p className="login-subtitle">{t('login.tokenSubtitle')}</p>
          </div>

          {error && (
            <div className="login-alert login-alert-error">{error}</div>
          )}

          <form onSubmit={handleTokenLogin}>
            <div className="form-group">
              <input
                className="input"
                type="password"
                value={token}
                onChange={(e) => { setToken(e.target.value); setError('') }}
                placeholder={t('login.tokenPlaceholder')}
                autoFocus
              />
            </div>
            <button type="submit" className="btn btn-primary login-btn-full" disabled={submitting}>
              {submitting ? t('login.signingIn') : t('login.signIn')}
            </button>
          </form>
        </div>
      </div>
    )
  }

  const hasGitHub = providers.includes('github')
  const hasOIDC = providers.includes('oidc')
  const hasLocal = providers.includes('local')
  const hasOAuth = hasGitHub || hasOIDC
  const showInviteField = (registrationMode === 'invite' || registrationMode === 'approval') && mode === 'register' && hasUsers
  const inviteRequired = registrationMode === 'invite' && hasUsers

  // Build OAuth login URLs with invite code if present
  const githubLoginUrl = inviteCode
    ? apiUrl(`/api/auth/github/login?invite_code=${encodeURIComponent(inviteCode)}`)
    : apiUrl('/api/auth/github/login')

  const oidcLoginUrl = inviteCode
    ? apiUrl(`/api/auth/oidc/login?invite_code=${encodeURIComponent(inviteCode)}`)
    : apiUrl('/api/auth/oidc/login')

  return (
    <div className="login-page">
      <div className="card login-card">
        <div className="login-header">
          <img src={apiUrl(branding.logoUrl)} alt={branding.instanceName} className="login-logo" />
          <h1 className="login-title">{branding.instanceName}</h1>
          {branding.instanceTagline && <p className="login-tagline">{branding.instanceTagline}</p>}
          <p className="login-subtitle">
            {!hasUsers
              ? t('login.createAdminSubtitle')
              : mode === 'register'
                ? t('login.registerSubtitle')
                : t('login.subtitle')}
          </p>
        </div>

        {error && (
          <div className="login-alert login-alert-error">{error}</div>
        )}

        {message && (
          <div className="login-alert login-alert-success">{message}</div>
        )}

        {hasGitHub && (
          <a
            href={githubLoginUrl}
            className="btn btn-primary login-btn-full"
            style={{ marginBottom: hasOIDC ? '0.5rem' : undefined }}
          >
            <i className="fab fa-github" /> {t('login.signInWithGitHub')}
          </a>
        )}

        {hasOIDC && (
          <a
            href={oidcLoginUrl}
            className="btn btn-primary login-btn-full"
          >
            <i className="fas fa-sign-in-alt" /> {t('login.signInWithSSO')}
          </a>
        )}

        {hasOAuth && hasLocal && (
          <div className="login-divider">{t('login.or')}</div>
        )}

        {hasLocal && mode === 'login' && (
          <form onSubmit={handleEmailLogin}>
            <div className="form-group">
              <label className="form-label">{t('login.email')}</label>
              <input
                className="input"
                type="email"
                value={email}
                onChange={(e) => { setEmail(e.target.value); setError('') }}
                placeholder={t('login.emailPlaceholder')}
                autoFocus={!hasGitHub}
                required
              />
            </div>
            <div className="form-group">
              <label className="form-label">{t('login.password')}</label>
              <input
                className="input"
                type="password"
                value={password}
                onChange={(e) => { setPassword(e.target.value); setError('') }}
                placeholder={t('login.passwordPlaceholder')}
                required
              />
            </div>
            <button type="submit" className="btn btn-primary login-btn-full" disabled={submitting}>
              {submitting ? t('login.signingIn') : t('login.signIn')}
            </button>
            {!(registrationMode === 'invite' && hasUsers && !urlInviteCode) && (
              <p className="login-footer">
                {t('login.noAccount')}{' '}
                <button type="button" className="login-link" onClick={() => { setMode('register'); setError(''); setMessage('') }}>
                  {t('login.register')}
                </button>
              </p>
            )}
          </form>
        )}

        {hasLocal && mode === 'register' && (
          <form onSubmit={handleRegister}>
            {showInviteField && (
              <div className="form-group">
                <label className="form-label">
                  {t('login.inviteCodeLabel')}{inviteRequired ? '' : t('login.inviteCodeOptional')}
                </label>
                <input
                  className="input"
                  type="text"
                  value={inviteCode}
                  onChange={(e) => { setInviteCode(e.target.value); setError('') }}
                  placeholder={t('login.inviteCodePlaceholder')}
                  required={inviteRequired}
                  readOnly={!!urlInviteCode}
                />
              </div>
            )}
            <div className="form-group">
              <label className="form-label">{t('login.email')}</label>
              <input
                className="input"
                type="email"
                value={email}
                onChange={(e) => { setEmail(e.target.value); setError('') }}
                placeholder={t('login.emailPlaceholder')}
                autoFocus
                required
              />
            </div>
            <div className="form-group">
              <label className="form-label">{t('login.name')}</label>
              <input
                className="input"
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder={t('login.namePlaceholder')}
              />
            </div>
            <div className="form-group">
              <label className="form-label">{t('login.password')}</label>
              <input
                className="input"
                type="password"
                value={password}
                onChange={(e) => { setPassword(e.target.value); setError('') }}
                placeholder={t('login.newPasswordPlaceholder')}
                minLength={8}
                required
              />
            </div>
            <div className="form-group">
              <label className="form-label">{t('login.confirmPassword')}</label>
              <input
                className="input"
                type="password"
                value={confirmPassword}
                onChange={(e) => { setConfirmPassword(e.target.value); setError('') }}
                placeholder={t('login.confirmPasswordPlaceholder')}
                required
              />
            </div>
            <button type="submit" className="btn btn-primary login-btn-full" disabled={submitting}>
              {submitting
                ? t('login.creatingAccount')
                : !hasUsers
                  ? t('login.createAdminAccount')
                  : t('login.register')}
            </button>
            {hasUsers && (
              <p className="login-footer">
                {t('login.hasAccount')}{' '}
                <button type="button" className="login-link" onClick={() => { setMode('login'); setError(''); setMessage('') }}>
                  {t('login.signIn')}
                </button>
              </p>
            )}
          </form>
        )}

        {/* Token login fallback */}
        <div className="login-token-toggle">
          <button
            type="button"
            onClick={() => setShowTokenLogin(!showTokenLogin)}
          >
            {showTokenLogin ? t('login.hideTokenLogin') : t('login.showTokenLogin')}
          </button>
          {showTokenLogin && (
            <form onSubmit={handleTokenLogin} className="login-token-form">
              <div className="form-group">
                <input
                  className="input"
                  type="password"
                  value={token}
                  onChange={(e) => { setToken(e.target.value); setError('') }}
                  placeholder={t('login.tokenAltPlaceholder')}
                />
              </div>
              <button type="submit" className="btn btn-secondary login-btn-full" disabled={submitting}>
                <i className="fas fa-key" /> {t('login.loginWithToken')}
              </button>
            </form>
          )}
        </div>
      </div>
    </div>
  )
}
