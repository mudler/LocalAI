import { useState, useEffect, useCallback } from 'react'
import { useOutletContext } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'
import { apiKeysApi, profileApi } from '../utils/api'
import LoadingSpinner from '../components/LoadingSpinner'

function formatDate(d) {
  if (!d) return '-'
  return new Date(d).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

function ProfileSection({ addToast }) {
  const { user, refresh } = useAuth()
  const [name, setName] = useState(user?.name || '')
  const [avatarUrl, setAvatarUrl] = useState(user?.avatarUrl || '')
  const [saving, setSaving] = useState(false)

  useEffect(() => { if (user?.name) setName(user.name) }, [user?.name])
  useEffect(() => { setAvatarUrl(user?.avatarUrl || '') }, [user?.avatarUrl])

  const hasChanges = (name.trim() && name.trim() !== user?.name) || (avatarUrl.trim() !== (user?.avatarUrl || ''))

  const handleSave = async (e) => {
    e.preventDefault()
    if (!name.trim() || !hasChanges) return
    setSaving(true)
    try {
      await profileApi.updateProfile(name.trim(), avatarUrl.trim())
      addToast('Profile updated', 'success')
      refresh()
    } catch (err) {
      addToast(`Failed to update profile: ${err.message}`, 'error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-md)' }}>
        <div style={{
          width: 48, height: 48, borderRadius: '50%',
          background: 'var(--color-primary-light)',
          border: '2px solid var(--color-primary-border)',
          display: 'flex', alignItems: 'center', justifyContent: 'center',
          flexShrink: 0,
          overflow: 'hidden',
        }}>
          {user?.avatarUrl ? (
            <img src={user.avatarUrl} alt="" style={{ width: 44, height: 44, borderRadius: '50%', objectFit: 'cover' }} />
          ) : (
            <i className="fas fa-user" style={{ fontSize: '1.125rem', color: 'var(--color-primary)' }} />
          )}
        </div>
        <div style={{ minWidth: 0 }}>
          <div style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)', marginBottom: 2 }}>{user?.email}</div>
          <div style={{ display: 'flex', gap: 'var(--spacing-xs)', alignItems: 'center' }}>
            <span style={{
              fontSize: '0.6875rem', fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em',
              padding: '1px 6px', borderRadius: 'var(--radius-sm)',
              background: user?.role === 'admin' ? 'var(--color-accent-light)' : 'var(--color-primary-light)',
              color: user?.role === 'admin' ? 'var(--color-accent)' : 'var(--color-primary)',
            }}>
              {user?.role}
            </span>
            <span style={{
              fontSize: '0.6875rem', color: 'var(--color-text-muted)',
              padding: '1px 6px', borderRadius: 'var(--radius-sm)',
              background: 'var(--color-bg-primary)',
            }}>
              {user?.provider || 'local'}
            </span>
          </div>
        </div>
      </div>

      <form onSubmit={handleSave} style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-sm)' }}>
        <div>
          <label style={{ display: 'block', fontSize: '0.75rem', fontWeight: 500, color: 'var(--color-text-secondary)', marginBottom: 4 }}>
            Display name
          </label>
          <input
            type="text"
            className="input"
            value={name}
            onChange={(e) => setName(e.target.value)}
            disabled={saving}
            maxLength={100}
          />
        </div>
        <div>
          <label style={{ display: 'block', fontSize: '0.75rem', fontWeight: 500, color: 'var(--color-text-secondary)', marginBottom: 4 }}>
            Avatar URL
          </label>
          <div style={{ display: 'flex', gap: 'var(--spacing-sm)', alignItems: 'center' }}>
            <input
              type="url"
              className="input"
              value={avatarUrl}
              onChange={(e) => setAvatarUrl(e.target.value)}
              disabled={saving}
              maxLength={512}
              placeholder="https://example.com/avatar.png"
              style={{ flex: 1 }}
            />
            {avatarUrl.trim() && (
              <img
                src={avatarUrl.trim()}
                alt="preview"
                style={{
                  width: 32, height: 32, borderRadius: '50%', objectFit: 'cover',
                  border: '1px solid var(--color-border)',
                  flexShrink: 0,
                }}
                onError={(e) => { e.target.style.display = 'none' }}
                onLoad={(e) => { e.target.style.display = 'block' }}
              />
            )}
          </div>
        </div>
        <div>
          <button
            type="submit"
            className="btn btn-primary btn-sm"
            disabled={saving || !name.trim() || !hasChanges}
          >
            {saving ? <LoadingSpinner size="sm" /> : 'Save'}
          </button>
        </div>
      </form>
    </div>
  )
}

function PasswordSection({ addToast }) {
  const [currentPw, setCurrentPw] = useState('')
  const [newPw, setNewPw] = useState('')
  const [confirmPw, setConfirmPw] = useState('')
  const [saving, setSaving] = useState(false)

  const handleSubmit = async (e) => {
    e.preventDefault()
    if (newPw !== confirmPw) {
      addToast('Passwords do not match', 'error')
      return
    }
    if (newPw.length < 8) {
      addToast('New password must be at least 8 characters', 'error')
      return
    }
    setSaving(true)
    try {
      await profileApi.changePassword(currentPw, newPw)
      addToast('Password changed', 'success')
      setCurrentPw('')
      setNewPw('')
      setConfirmPw('')
    } catch (err) {
      addToast(err.message, 'error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <form onSubmit={handleSubmit} style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-sm)' }}>
      <div>
        <label style={{ display: 'block', fontSize: '0.75rem', fontWeight: 500, color: 'var(--color-text-secondary)', marginBottom: 4 }}>
          Current password
        </label>
        <input
          type="password"
          className="input"
          value={currentPw}
          onChange={(e) => setCurrentPw(e.target.value)}
          placeholder="Enter current password"
          disabled={saving}
          required
        />
      </div>
      <div style={{ display: 'flex', gap: 'var(--spacing-sm)' }}>
        <div style={{ flex: 1 }}>
          <label style={{ display: 'block', fontSize: '0.75rem', fontWeight: 500, color: 'var(--color-text-secondary)', marginBottom: 4 }}>
            New password
          </label>
          <input
            type="password"
            className="input"
            value={newPw}
            onChange={(e) => setNewPw(e.target.value)}
            placeholder="At least 8 characters"
            minLength={8}
            disabled={saving}
            required
          />
        </div>
        <div style={{ flex: 1 }}>
          <label style={{ display: 'block', fontSize: '0.75rem', fontWeight: 500, color: 'var(--color-text-secondary)', marginBottom: 4 }}>
            Confirm
          </label>
          <input
            type="password"
            className="input"
            value={confirmPw}
            onChange={(e) => setConfirmPw(e.target.value)}
            placeholder="Repeat new password"
            disabled={saving}
            required
          />
        </div>
      </div>
      <div>
        <button
          type="submit"
          className="btn btn-primary btn-sm"
          disabled={saving || !currentPw || !newPw || !confirmPw}
        >
          {saving ? <><LoadingSpinner size="sm" /> Changing...</> : 'Change password'}
        </button>
      </div>
    </form>
  )
}

function ApiKeysSection({ addToast }) {
  const [keys, setKeys] = useState([])
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [newKeyName, setNewKeyName] = useState('')
  const [newKeyPlaintext, setNewKeyPlaintext] = useState(null)
  const [revokingId, setRevokingId] = useState(null)

  const fetchKeys = useCallback(async () => {
    setLoading(true)
    try {
      const data = await apiKeysApi.list()
      setKeys(data.keys || [])
    } catch (err) {
      addToast(`Failed to load API keys: ${err.message}`, 'error')
    } finally {
      setLoading(false)
    }
  }, [addToast])

  useEffect(() => { fetchKeys() }, [fetchKeys])

  const handleCreate = async (e) => {
    e.preventDefault()
    if (!newKeyName.trim()) return
    setCreating(true)
    try {
      const data = await apiKeysApi.create(newKeyName.trim())
      setNewKeyPlaintext(data.key)
      setNewKeyName('')
      await fetchKeys()
      addToast('API key created', 'success')
    } catch (err) {
      addToast(`Failed to create API key: ${err.message}`, 'error')
    } finally {
      setCreating(false)
    }
  }

  const handleRevoke = async (id, name) => {
    if (!window.confirm(`Revoke API key "${name}"? This cannot be undone.`)) return
    setRevokingId(id)
    try {
      await apiKeysApi.revoke(id)
      setKeys(prev => prev.filter(k => k.id !== id))
      addToast('API key revoked', 'success')
    } catch (err) {
      addToast(`Failed to revoke API key: ${err.message}`, 'error')
    } finally {
      setRevokingId(null)
    }
  }

  const copyToClipboard = (text) => {
    if (navigator.clipboard?.writeText) {
      navigator.clipboard.writeText(text).then(
        () => addToast('Copied to clipboard', 'success'),
        () => fallbackCopy(text),
      )
    } else {
      fallbackCopy(text)
    }
  }

  const fallbackCopy = (text) => {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    try {
      document.execCommand('copy')
      addToast('Copied to clipboard', 'success')
    } catch (_) {
      addToast('Failed to copy', 'error')
    }
    document.body.removeChild(ta)
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)' }}>
      {/* Create key form */}
      <form onSubmit={handleCreate} style={{ display: 'flex', gap: 'var(--spacing-sm)', alignItems: 'flex-end' }}>
        <div style={{ flex: 1 }}>
          <label style={{ display: 'block', fontSize: '0.75rem', fontWeight: 500, color: 'var(--color-text-secondary)', marginBottom: 4 }}>
            New key
          </label>
          <input
            type="text"
            className="input"
            placeholder="Key name (e.g. my-app, ci-pipeline)"
            value={newKeyName}
            onChange={(e) => setNewKeyName(e.target.value)}
            disabled={creating}
            maxLength={64}
          />
        </div>
        <button type="submit" className="btn btn-primary btn-sm" disabled={creating || !newKeyName.trim()}>
          {creating ? <LoadingSpinner size="sm" /> : <><i className="fas fa-plus" /> Create</>}
        </button>
      </form>

      {/* Newly created key banner */}
      {newKeyPlaintext && (
        <div style={{
          padding: 'var(--spacing-sm) var(--spacing-md)',
          border: '1px solid var(--color-warning-border)',
          borderRadius: 'var(--radius-md)',
          background: 'var(--color-warning-light)',
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)', marginBottom: 'var(--spacing-xs)', fontSize: '0.75rem', fontWeight: 600, color: 'var(--color-warning)' }}>
            <i className="fas fa-triangle-exclamation" />
            Copy now — this key won't be shown again
          </div>
          <div style={{ display: 'flex', gap: 'var(--spacing-sm)', alignItems: 'center' }}>
            <code style={{
              flex: 1, padding: 'var(--spacing-xs) var(--spacing-sm)',
              background: 'var(--color-bg-primary)', borderRadius: 'var(--radius-sm)',
              fontFamily: 'JetBrains Mono, monospace', fontSize: '0.75rem',
              wordBreak: 'break-all', color: 'var(--color-text-primary)',
            }}>
              {newKeyPlaintext}
            </code>
            <button className="btn btn-secondary btn-sm" onClick={() => copyToClipboard(newKeyPlaintext)}>
              <i className="fas fa-copy" />
            </button>
            <button className="btn btn-secondary btn-sm" onClick={() => setNewKeyPlaintext(null)}>
              <i className="fas fa-times" />
            </button>
          </div>
        </div>
      )}

      {/* Keys list */}
      {loading ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-md)' }}>
          <LoadingSpinner size="sm" />
        </div>
      ) : keys.length === 0 ? (
        <div style={{ textAlign: 'center', padding: 'var(--spacing-md)', color: 'var(--color-text-muted)', fontSize: '0.8125rem' }}>
          No API keys yet. Create one above to get programmatic access.
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
          {keys.map(k => (
            <div key={k.id} style={{
              display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)',
              padding: 'var(--spacing-xs) var(--spacing-sm)',
              borderRadius: 'var(--radius-sm)',
              background: 'var(--color-bg-primary)',
            }}>
              <i className="fas fa-key" style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)', width: 16, textAlign: 'center' }} />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: '0.8125rem', fontWeight: 500, color: 'var(--color-text-primary)' }}>{k.name}</div>
                <div style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)', fontFamily: 'JetBrains Mono, monospace' }}>
                  {k.keyPrefix}... &middot; {formatDate(k.createdAt)}
                  {k.lastUsed && <> &middot; last used {formatDate(k.lastUsed)}</>}
                </div>
              </div>
              <button
                className="btn btn-sm"
                style={{ color: 'var(--color-error)', padding: '2px 6px' }}
                onClick={() => handleRevoke(k.id, k.name)}
                disabled={revokingId === k.id}
                title="Revoke key"
              >
                {revokingId === k.id ? <LoadingSpinner size="sm" /> : <i className="fas fa-trash" style={{ fontSize: '0.6875rem' }} />}
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

export default function Account() {
  const { addToast } = useOutletContext()
  const { authEnabled, user } = useAuth()

  if (!authEnabled) {
    return (
      <div className="page">
        <div className="empty-state">
          <div className="empty-state-icon"><i className="fas fa-user-gear" /></div>
          <h2 className="empty-state-title">Account unavailable</h2>
          <p className="empty-state-text">Authentication must be enabled to manage your account.</p>
        </div>
      </div>
    )
  }

  const isLocal = user?.provider === 'local'

  const sectionHeader = (icon, title) => (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 6,
      fontSize: '0.6875rem', fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.05em',
      color: 'var(--color-text-muted)', marginBottom: 'var(--spacing-sm)',
      paddingBottom: 'var(--spacing-xs)', borderBottom: '1px solid var(--color-border-subtle)',
    }}>
      <i className={icon} style={{ fontSize: '0.625rem', opacity: 0.7 }} />
      {title}
    </div>
  )

  return (
    <div className="page">
      <div className="page-header" style={{ marginBottom: 'var(--spacing-sm)' }}>
        <h1 className="page-title">Account</h1>
        <p className="page-subtitle">Profile, credentials, and API keys</p>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)', maxWidth: 640 }}>
        <div className="card" style={{ padding: 'var(--spacing-md)' }}>
          {sectionHeader('fas fa-user', 'Profile')}
          <ProfileSection addToast={addToast} />
        </div>

        {isLocal && (
          <div className="card" style={{ padding: 'var(--spacing-md)' }}>
            {sectionHeader('fas fa-lock', 'Password')}
            <PasswordSection addToast={addToast} />
          </div>
        )}

        <div className="card" style={{ padding: 'var(--spacing-md)' }}>
          {sectionHeader('fas fa-key', 'API keys')}
          <ApiKeysSection addToast={addToast} />
        </div>
      </div>
    </div>
  )
}
