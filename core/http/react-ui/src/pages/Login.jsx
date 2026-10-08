import { useState, useEffect } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../context/AuthContext'
import { useBranding } from '../contexts/BrandingContext'
import { apiUrl } from '../utils/basePath'
import './login.css'
import Icon from '../components/Icon'

// Sign-in, registration and the invite page: one screen, one field per step.
// What it offers comes from /api/auth/status and nothing else: the providers
// that list names (local, GitHub, OIDC), whether anyone has registered yet,
// and the registration mode. A key-only server (static API keys, no accounts)
// gets one field for the key.
//
// Steps are the page's own: signing in asks for the email, then the password,
// and sends both in one call, as before. Registering asks for the email, then
// the rest. The server never says whether an email exists.
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

  const [mode, setMode] = useState('login') // 'login' or 'register'
  const [step, setStep] = useState(1)
  const [email, setEmail] = useState('')
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [inviteCode, setInviteCode] = useState('')
  const [error, setError] = useState('')
  const [pending, setPending] = useState(null) // { text } once an account waits for approval
  const [submitting, setSubmitting] = useState(false)
  // The server's rejection of a weak password that may be overridden: the form
  // then shows an acknowledgement, and the next submit sends it.
  const [weakPasswordWarning, setWeakPasswordWarning] = useState('')
  const [acknowledgeWeakPassword, setAcknowledgeWeakPassword] = useState(false)
  const [showTokenLogin, setShowTokenLogin] = useState(false)
  const [token, setToken] = useState('')

  const extractError = (data, fallback) => {
    if (!data) return fallback
    if (typeof data.error === 'string') return data.error
    if (data.error && typeof data.error === 'object') return data.error.message || fallback
    if (typeof data.message === 'string') return data.message
    return fallback
  }

  // The invite link fills in the code and opens registration.
  useEffect(() => {
    if (urlInviteCode) {
      setInviteCode(urlInviteCode)
      setMode('register')
    }
  }, [urlInviteCode])

  // An OAuth redirect can come back with an error (invite_required).
  useEffect(() => {
    if (searchParams.get('error') === 'invite_required') setError(t('login.errors.inviteRequired'))
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

  // Nothing to sign in to, or already signed in: go to the app.
  useEffect(() => {
    if (!authLoading && ((!authEnabled && !staticApiKeyRequired) || user)) {
      navigate('/app', { replace: true })
    }
  }, [authLoading, authEnabled, staticApiKeyRequired, user, navigate])

  const goMode = (next) => {
    setMode(next); setStep(1); setError(''); setPending(null)
    setPassword(''); setConfirmPassword('')
  }

  const handleEmailLogin = async (e) => {
    e.preventDefault()
    setError('')
    setPending(null)
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
    setPending(null)
    if (password !== confirmPassword) {
      setError(t('login.errors.passwordsDoNotMatch'))
      return
    }
    setSubmitting(true)
    try {
      const body = { email, password, name }
      if (inviteCode) body.inviteCode = inviteCode
      if (acknowledgeWeakPassword) body.acknowledge_weak_password = true
      const res = await fetch(apiUrl('/api/auth/register'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      })
      const data = await res.json()
      if (!res.ok) {
        if (data && data.overridable) {
          setWeakPasswordWarning(extractError(data, ''))
          setError('')
        } else {
          setError(extractError(data, t('login.errors.registrationFailed')))
          setWeakPasswordWarning('')
          setAcknowledgeWeakPassword(false)
        }
        setSubmitting(false)
        return
      }
      setWeakPasswordWarning('')
      if (data.pending) {
        // The account waits for an admin. Say so on the sign-in screen.
        setPending({ text: data.message || '' })
        setMode('login'); setStep(1); setPassword(''); setConfirmPassword('')
        setSubmitting(false)
        return
      }
      // Full reload so the auth provider picks up the new session cookie.
      window.location.href = '/app'
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

  const Brand = (
    <aside className="lg-brand" aria-label={branding.instanceName}>
      <div className="lg-brand__top">
        <img src={apiUrl(branding.logoUrl)} alt="" className="lg-brand__logo" />
        <span className="lg-brand__name">{branding.instanceName}</span>
      </div>
      {branding.instanceTagline && <p className="lg-brand__tagline">{branding.instanceTagline}</p>}
    </aside>
  )

  const Alert = error ? <p className="lg-alert" role="alert" data-tone="error">{error}</p> : null

  // Legacy key-only mode: one field for the key.
  if (staticApiKeyRequired && !authEnabled) {
    return (
      <div className="lg" data-screen="api-key-only">
        {Brand}
        <main className="lg-main">
          <form className="lg-form" onSubmit={handleTokenLogin}>
            <h1 className="lg-title">{t('login.tokenTitle')}</h1>
            <p className="lg-lead">{t('login.tokenLead')}</p>
            {Alert}
            <div className="dk-field">
              <label className="dk-label" htmlFor="lg-key">{t('login.apiKeyLabel')}</label>
              <input
                id="lg-key" className="dk-input dk-input--mono" type="password" value={token} autoFocus autoComplete="off"
                placeholder={t('login.tokenPlaceholder')} onChange={(e) => { setToken(e.target.value); setError('') }}
              />
            </div>
            <button type="submit" className="dk-btn dk-btn--primary dk-btn--lg lg-full" disabled={submitting}>
              {submitting ? t('login.signingIn') : t('login.signIn')}
            </button>
          </form>
        </main>
      </div>
    )
  }

  const hasGitHub = providers.includes('github')
  const hasOIDC = providers.includes('oidc')
  const hasLocal = providers.includes('local')
  const hasOAuth = hasGitHub || hasOIDC
  const showInviteField = (registrationMode === 'invite' || registrationMode === 'approval') && mode === 'register' && hasUsers
  const inviteRequired = registrationMode === 'invite' && hasUsers
  const firstAdmin = !hasUsers
  const joining = !!urlInviteCode

  const githubLoginUrl = inviteCode ? apiUrl(`/api/auth/github/login?invite_code=${encodeURIComponent(inviteCode)}`) : apiUrl('/api/auth/github/login')
  const oidcLoginUrl = inviteCode ? apiUrl(`/api/auth/oidc/login?invite_code=${encodeURIComponent(inviteCode)}`) : apiUrl('/api/auth/oidc/login')

  const title = firstAdmin ? t('login.createAdminTitle')
    : joining ? t('login.joinTitle', { name: branding.instanceName })
    : mode === 'register' ? t('login.registerTitle') : t('login.title')
  const lead = firstAdmin ? t('login.createAdminLead')
    : joining ? t('login.inviteLead')
    : mode === 'register' ? t('login.registerSubtitle') : t('login.toInstance', { name: branding.instanceName })
  const screen = firstAdmin ? 'first-admin' : joining ? 'invite' : mode === 'register' ? 'register' : 'sign-in'
  const stepsOn = hasLocal && !showTokenLogin

  return (
    <div className="lg" data-screen={screen} data-step={hasLocal ? step : undefined}>
      {Brand}
      <main className="lg-main">
        <div className="lg-form">
          {stepsOn && (
            <div className="lg-steps" role="img" aria-label={`Step ${step} of 2`}>
              <span data-on /><span data-on={step === 2 || undefined} />
            </div>
          )}
          <h1 className="lg-title">{title}</h1>
          <p className="lg-lead">{lead}</p>

          {pending && (
            <div className="lg-alert" role="status" data-tone="pending" data-testid="login-pending">
              <Icon name="clock" aria-hidden="true" />
              <p><strong>{t('login.pendingTitle')}</strong> {pending.text || t('login.pendingText')}</p>
            </div>
          )}
          {Alert}

          {step === 1 && hasGitHub && (
            <a href={githubLoginUrl} className="dk-btn dk-btn--secondary dk-btn--lg lg-full">
              <Icon name="github" /> {t('login.continueWith', { provider: 'GitHub' })}
            </a>
          )}
          {step === 1 && hasOIDC && (
            <a href={oidcLoginUrl} className="dk-btn dk-btn--secondary dk-btn--lg lg-full">
              <Icon name="log-in" /> {t('login.continueWith', { provider: 'SSO' })}
            </a>
          )}
          {step === 1 && hasOAuth && hasLocal && <div className="lg-divider" role="separator"><span>{t('login.or')}</span></div>}

          {hasLocal && mode === 'login' && step === 1 && (
            <form onSubmit={(e) => { e.preventDefault(); if (email.trim()) { setError(''); setStep(2) } }}>
              <div className="dk-field">
                <label className="dk-label" htmlFor="lg-email">{t('login.email')}</label>
                <input id="lg-email" className="dk-input" type="email" value={email} required autoComplete="username" autoFocus={!hasOAuth}
                  placeholder={t('login.emailPlaceholder')} onChange={(e) => { setEmail(e.target.value); setError('') }} />
              </div>
              <button type="submit" className="dk-btn dk-btn--primary dk-btn--lg lg-full">{t('login.continue')}</button>
            </form>
          )}

          {hasLocal && mode === 'login' && step === 2 && (
            <form onSubmit={handleEmailLogin}>
              <p className="lg-as">{t('login.signInAs', { email })} <button type="button" className="lg-link" onClick={() => { setStep(1); setError('') }}>{t('login.changeEmail')}</button></p>
              <div className="dk-field">
                <label className="dk-label" htmlFor="lg-password">{t('login.password')}</label>
                <input id="lg-password" className="dk-input" type="password" value={password} required autoFocus autoComplete="current-password"
                  placeholder={t('login.passwordPlaceholder')} onChange={(e) => { setPassword(e.target.value); setError('') }} />
              </div>
              <button type="submit" className="dk-btn dk-btn--primary dk-btn--lg lg-full" disabled={submitting}>
                {submitting ? t('login.signingIn') : t('login.signIn')}
              </button>
              <button type="button" className="dk-btn dk-btn--ghost lg-full" onClick={() => { setStep(1); setError('') }}>{t('login.back')}</button>
            </form>
          )}

          {hasLocal && mode === 'register' && step === 1 && (
            <form onSubmit={(e) => { e.preventDefault(); if (email.trim()) { setError(''); setStep(2) } }}>
              <div className="dk-field">
                <label className="dk-label" htmlFor="lg-email">{t('login.email')}</label>
                <input id="lg-email" className="dk-input" type="email" value={email} required autoComplete="username" autoFocus={!hasOAuth}
                  placeholder={t('login.emailPlaceholder')} onChange={(e) => { setEmail(e.target.value); setError('') }} />
              </div>
              <button type="submit" className="dk-btn dk-btn--primary dk-btn--lg lg-full">{t('login.continue')}</button>
            </form>
          )}

          {hasLocal && mode === 'register' && step === 2 && (
            <form onSubmit={handleRegister}>
              <p className="lg-as">{email} <button type="button" className="lg-link" onClick={() => { setStep(1); setError('') }}>{t('login.changeEmail')}</button></p>
              {showInviteField && (
                <div className="dk-field">
                  <label className="dk-label" htmlFor="lg-invite">
                    {t('login.inviteCodeLabel')}{inviteRequired || urlInviteCode ? '' : t('login.inviteCodeOptional')}
                  </label>
                  <input id="lg-invite" className="dk-input dk-input--mono" type="text" value={inviteCode} required={inviteRequired} readOnly={!!urlInviteCode}
                    placeholder={t('login.inviteCodePlaceholder')} onChange={(e) => { setInviteCode(e.target.value); setError('') }} />
                  {urlInviteCode && <p className="dk-hint">{t('login.inviteFilled')}</p>}
                </div>
              )}
              <div className="dk-field">
                <label className="dk-label" htmlFor="lg-name" data-optional>{t('login.name')}</label>
                <input id="lg-name" className="dk-input" type="text" value={name} autoFocus placeholder={t('login.namePlaceholder')} autoComplete="name" onChange={(e) => setName(e.target.value)} />
              </div>
              <div className="dk-field">
                <label className="dk-label" htmlFor="lg-newpw">{t('login.password')}</label>
                <input id="lg-newpw" className="dk-input" type="password" value={password} required autoComplete="new-password" placeholder={t('login.newPasswordPlaceholder')}
                  onChange={(e) => { setPassword(e.target.value); setError(''); setWeakPasswordWarning(''); setAcknowledgeWeakPassword(false) }} />
              </div>
              <div className="dk-field">
                <label className="dk-label" htmlFor="lg-confirm">{t('login.confirmPassword')}</label>
                <input id="lg-confirm" className="dk-input" type="password" value={confirmPassword} required autoComplete="new-password" placeholder={t('login.confirmPasswordPlaceholder')}
                  onChange={(e) => { setConfirmPassword(e.target.value); setError('') }} />
              </div>
              {weakPasswordWarning && (
                <div className="lg-alert" role="alert" data-tone="warn">
                  <p>{weakPasswordWarning}</p>
                  <label className="dk-choice">
                    <input type="checkbox" className="dk-check" checked={acknowledgeWeakPassword} onChange={(e) => setAcknowledgeWeakPassword(e.target.checked)} />
                    {t('login.useAnyway')}
                  </label>
                </div>
              )}
              <button type="submit" className="dk-btn dk-btn--primary dk-btn--lg lg-full" disabled={submitting}>
                {submitting ? t('login.creatingAccount') : firstAdmin ? t('login.createAdminAccount') : t('login.register')}
              </button>
              <button type="button" className="dk-btn dk-btn--ghost lg-full" onClick={() => { setStep(1); setError('') }}>{t('login.back')}</button>
            </form>
          )}

          {hasLocal && step === 1 && mode === 'login' && !(registrationMode === 'invite' && hasUsers && !urlInviteCode) && hasUsers && (
            <p className="lg-foot">
              {t('login.noAccount')}{' '}
              <button type="button" className="lg-link" onClick={() => goMode('register')}>{t('login.register')}</button>
            </p>
          )}
          {hasLocal && hasUsers && mode === 'register' && step === 1 && (
            <p className="lg-foot">
              {t('login.hasAccount')}{' '}
              <button type="button" className="lg-link" onClick={() => goMode('login')}>{t('login.signIn')}</button>
            </p>
          )}

          {/* Token login stays available whatever else is on the screen. */}
          <div className="lg-token">
            <button type="button" className="lg-link" aria-expanded={showTokenLogin} onClick={() => setShowTokenLogin(!showTokenLogin)}>
              {showTokenLogin ? t('login.hideTokenLogin') : t('login.showTokenLogin')}
            </button>
            {showTokenLogin && (
              <form onSubmit={handleTokenLogin} className="lg-token__form">
                <div className="dk-field">
                  <label className="dk-label" htmlFor="lg-token">{t('login.apiKeyLabel')}</label>
                  <input id="lg-token" className="dk-input dk-input--mono" type="password" value={token} autoComplete="off" placeholder={t('login.tokenAltPlaceholder')}
                    onChange={(e) => { setToken(e.target.value); setError('') }} />
                </div>
                <button type="submit" className="dk-btn dk-btn--secondary lg-full" disabled={submitting}>
                  <Icon name="key" /> {t('login.loginWithToken')}
                </button>
              </form>
            )}
          </div>
        </div>
      </main>
    </div>
  )
}
