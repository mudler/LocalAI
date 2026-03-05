import { useState } from 'react'
import { useNavigate } from 'react-router-dom'

export default function Login() {
  const navigate = useNavigate()
  const [token, setToken] = useState('')
  const [error, setError] = useState('')

  const handleSubmit = (e) => {
    e.preventDefault()
    if (!token.trim()) {
      setError('Please enter a token')
      return
    }
    // Set token as cookie
    document.cookie = `token=${encodeURIComponent(token.trim())}; path=/; SameSite=Strict`
    navigate('/')
  }

  return (
    <div style={{
      minHeight: '100vh',
      background: 'var(--color-bg-primary)',
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      padding: 'var(--spacing-xl)',
    }}>
      <div className="card" style={{ width: '100%', maxWidth: '400px', padding: 'var(--spacing-xl)' }}>
        <div style={{ textAlign: 'center', marginBottom: 'var(--spacing-xl)' }}>
          <img src="/static/logo.png" alt="LocalAI" style={{ width: 64, height: 64, marginBottom: 'var(--spacing-md)' }} />
          <h1 style={{ fontSize: '1.5rem', fontWeight: 700, marginBottom: 'var(--spacing-xs)' }}>
            <span className="text-gradient">LocalAI</span>
          </h1>
          <p style={{ color: 'var(--color-text-secondary)', fontSize: '0.875rem' }}>Enter your API token to continue</p>
        </div>

        <form onSubmit={handleSubmit}>
          <div className="form-group">
            <label className="form-label">API Token</label>
            <input
              className="input"
              type="password"
              value={token}
              onChange={(e) => { setToken(e.target.value); setError('') }}
              placeholder="Enter token..."
              autoFocus
            />
            {error && <p style={{ color: 'var(--color-error)', fontSize: '0.8125rem', marginTop: 'var(--spacing-xs)' }}>{error}</p>}
          </div>
          <button type="submit" className="btn btn-primary" style={{ width: '100%' }}>
            <i className="fas fa-sign-in-alt" /> Login
          </button>
        </form>
      </div>
    </div>
  )
}
