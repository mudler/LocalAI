import { useState, useEffect, useCallback } from 'react'
import { useOutletContext } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'
import { apiKeysApi, profileApi } from '../utils/api'
import LoadingSpinner from '../components/LoadingSpinner'
import SettingRow from '../components/SettingRow'
import ConfirmDialog from '../components/ConfirmDialog'
import './auth.css'

function formatDate(d) {
  if (!d) return '-'
  return new Date(d).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

const TABS = [
  { id: 'profile', icon: 'fa-user', label: 'Profile' },
  { id: 'security', icon: 'fa-lock', label: 'Security' },
  { id: 'apikeys', icon: 'fa-key', label: 'API Keys' },
]

function ProfileTab({ addToast }) {
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
    <div>
      {/* User info header */}
      <div className="account-user-header">
        <div className="account-avatar-frame">
          {user?.avatarUrl ? (
            <img src={user.avatarUrl} alt="" className="user-avatar--lg" />
          ) : (
            <i className="fas fa-user account-avatar-icon" />
          )}
        </div>
        <div className="account-user-meta">
          <div className="account-user-email">{user?.email}</div>
          <div className="account-user-badges">
            <span className={`role-badge ${user?.role === 'admin' ? 'role-badge-admin' : 'role-badge-user'}`}>
              {user?.role}
            </span>
            <span className="provider-tag">
              {user?.provider || 'local'}
            </span>
          </div>
        </div>
      </div>

      {/* Profile form */}
      <form onSubmit={handleSave}>
        <div className="card">
          <SettingRow label="Display name" description="Your public display name">
            <input
              type="text"
              className="input account-input-sm"
              value={name}
              onChange={(e) => setName(e.target.value)}
              disabled={saving}
              maxLength={100}
            />
          </SettingRow>
          <SettingRow label="Avatar URL" description="URL to your profile picture">
            <div className="account-input-row">
              <input
                type="url"
                className="input account-input-sm"
                value={avatarUrl}
                onChange={(e) => setAvatarUrl(e.target.value)}
                disabled={saving}
                maxLength={512}
                placeholder="https://example.com/avatar.png"
              />
              {avatarUrl.trim() && (
                <img
                  src={avatarUrl.trim()}
                  alt="preview"
                  className="account-avatar-preview"
                  onError={(e) => { e.target.style.display = 'none' }}
                  onLoad={(e) => { e.target.style.display = 'block' }}
                />
              )}
            </div>
          </SettingRow>
        </div>
        <div className="form-actions">
          <button
            type="submit"
            className="btn btn-primary btn-sm"
            disabled={saving || !name.trim() || !hasChanges}
          >
            {saving ? <><LoadingSpinner size="sm" /> Saving...</> : <><i className="fas fa-save" /> Save</>}
          </button>
        </div>
      </form>
    </div>
  )
}

function SecurityTab({ addToast }) {
  const { user } = useAuth()
  const isLocal = user?.provider === 'local'

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

  if (!isLocal) {
    return (
      <div className="card empty-icon-block">
        <i className="fas fa-shield-halved" />
        <div className="empty-icon-block-text">
          Password management is not available for {user?.provider || 'OAuth'} accounts.
        </div>
      </div>
    )
  }

  return (
    <form onSubmit={handleSubmit}>
      <div className="card">
        <SettingRow label="Current password" description="Enter your existing password to verify your identity">
          <input
            type="password"
            className="input account-input-sm"
            value={currentPw}
            onChange={(e) => setCurrentPw(e.target.value)}
            placeholder="Current password"
            disabled={saving}
            required
          />
        </SettingRow>
        <SettingRow label="New password" description="Must be at least 8 characters">
          <input
            type="password"
            className="input account-input-sm"
            value={newPw}
            onChange={(e) => setNewPw(e.target.value)}
            placeholder="New password"
            minLength={8}
            disabled={saving}
            required
          />
        </SettingRow>
        <SettingRow label="Confirm password" description="Re-enter your new password">
          <input
            type="password"
            className="input account-input-sm"
            value={confirmPw}
            onChange={(e) => setConfirmPw(e.target.value)}
            placeholder="Confirm new password"
            disabled={saving}
            required
          />
        </SettingRow>
      </div>
      <div className="form-actions">
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

function ApiKeysTab({ addToast }) {
  const [keys, setKeys] = useState([])
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [newKeyName, setNewKeyName] = useState('')
  const [newKeyPlaintext, setNewKeyPlaintext] = useState(null)
  const [revokingId, setRevokingId] = useState(null)
  const [confirmDialog, setConfirmDialog] = useState(null)

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
    setConfirmDialog({
      title: 'Revoke API Key',
      message: `Revoke API key "${name}"? This cannot be undone.`,
      confirmLabel: 'Revoke',
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
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
      },
    })
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
    <div>
      {/* Create key form */}
      <div className="card" style={{ marginBottom: 'var(--spacing-md)' }}>
        <form onSubmit={handleCreate}>
          <SettingRow label="Create API key" description="Generate a key for programmatic access">
            <div className="account-input-row">
              <input
                type="text"
                className="input account-input-xs"
                placeholder="Key name (e.g. my-app)"
                value={newKeyName}
                onChange={(e) => setNewKeyName(e.target.value)}
                disabled={creating}
                maxLength={64}
              />
              <button type="submit" className="btn btn-primary btn-sm" disabled={creating || !newKeyName.trim()}>
                {creating ? <LoadingSpinner size="sm" /> : <><i className="fas fa-plus" /> Create</>}
              </button>
            </div>
          </SettingRow>
        </form>
      </div>

      {/* Newly created key banner */}
      {newKeyPlaintext && (
        <div className="new-key-banner">
          <div className="new-key-banner-header">
            <i className="fas fa-triangle-exclamation" />
            Copy now — this key won't be shown again
          </div>
          <div className="new-key-banner-body">
            <code className="new-key-value">
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
        <div className="auth-loading">
          <LoadingSpinner size="sm" />
        </div>
      ) : keys.length === 0 ? (
        <div className="card empty-icon-block">
          <i className="fas fa-key" />
          <div className="empty-icon-block-text">
            No API keys yet. Create one above to get programmatic access.
          </div>
        </div>
      ) : (
        <div className="card">
          {keys.map((k) => (
            <div key={k.id} className="apikey-row">
              <i className="fas fa-key apikey-icon" />
              <div className="apikey-info">
                <div className="apikey-name">{k.name}</div>
                <div className="apikey-details">
                  {k.keyPrefix}... &middot; {formatDate(k.createdAt)}
                  {k.lastUsed && <> &middot; last used {formatDate(k.lastUsed)}</>}
                </div>
              </div>
              <button
                className="btn btn-sm apikey-revoke-btn"
                onClick={() => handleRevoke(k.id, k.name)}
                disabled={revokingId === k.id}
                title="Revoke key"
              >
                {revokingId === k.id ? <LoadingSpinner size="sm" /> : <i className="fas fa-trash" />}
              </button>
            </div>
          ))}
        </div>
      )}
      <ConfirmDialog
        open={!!confirmDialog}
        title={confirmDialog?.title}
        message={confirmDialog?.message}
        confirmLabel={confirmDialog?.confirmLabel}
        danger={confirmDialog?.danger}
        onConfirm={confirmDialog?.onConfirm}
        onCancel={() => setConfirmDialog(null)}
      />
    </div>
  )
}

export default function Account() {
  const { addToast } = useOutletContext()
  const { authEnabled, user } = useAuth()
  const [activeTab, setActiveTab] = useState('profile')

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

  // Filter tabs: hide security tab for OAuth-only users
  const isLocal = user?.provider === 'local'
  const visibleTabs = isLocal ? TABS : TABS.filter(t => t.id !== 'security')

  return (
    <div className="page account-page">
      {/* Header */}
      <div className="page-header">
        <h1 className="page-title">Account</h1>
        <p className="page-subtitle">Profile, credentials, and API keys</p>
      </div>

      {/* Tab bar */}
      <div className="auth-tab-bar auth-tab-bar--flush">
        {visibleTabs.map(tab => (
          <button
            key={tab.id}
            onClick={() => setActiveTab(tab.id)}
            className={`auth-tab ${activeTab === tab.id ? 'active' : ''}`}
          >
            <i className={`fas ${tab.icon} auth-tab-icon`} />
            {tab.label}
          </button>
        ))}
      </div>

      {/* Tab content */}
      {activeTab === 'profile' && <ProfileTab addToast={addToast} />}
      {activeTab === 'security' && <SecurityTab addToast={addToast} />}
      {activeTab === 'apikeys' && <ApiKeysTab addToast={addToast} />}
    </div>
  )
}
