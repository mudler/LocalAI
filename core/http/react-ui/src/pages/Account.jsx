import { useState, useEffect, useCallback } from 'react'
import { useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../context/AuthContext'
import { apiKeysApi, profileApi } from '../utils/api'
import LoadingSpinner from '../components/LoadingSpinner'
import PageHeader from '../components/PageHeader'
import SettingRow from '../components/SettingRow'
import ConfirmDialog from '../components/ConfirmDialog'
import './auth.css'
import Icon from '../components/Icon'

function formatDate(d) {
  if (!d) return '-'
  return new Date(d).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

const TABS = [
  { id: 'profile', icon: 'user', labelKey: 'account.tabs.profile' },
  { id: 'security', icon: 'lock', labelKey: 'account.tabs.security' },
  { id: 'apikeys', icon: 'key', labelKey: 'account.tabs.apiKeys' },
]

function ProfileTab({ addToast }) {
  const { t } = useTranslation('auth')
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
      addToast(t('account.profile.updated'), 'success')
      refresh()
    } catch (err) {
      addToast(t('account.profile.updateFailed', { message: err.message }), 'error')
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
            <Icon name="user" className="account-avatar-icon" />
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
          <SettingRow label={t('account.profile.displayName')} description={t('account.profile.displayNameDescription')}>
            <input
              type="text"
              className="input account-input-sm"
              value={name}
              onChange={(e) => setName(e.target.value)}
              disabled={saving}
              maxLength={100}
            />
          </SettingRow>
          <SettingRow label={t('account.profile.avatarUrl')} description={t('account.profile.avatarUrlDescription')}>
            <div className="account-input-row">
              <input
                type="url"
                className="input account-input-sm"
                value={avatarUrl}
                onChange={(e) => setAvatarUrl(e.target.value)}
                disabled={saving}
                maxLength={512}
                placeholder={t('account.profile.avatarUrlPlaceholder')}
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
            {saving
              ? <><LoadingSpinner size="sm" /> {t('account.profile.saving')}</>
              : <><Icon name="save" /> {t('account.profile.save')}</>}
          </button>
        </div>
      </form>
    </div>
  )
}

function SecurityTab({ addToast }) {
  const { t } = useTranslation('auth')
  const { user } = useAuth()
  const isLocal = user?.provider === 'local'

  const [currentPw, setCurrentPw] = useState('')
  const [newPw, setNewPw] = useState('')
  const [confirmPw, setConfirmPw] = useState('')
  const [saving, setSaving] = useState(false)
  const [weakWarning, setWeakWarning] = useState('')
  const [acknowledgeWeak, setAcknowledgeWeak] = useState(false)

  const handleSubmit = async (e) => {
    e.preventDefault()
    if (newPw !== confirmPw) {
      addToast(t('account.security.passwordsDoNotMatch'), 'error')
      return
    }
    setSaving(true)
    try {
      await profileApi.changePassword(currentPw, newPw, acknowledgeWeak)
      addToast(t('account.security.changed'), 'success')
      setCurrentPw('')
      setNewPw('')
      setConfirmPw('')
      setWeakWarning('')
      setAcknowledgeWeak(false)
    } catch (err) {
      if (err.body?.overridable) {
        setWeakWarning(err.body.error || err.message)
      } else {
        addToast(err.message, 'error')
      }
    } finally {
      setSaving(false)
    }
  }

  if (!isLocal) {
    return (
      <div className="card empty-icon-block">
        <Icon name="shield" />
        <div className="empty-icon-block-text">
          {t('account.security.oauthOnly', { provider: user?.provider || 'OAuth' })}
        </div>
      </div>
    )
  }

  return (
    <form onSubmit={handleSubmit}>
      <div className="card">
        <SettingRow label={t('account.security.currentPassword')} description={t('account.security.currentPasswordDescription')}>
          <input
            type="password"
            className="input account-input-sm"
            value={currentPw}
            onChange={(e) => setCurrentPw(e.target.value)}
            placeholder={t('account.security.currentPasswordPlaceholder')}
            disabled={saving}
            required
          />
        </SettingRow>
        <SettingRow label={t('account.security.newPassword')} description={t('account.security.newPasswordDescription')}>
          <input
            type="password"
            className="input account-input-sm"
            value={newPw}
            onChange={(e) => {
              setNewPw(e.target.value)
              setWeakWarning('')
              setAcknowledgeWeak(false)
            }}
            placeholder={t('account.security.newPasswordPlaceholder')}
            disabled={saving}
            required
          />
        </SettingRow>
        <SettingRow label={t('account.security.confirmPassword')} description={t('account.security.confirmPasswordDescription')}>
          <input
            type="password"
            className="input account-input-sm"
            value={confirmPw}
            onChange={(e) => setConfirmPw(e.target.value)}
            placeholder={t('account.security.confirmPasswordPlaceholder')}
            disabled={saving}
            required
          />
        </SettingRow>
        {weakWarning && (
          <SettingRow label="" description="">
            <div role="alert" className="stack stack--xs">
              <div className="login-warning">{weakWarning}</div>
              <label style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)', fontSize: '0.875rem' }}>
                <input
                  type="checkbox"
                  checked={acknowledgeWeak}
                  onChange={(e) => setAcknowledgeWeak(e.target.checked)}
                  disabled={saving}
                />
                Use this password anyway
              </label>
            </div>
          </SettingRow>
        )}
      </div>
      <div className="form-actions">
        <button
          type="submit"
          className="btn btn-primary btn-sm"
          disabled={saving || !currentPw || !newPw || !confirmPw}
        >
          {saving
            ? <><LoadingSpinner size="sm" /> {t('account.security.changing')}</>
            : t('account.security.changePassword')}
        </button>
      </div>
    </form>
  )
}

function ApiKeysTab({ addToast }) {
  const { t } = useTranslation('auth')
  const [keys, setKeys] = useState([])
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [newKeyName, setNewKeyName] = useState('')
  const [newKeyPlaintext, setNewKeyPlaintext] = useState(null)
  const [revokingId, setRevokingId] = useState(null)
  const [confirmDialog, setConfirmDialog] = useState(null)
  const [pauseFormId, setPauseFormId] = useState(null)
  const [pauseMode, setPauseMode] = useState('indefinite')
  const [pauseUntil, setPauseUntil] = useState('')
  const [pauseBusyId, setPauseBusyId] = useState(null)

  const fetchKeys = useCallback(async () => {
    setLoading(true)
    try {
      const data = await apiKeysApi.list()
      setKeys(data.keys || [])
    } catch (err) {
      addToast(t('account.apiKeys.loadFailed', { message: err.message }), 'error')
    } finally {
      setLoading(false)
    }
  }, [addToast, t])

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
      addToast(t('account.apiKeys.createdToast'), 'success')
    } catch (err) {
      addToast(t('account.apiKeys.createFailed', { message: err.message }), 'error')
    } finally {
      setCreating(false)
    }
  }

  const handleRevoke = async (id, name) => {
    setConfirmDialog({
      title: t('account.apiKeys.revokeTitle'),
      message: t('account.apiKeys.revokeMessage', { name }),
      confirmLabel: t('account.apiKeys.revoke'),
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        setRevokingId(id)
        try {
          await apiKeysApi.revoke(id)
          setKeys(prev => prev.filter(k => k.id !== id))
          addToast(t('account.apiKeys.revoked'), 'success')
        } catch (err) {
          addToast(t('account.apiKeys.revokeFailed', { message: err.message }), 'error')
        } finally {
          setRevokingId(null)
        }
      },
    })
  }

  const isPaused = (k) => k.disabled || (k.pausedUntil && new Date(k.pausedUntil) > new Date())

  const applyPause = async (id, disabled, pausedUntil) => {
    setPauseBusyId(id)
    try {
      await apiKeysApi.setPause(id, disabled, pausedUntil)
      setPauseFormId(null)
      setPauseUntil('')
      await fetchKeys()
      addToast(t(disabled || pausedUntil ? 'account.apiKeys.pausedToast' : 'account.apiKeys.resumedToast'), 'success')
    } catch (err) {
      addToast(t('account.apiKeys.pauseFailed', { message: err.message }), 'error')
    } finally {
      setPauseBusyId(null)
    }
  }

  const submitPause = (id) => {
    if (pauseMode === 'until') {
      if (!pauseUntil) return
      applyPause(id, false, new Date(pauseUntil).toISOString())
    } else {
      applyPause(id, true, null)
    }
  }

  const openPauseForm = (id) => {
    setPauseMode('indefinite')
    setPauseUntil('')
    setPauseFormId(id)
  }

  const copyToClipboard = (text) => {
    if (navigator.clipboard?.writeText) {
      navigator.clipboard.writeText(text).then(
        () => addToast(t('account.apiKeys.copiedToast'), 'success'),
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
      addToast(t('account.apiKeys.copiedToast'), 'success')
    } catch (_) {
      addToast(t('account.apiKeys.copyFailed'), 'error')
    }
    document.body.removeChild(ta)
  }

  return (
    <div>
      {/* Create key form */}
      <div className="card mb-md">
        <form onSubmit={handleCreate}>
          <SettingRow label={t('account.apiKeys.create')} description={t('account.apiKeys.createDescription')}>
            <div className="account-input-row">
              <input
                type="text"
                className="input account-input-xs"
                placeholder={t('account.apiKeys.namePlaceholder')}
                value={newKeyName}
                onChange={(e) => setNewKeyName(e.target.value)}
                disabled={creating}
                maxLength={64}
              />
              <button type="submit" className="btn btn-primary btn-sm" disabled={creating || !newKeyName.trim()}>
                {creating ? <LoadingSpinner size="sm" /> : <><Icon name="plus" /> {t('account.apiKeys.createButton')}</>}
              </button>
            </div>
          </SettingRow>
        </form>
      </div>

      {/* Newly created key banner */}
      {newKeyPlaintext && (
        <div className="new-key-banner">
          <div className="new-key-banner-header">
            <Icon name="warning" />
            {t('account.apiKeys.copyNow')}
          </div>
          <div className="new-key-banner-body">
            <code className="new-key-value">
              {newKeyPlaintext}
            </code>
            <button className="btn btn-secondary btn-sm" onClick={() => copyToClipboard(newKeyPlaintext)}>
              <Icon name="copy" />
            </button>
            <button className="btn btn-secondary btn-sm" onClick={() => setNewKeyPlaintext(null)}>
              <Icon name="close" />
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
          <Icon name="key" />
          <div className="empty-icon-block-text">
            {t('account.apiKeys.empty')}
          </div>
        </div>
      ) : (
        <div className="card">
          {keys.map((k) => (
            <div key={k.id} className="apikey-item">
              <div className="apikey-row">
                <Icon name="key" className="apikey-icon" />
                <div className="apikey-info">
                  <div className="apikey-name">
                    {k.name}
                    {isPaused(k) && (
                      <span className="apikey-paused-badge">
                        {k.pausedUntil && !k.disabled
                          ? t('account.apiKeys.pausedUntil', { date: formatDate(k.pausedUntil) })
                          : t('account.apiKeys.paused')}
                      </span>
                    )}
                  </div>
                  <div className="apikey-details">
                    {k.keyPrefix}... &middot; {formatDate(k.createdAt)}
                    {k.lastUsed && <> &middot; {t('account.apiKeys.lastUsed', { date: formatDate(k.lastUsed) })}</>}
                  </div>
                </div>
                {isPaused(k) ? (
                  <button
                    className="btn btn-secondary btn-sm"
                    onClick={() => applyPause(k.id, false, null)}
                    disabled={pauseBusyId === k.id}
                  >
                    {pauseBusyId === k.id ? <LoadingSpinner size="sm" /> : <><Icon name="play" /> {t('account.apiKeys.resume')}</>}
                  </button>
                ) : (
                  <button
                    className="btn btn-secondary btn-sm"
                    onClick={() => (pauseFormId === k.id ? setPauseFormId(null) : openPauseForm(k.id))}
                  >
                    <Icon name="pause" /> {t('account.apiKeys.pause')}
                  </button>
                )}
                <button
                  className="btn btn-sm apikey-revoke-btn"
                  onClick={() => handleRevoke(k.id, k.name)}
                  disabled={revokingId === k.id}
                  title={t('account.apiKeys.revokeKey')}
                >
                  {revokingId === k.id ? <LoadingSpinner size="sm" /> : <Icon name="trash" />}
                </button>
              </div>
              {pauseFormId === k.id && !isPaused(k) && (
                <div className="apikey-pause-form">
                  <label className="apikey-pause-option">
                    <input
                      type="radio"
                      name={`pause-mode-${k.id}`}
                      checked={pauseMode === 'indefinite'}
                      onChange={() => setPauseMode('indefinite')}
                    />
                    {t('account.apiKeys.pauseIndefinitely')}
                  </label>
                  <label className="apikey-pause-option">
                    <input
                      type="radio"
                      name={`pause-mode-${k.id}`}
                      checked={pauseMode === 'until'}
                      onChange={() => setPauseMode('until')}
                    />
                    {t('account.apiKeys.pauseUntil')}
                  </label>
                  {pauseMode === 'until' && (
                    <input
                      type="datetime-local"
                      className="input apikey-pause-date"
                      aria-label={t('account.apiKeys.pauseUntil')}
                      value={pauseUntil}
                      onChange={(e) => setPauseUntil(e.target.value)}
                    />
                  )}
                  <button
                    className="btn btn-primary btn-sm"
                    onClick={() => submitPause(k.id)}
                    disabled={pauseBusyId === k.id || (pauseMode === 'until' && !pauseUntil)}
                  >
                    {t('account.apiKeys.pauseConfirm')}
                  </button>
                </div>
              )}
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
  const { t } = useTranslation('auth')
  const { authEnabled, user } = useAuth()
  const [activeTab, setActiveTab] = useState('profile')

  if (!authEnabled) {
    return (
      <div className="page page--narrow">
        <div className="empty-state">
          <div className="empty-state-icon"><Icon name="user-settings" /></div>
          <h2 className="empty-state-title">{t('account.unavailable')}</h2>
          <p className="empty-state-text">{t('account.unavailableText')}</p>
        </div>
      </div>
    )
  }

  // Filter tabs: hide security tab for OAuth-only users
  const isLocal = user?.provider === 'local'
  const visibleTabs = isLocal ? TABS : TABS.filter(tab => tab.id !== 'security')

  return (
    <div className="page page--narrow account-page">
      {/* Header */}
      <PageHeader title={t('account.title')} supporting={t('account.subtitle')} />

      {/* Tab bar */}
      <div className="auth-tab-bar auth-tab-bar--flush">
        {visibleTabs.map(tab => (
          <button
            key={tab.id}
            onClick={() => setActiveTab(tab.id)}
            className={`auth-tab ${activeTab === tab.id ? 'active' : ''}`}
          >
            <Icon name={tab.icon} className="auth-tab-icon" />
            {t(tab.labelKey)}
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
