import { useState, useEffect } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'
import { apiUrl } from '../utils/basePath'
import './auth.css'

export default function Login() {
  const navigate = useNavigate()
  const { code: urlInviteCode } = useParams()
  const [searchParams] = useSearchParams()
  const { authEnabled, user, loading: authLoading, refresh } = useAuth()
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
      setError('A valid invite code is required to register')
    }
  }, [searchParams])

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
    if (!authLoading && (!authEnabled || user)) {
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
        setError(extractError(data, 'Login failed'))
        setSubmitting(false)
        return
      }

      await refresh()
    } catch {
      setError('Network error')
      setSubmitting(false)
    }
  }

  const handleRegister = async (e) => {
    e.preventDefault()
    setError('')
    setMessage('')

    if (password !== confirmPassword) {
      setError('Passwords do not match')
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
        setError(extractError(data, 'Registration failed'))
        setSubmitting(false)
        return
      }

      if (data.pending) {
        setMessage(data.message || 'Registration successful, awaiting approval.')
        setSubmitting(false)
        return
      }

      // Full reload so the auth provider picks up the new session cookie
      window.location.href = '/app'
      return
    } catch {
      setError('Network error')
      setSubmitting(false)
    }
  }

  const handleTokenLogin = async (e) => {
    e.preventDefault()
    if (!token.trim()) {
      setError('Please enter a token')
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
        setError(extractError(data, 'Invalid token'))
        setSubmitting(false)
        return
      }

      await refresh()
    } catch {
      setError('Network error')
      setSubmitting(false)
    }
  }

  if (authLoading || statusLoading) return null

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
          <img src={apiUrl('/static/logo.png')} alt="LocalAI" className="login-logo" />
          <p className="login-subtitle">
            {!hasUsers ? 'Create your admin account' : mode === 'register' ? 'Create an account' : 'Sign in to continue'}
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
            <i className="fab fa-github" /> Sign in with GitHub
          </a>
        )}

        {hasOIDC && (
          <a
            href={oidcLoginUrl}
            className="btn btn-primary login-btn-full"
          >
            <i className="fas fa-sign-in-alt" /> Sign in with SSO
          </a>
        )}

        {hasOAuth && hasLocal && (
          <div className="login-divider">or</div>
        )}

        {hasLocal && mode === 'login' && (
          <form onSubmit={handleEmailLogin}>
            <div className="form-group">
              <label className="form-label">Email</label>
              <input
                className="input"
                type="email"
                value={email}
                onChange={(e) => { setEmail(e.target.value); setError('') }}
                placeholder="you@example.com"
                autoFocus={!hasGitHub}
                required
              />
            </div>
            <div className="form-group">
              <label className="form-label">Password</label>
              <input
                className="input"
                type="password"
                value={password}
                onChange={(e) => { setPassword(e.target.value); setError('') }}
                placeholder="Enter password..."
                required
              />
            </div>
            <button type="submit" className="btn btn-primary login-btn-full" disabled={submitting}>
              {submitting ? 'Signing in...' : 'Sign In'}
            </button>
            {!(registrationMode === 'invite' && hasUsers && !urlInviteCode) && (
              <p className="login-footer">
                Don't have an account?{' '}
                <button type="button" className="login-link" onClick={() => { setMode('register'); setError(''); setMessage('') }}>
                  Register
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
                  Invite Code{inviteRequired ? '' : ' (optional — skip the approval wait)'}
                </label>
                <input
                  className="input"
                  type="text"
                  value={inviteCode}
                  onChange={(e) => { setInviteCode(e.target.value); setError('') }}
                  placeholder="Paste your invite code..."
                  required={inviteRequired}
                  readOnly={!!urlInviteCode}
                />
              </div>
            )}
            <div className="form-group">
              <label className="form-label">Email</label>
              <input
                className="input"
                type="email"
                value={email}
                onChange={(e) => { setEmail(e.target.value); setError('') }}
                placeholder="you@example.com"
                autoFocus
                required
              />
            </div>
            <div className="form-group">
              <label className="form-label">Name</label>
              <input
                className="input"
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="Your name (optional)"
              />
            </div>
            <div className="form-group">
              <label className="form-label">Password</label>
              <input
                className="input"
                type="password"
                value={password}
                onChange={(e) => { setPassword(e.target.value); setError('') }}
                placeholder="At least 8 characters"
                minLength={8}
                required
              />
            </div>
            <div className="form-group">
              <label className="form-label">Confirm Password</label>
              <input
                className="input"
                type="password"
                value={confirmPassword}
                onChange={(e) => { setConfirmPassword(e.target.value); setError('') }}
                placeholder="Repeat password"
                required
              />
            </div>
            <button type="submit" className="btn btn-primary login-btn-full" disabled={submitting}>
              {submitting ? 'Creating account...' : !hasUsers ? 'Create Admin Account' : 'Register'}
            </button>
            {hasUsers && (
              <p className="login-footer">
                Already have an account?{' '}
                <button type="button" className="login-link" onClick={() => { setMode('login'); setError(''); setMessage('') }}>
                  Sign in
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
            {showTokenLogin ? 'Hide token login' : 'Login with API Token'}
          </button>
          {showTokenLogin && (
            <form onSubmit={handleTokenLogin} className="login-token-form">
              <div className="form-group">
                <input
                  className="input"
                  type="password"
                  value={token}
                  onChange={(e) => { setToken(e.target.value); setError('') }}
                  placeholder="Enter API token..."
                />
              </div>
              <button type="submit" className="btn btn-secondary login-btn-full" disabled={submitting}>
                <i className="fas fa-key" /> Login with Token
              </button>
            </form>
          )}
        </div>
      </div>
    </div>
  )
}
