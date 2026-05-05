import { useState, useEffect, useCallback } from 'react'
import { useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../context/AuthContext'
import { adminUsersApi, adminInvitesApi } from '../utils/api'
import LoadingSpinner from '../components/LoadingSpinner'
import Modal from '../components/Modal'
import ConfirmDialog from '../components/ConfirmDialog'
import './auth.css'

function RoleBadge({ role }) {
  const isPrimary = role === 'admin'
  return (
    <span className={`badge ${isPrimary ? 'badge-primary' : 'badge-secondary'}`}>
      {role}
    </span>
  )
}

function StatusBadge({ status }) {
  const variant = status === 'active'
    ? 'success'
    : status === 'disabled'
      ? 'danger'
      : 'warning'
  return (
    <span className={`status-badge status-badge-${variant}`}>
      {status || 'unknown'}
    </span>
  )
}

function ProviderBadge({ provider }) {
  return (
    <span className="badge badge-secondary" style={{ fontSize: '0.7rem' }}>
      {provider || 'local'}
    </span>
  )
}

function PermissionSummary({ user, onClick }) {
  if (user.role === 'admin') {
    return <span className="perm-summary-text">All (admin)</span>
  }

  const perms = user.permissions || {}
  const apiFeatures = ['chat', 'images', 'audio_speech', 'audio_transcription', 'vad', 'detection', 'video', 'embeddings', 'sound']
  const agentFeatures = ['agents', 'skills', 'collections', 'mcp_jobs']
  const generalFeatures = ['fine_tuning']

  const apiOn = apiFeatures.filter(f => perms[f] !== false && (perms[f] === true || perms[f] === undefined)).length
  const agentOn = agentFeatures.filter(f => perms[f]).length
  const generalOn = generalFeatures.filter(f => perms[f]).length

  const modelRestricted = user.allowed_models?.enabled
  const quotaCount = (user.quotas || []).length

  return (
    <button
      className="btn btn-sm btn-secondary perm-summary-btn"
      onClick={onClick}
      title="Edit permissions"
    >
      <i className="fas fa-shield-halved" />
      {apiOn}/{apiFeatures.length} API, {agentOn}/{agentFeatures.length} Agent, {generalOn}/{generalFeatures.length} Features
      {modelRestricted && ' | Models restricted'}
      {quotaCount > 0 && ` · ${quotaCount} quota${quotaCount !== 1 ? 's' : ''}`}
    </button>
  )
}

const WINDOW_OPTIONS = [
  { value: '1m', label: '1 minute' },
  { value: '5m', label: '5 minutes' },
  { value: '1h', label: '1 hour' },
  { value: '6h', label: '6 hours' },
  { value: '1d', label: '1 day' },
  { value: '7d', label: '7 days' },
  { value: '30d', label: '30 days' },
]

function PermissionsModal({ user, featureMeta, availableModels, onClose, onSave, addToast }) {
  const [permissions, setPermissions] = useState({ ...(user.permissions || {}) })
  const [allowedModels, setAllowedModels] = useState(user.allowed_models || { enabled: false, models: [] })
  const [quotas, setQuotas] = useState((user.quotas || []).map(q => ({ ...q, _dirty: false })))
  const [deletedQuotaIds, setDeletedQuotaIds] = useState([])
  const [saving, setSaving] = useState(false)

  const apiFeatures = featureMeta?.api_features || []
  const agentFeatures = featureMeta?.agent_features || []
  const generalFeatures = featureMeta?.general_features || []

  useEffect(() => {
    const handleKeyDown = (e) => {
      if (e.key === 'Escape') onClose()
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [onClose])

  const toggleFeature = (key) => {
    setPermissions(prev => ({ ...prev, [key]: !prev[key] }))
  }

  const setAllFeatures = (features, value) => {
    setPermissions(prev => {
      const updated = { ...prev }
      features.forEach(f => { updated[f.key] = value })
      return updated
    })
  }

  const toggleModel = (model) => {
    setAllowedModels(prev => {
      const models = prev.models || []
      const has = models.includes(model)
      return {
        ...prev,
        models: has ? models.filter(m => m !== model) : [...models, model],
      }
    })
  }

  const setAllModels = (value) => {
    if (value) {
      setAllowedModels(prev => ({ ...prev, models: [...(availableModels || [])] }))
    } else {
      setAllowedModels(prev => ({ ...prev, models: [] }))
    }
  }

  const addQuota = () => {
    setQuotas(prev => [...prev, {
      id: null, model: '', max_requests: null, max_total_tokens: null, window: '1h',
      current_requests: 0, current_total_tokens: 0, _dirty: true, _new: true,
    }])
  }

  const updateQuota = (idx, field, value) => {
    setQuotas(prev => prev.map((q, i) => i === idx ? { ...q, [field]: value, _dirty: true } : q))
  }

  const removeQuota = (idx) => {
    const q = quotas[idx]
    if (q.id && !q._new) {
      setDeletedQuotaIds(prev => [...prev, q.id])
    }
    setQuotas(prev => prev.filter((_, i) => i !== idx))
  }

  const handleSave = async () => {
    setSaving(true)
    try {
      await adminUsersApi.setPermissions(user.id, permissions)
      await adminUsersApi.setModels(user.id, allowedModels)

      // Delete removed quotas
      for (const qid of deletedQuotaIds) {
        await adminUsersApi.deleteQuota(user.id, qid)
      }
      // Upsert dirty quotas
      for (const q of quotas) {
        if (q._dirty || q._new) {
          await adminUsersApi.setQuota(user.id, {
            model: q.model,
            max_requests: q.max_requests || null,
            max_total_tokens: q.max_total_tokens || null,
            window: q.window,
          })
        }
      }

      // Refetch quotas so caller gets fresh state (including server-assigned IDs and current usage)
      let freshQuotas = []
      try {
        const qData = await adminUsersApi.getQuotas(user.id)
        freshQuotas = Array.isArray(qData) ? qData : qData.quotas || []
      } catch {
        // Fall back to local state if refetch fails
        freshQuotas = quotas.map(q => ({ ...q, _dirty: false, _new: false }))
      }
      onSave(user.id, permissions, allowedModels, freshQuotas)
      addToast(`Permissions updated for ${user.name || user.email}`, 'success')
      onClose()
    } catch (err) {
      addToast(`Failed to update permissions: ${err.message}`, 'error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal onClose={onClose} maxWidth="640px">
      <div className="perm-modal-body">
        {/* Header with avatar */}
        <div className="perm-modal-header">
          {user.avatarUrl ? (
            <img src={user.avatarUrl} alt="" className="perm-modal-avatar" />
          ) : (
            <i className="fas fa-user-circle user-avatar-placeholder--lg" />
          )}
          <h3>Permissions for &ldquo;{user.name || user.email}&rdquo;</h3>
        </div>

        {/* API Endpoints */}
        <div className="perm-section">
          <div className="perm-section-header">
            <strong className="perm-section-title">
              <i className="fas fa-plug" />
              API Endpoints
            </strong>
            <div className="action-group">
              <button className="btn btn-sm btn-secondary perm-btn-all-none" onClick={() => setAllFeatures(apiFeatures, true)}>All</button>
              <button className="btn btn-sm btn-secondary perm-btn-all-none" onClick={() => setAllFeatures(apiFeatures, false)}>None</button>
            </div>
          </div>
          <div className="perm-grid">
            {apiFeatures.map(f => (
              <button
                key={f.key}
                className={`btn btn-sm ${permissions[f.key] ? 'btn-primary' : 'btn-secondary'} perm-btn-feature`}
                onClick={() => toggleFeature(f.key)}
              >
                {f.label}
              </button>
            ))}
          </div>
        </div>

        {/* Agent Features */}
        <div className="perm-section">
          <div className="perm-section-header">
            <strong className="perm-section-title">
              <i className="fas fa-robot" />
              Agent Features
            </strong>
            <div className="action-group">
              <button className="btn btn-sm btn-secondary perm-btn-all-none" onClick={() => setAllFeatures(agentFeatures, true)}>All</button>
              <button className="btn btn-sm btn-secondary perm-btn-all-none" onClick={() => setAllFeatures(agentFeatures, false)}>None</button>
            </div>
          </div>
          <div className="perm-grid">
            {agentFeatures.map(f => (
              <button
                key={f.key}
                className={`btn btn-sm ${permissions[f.key] ? 'btn-primary' : 'btn-secondary'} perm-btn-feature`}
                onClick={() => toggleFeature(f.key)}
              >
                {f.label}
              </button>
            ))}
          </div>
        </div>

        {/* General Features */}
        {generalFeatures.length > 0 && (
        <div className="perm-section">
          <div className="perm-section-header">
            <strong className="perm-section-title">
              <i className="fas fa-sliders" />
              Features
            </strong>
            <div className="action-group">
              <button className="btn btn-sm btn-secondary perm-btn-all-none" onClick={() => setAllFeatures(generalFeatures, true)}>All</button>
              <button className="btn btn-sm btn-secondary perm-btn-all-none" onClick={() => setAllFeatures(generalFeatures, false)}>None</button>
            </div>
          </div>
          <div className="perm-grid">
            {generalFeatures.map(f => (
              <button
                key={f.key}
                className={`btn btn-sm ${permissions[f.key] ? 'btn-primary' : 'btn-secondary'} perm-btn-feature`}
                onClick={() => toggleFeature(f.key)}
              >
                {f.label}
              </button>
            ))}
          </div>
        </div>
        )}

        {/* Model Access */}
        <div className="perm-section">
          <div className="perm-section-header">
            <strong className="perm-section-title">
              <i className="fas fa-cubes" />
              Model Access
            </strong>
          </div>
          <div style={{ marginBottom: 'var(--spacing-sm)' }}>
            <label className="perm-toggle-label">
              <label className="toggle" style={{ flexShrink: 0 }}>
                <input
                  type="checkbox"
                  checked={allowedModels.enabled}
                  onChange={() => setAllowedModels(prev => ({ ...prev, enabled: !prev.enabled }))}
                />
                <span className="toggle-slider" />
              </label>
              Restrict to specific models
            </label>
          </div>
          {allowedModels.enabled ? (
            <>
              <div className="action-group" style={{ marginBottom: 'var(--spacing-sm)' }}>
                <button className="btn btn-sm btn-secondary perm-btn-all-none" onClick={() => setAllModels(true)}>All</button>
                <button className="btn btn-sm btn-secondary perm-btn-all-none" onClick={() => setAllModels(false)}>None</button>
              </div>
              <div className="model-list">
                {(availableModels || []).map(m => {
                  const checked = (allowedModels.models || []).includes(m)
                  return (
                    <label
                      key={m}
                      className={`model-item${checked ? ' model-item-checked' : ''}`}
                    >
                      <input
                        type="checkbox"
                        checked={checked}
                        onChange={() => toggleModel(m)}
                      />
                      <span className="model-item-check">
                        {checked && <i className="fas fa-check" />}
                      </span>
                      <span className="model-item-name">{m}</span>
                    </label>
                  )
                })}
                {(!availableModels || availableModels.length === 0) && (
                  <span className="perm-empty">No models available</span>
                )}
              </div>
            </>
          ) : (
            <p className="perm-hint">All models are accessible</p>
          )}
        </div>

        {/* Quotas */}
        <div className="perm-section">
          <div className="perm-section-header">
            <strong className="perm-section-title">
              <i className="fas fa-gauge-high" />
              Quotas
            </strong>
            <button className="btn btn-sm btn-primary" onClick={addQuota}>
              <i className="fas fa-plus" /> Add rule
            </button>
          </div>
          {quotas.length === 0 ? (
            <div className="quota-empty">
              <i className="fas fa-infinity quota-empty-icon" />
              <span>No quota rules &mdash; unlimited access</span>
            </div>
          ) : (
            <div className="quota-rules-list">
              {quotas.map((q, idx) => {
                const reqPct = (q.max_requests && !q._new) ? Math.min(100, Math.round(((q.current_requests ?? 0) / q.max_requests) * 100)) : null
                const tokPct = (q.max_total_tokens && !q._new) ? Math.min(100, Math.round(((q.current_total_tokens ?? 0) / q.max_total_tokens) * 100)) : null
                return (
                  <div key={q.id || `new-${idx}`} className="quota-card">
                    <div className="quota-card-header">
                      <select
                        className="quota-select quota-select--model"
                        value={q.model}
                        onChange={e => updateQuota(idx, 'model', e.target.value)}
                      >
                        <option value="">All models</option>
                        {(availableModels || []).map(m => (
                          <option key={m} value={m}>{m}</option>
                        ))}
                      </select>
                      <select
                        className="quota-select"
                        value={q.window}
                        onChange={e => updateQuota(idx, 'window', e.target.value)}
                      >
                        {WINDOW_OPTIONS.map(w => (
                          <option key={w.value} value={w.value}>per {w.label}</option>
                        ))}
                      </select>
                      <button
                        className="btn btn-sm btn-danger quota-remove-btn"
                        onClick={() => removeQuota(idx)}
                        title="Remove rule"
                        aria-label="Remove quota rule"
                      >
                        <i className="fas fa-trash" />
                      </button>
                    </div>
                    <div className="quota-card-fields">
                      <div className="quota-field">
                        <label className="quota-field-label">Max requests</label>
                        <input
                          type="number"
                          className="quota-input"
                          placeholder="Unlimited"
                          value={q.max_requests ?? ''}
                          onChange={e => updateQuota(idx, 'max_requests', e.target.value ? parseInt(e.target.value, 10) : null)}
                          min="0"
                        />
                        {reqPct !== null && (
                          <div className="quota-usage">
                            <div className="quota-progress">
                              <div
                                className={`quota-progress-fill${reqPct >= 90 ? ' quota-progress-fill--danger' : reqPct >= 70 ? ' quota-progress-fill--warning' : ''}`}
                                style={{ width: `${reqPct}%` }}
                              />
                            </div>
                            <span className="quota-usage-label">{q.current_requests ?? 0} / {q.max_requests}</span>
                          </div>
                        )}
                      </div>
                      <div className="quota-field">
                        <label className="quota-field-label">Max tokens</label>
                        <input
                          type="number"
                          className="quota-input"
                          placeholder="Unlimited"
                          value={q.max_total_tokens ?? ''}
                          onChange={e => updateQuota(idx, 'max_total_tokens', e.target.value ? parseInt(e.target.value, 10) : null)}
                          min="0"
                        />
                        {tokPct !== null && (
                          <div className="quota-usage">
                            <div className="quota-progress">
                              <div
                                className={`quota-progress-fill${tokPct >= 90 ? ' quota-progress-fill--danger' : tokPct >= 70 ? ' quota-progress-fill--warning' : ''}`}
                                style={{ width: `${tokPct}%` }}
                              />
                            </div>
                            <span className="quota-usage-label">{(q.current_total_tokens ?? 0).toLocaleString()} / {q.max_total_tokens.toLocaleString()}</span>
                          </div>
                        )}
                      </div>
                    </div>
                  </div>
                )
              })}
            </div>
          )}
        </div>

        {/* Actions */}
        <div className="perm-modal-actions">
          <button className="btn btn-secondary" onClick={onClose}>Cancel</button>
          <button className="btn btn-primary" onClick={handleSave} disabled={saving}>
            {saving ? 'Saving...' : 'Save'}
          </button>
        </div>
      </div>
    </Modal>
  )
}

function InviteStatusBadge({ invite }) {
  const now = new Date()
  const expired = new Date(invite.expiresAt) < now
  const used = !!invite.usedBy

  if (used) {
    return <StatusBadge status="used" />
  }
  if (expired) {
    return <span className="status-badge status-badge-danger">expired</span>
  }
  return <span className="status-badge status-badge-success">available</span>
}

function isInviteAvailable(invite) {
  return !invite.usedBy && new Date(invite.expiresAt) > new Date()
}

function InvitesTab({ addToast }) {
  const [invites, setInvites] = useState([])
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)
  const [confirmDialog, setConfirmDialog] = useState(null)
  const [newInviteCodes, setNewInviteCodes] = useState({})

  const fetchInvites = useCallback(async () => {
    setLoading(true)
    try {
      const data = await adminInvitesApi.list()
      setInvites(Array.isArray(data) ? data : data.invites || [])
    } catch (err) {
      addToast(`Failed to load invites: ${err.message}`, 'error')
    } finally {
      setLoading(false)
    }
  }, [addToast])

  useEffect(() => {
    fetchInvites()
  }, [fetchInvites])

  const handleCreate = async () => {
    setCreating(true)
    try {
      const resp = await adminInvitesApi.create(168) // 7 days
      if (resp && resp.id && resp.code) {
        setNewInviteCodes(prev => ({ ...prev, [resp.id]: resp.code }))
      }
      addToast('Invite link created', 'success')
      fetchInvites()
    } catch (err) {
      addToast(`Failed to create invite: ${err.message}`, 'error')
    } finally {
      setCreating(false)
    }
  }

  const handleRevoke = async (invite) => {
    setConfirmDialog({
      title: 'Revoke Invite',
      message: 'Revoke this invite link?',
      confirmLabel: 'Revoke',
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        try {
          await adminInvitesApi.delete(invite.id)
          setInvites(prev => prev.filter(x => x.id !== invite.id))
          addToast('Invite revoked', 'success')
        } catch (err) {
          addToast(`Failed to revoke invite: ${err.message}`, 'error')
        }
      },
    })
  }

  const handleCopyUrl = (code) => {
    const url = `${window.location.origin}/invite/${code}`
    try {
      const textarea = document.createElement('textarea')
      textarea.value = url
      textarea.style.position = 'fixed'
      textarea.style.opacity = '0'
      document.body.appendChild(textarea)
      textarea.select()
      document.execCommand('copy')
      document.body.removeChild(textarea)
      addToast('Invite URL copied to clipboard', 'success')
    } catch {
      addToast('Failed to copy URL', 'error')
    }
  }

  if (loading) {
    return (
      <div className="auth-loading">
        <LoadingSpinner size="lg" />
      </div>
    )
  }

  return (
    <>
      <div className="auth-toolbar">
        <button className="btn btn-primary btn-sm" onClick={handleCreate} disabled={creating}>
          <i className="fas fa-plus" /> {creating ? 'Creating...' : 'Generate Invite Link'}
        </button>
        <button className="btn btn-secondary btn-sm" onClick={fetchInvites} disabled={loading}>
          <i className="fas fa-rotate" /> Refresh
        </button>
      </div>

      {invites.length === 0 ? (
        <div className="empty-state">
          <div className="empty-state-icon"><i className="fas fa-envelope-open-text" /></div>
          <h2 className="empty-state-title">No invite links</h2>
          <p className="empty-state-text">Generate an invite link to let someone register.</p>
        </div>
      ) : (
        <div className="table-container">
          <table className="table">
            <thead>
              <tr>
                <th>Invite Link</th>
                <th>Status</th>
                <th>Created By</th>
                <th>Used By</th>
                <th>Expires</th>
                <th className="cell-actions--sm">Actions</th>
              </tr>
            </thead>
            <tbody>
              {invites.map(inv => (
                <tr key={inv.id}>
                  <td className="invite-cell">
                    {(() => {
                      const code = inv.code || newInviteCodes[inv.id]
                      if (isInviteAvailable(inv) && code) {
                        return (
                          <div className="invite-link-row">
                            <span
                              className="invite-link-text"
                              title={`${window.location.origin}/invite/${code}`}
                            >
                              {`${window.location.origin}/invite/${code}`}
                            </span>
                            <button
                              className="btn btn-sm btn-secondary invite-copy-btn"
                              onClick={() => handleCopyUrl(code)}
                              title="Copy invite URL"
                            >
                              <i className="fas fa-copy" /> Copy
                            </button>
                          </div>
                        )
                      }
                      return (
                        <span className="mono-text">
                          {inv.codePrefix || code?.substring(0, 8) || '???'}...
                        </span>
                      )
                    })()}
                  </td>
                  <td><InviteStatusBadge invite={inv} /></td>
                  <td className="cell-sm">
                    {inv.createdBy?.name || inv.createdBy?.id || '-'}
                  </td>
                  <td className="cell-sm">
                    {inv.usedBy?.name || inv.usedBy?.id || '\u2014'}
                  </td>
                  <td className="cell-muted">
                    {inv.expiresAt ? new Date(inv.expiresAt).toLocaleString() : '-'}
                  </td>
                  <td>
                    {isInviteAvailable(inv) && (
                      <button
                        className="btn btn-sm btn-danger"
                        onClick={() => handleRevoke(inv)}
                        title="Revoke invite"
                      >
                        <i className="fas fa-trash" />
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
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
    </>
  )
}

export default function Users() {
  const { addToast } = useOutletContext()
  const { user: currentUser } = useAuth()
  const { t } = useTranslation('admin')
  const [users, setUsers] = useState([])
  const [loading, setLoading] = useState(true)
  const [search, setSearch] = useState('')
  const [activeTab, setActiveTab] = useState('users')
  const [editingUser, setEditingUser] = useState(null)
  const [featureMeta, setFeatureMeta] = useState(null)
  const [availableModels, setAvailableModels] = useState([])
  const [confirmDialog, setConfirmDialog] = useState(null)
  const [passwordResetUser, setPasswordResetUser] = useState(null)
  const [newPassword, setNewPassword] = useState('')
  const [resettingPassword, setResettingPassword] = useState(false)

  const fetchUsers = useCallback(async () => {
    setLoading(true)
    try {
      const data = await adminUsersApi.list()
      setUsers(Array.isArray(data) ? data : data.users || [])
    } catch (err) {
      addToast(`Failed to load users: ${err.message}`, 'error')
    } finally {
      setLoading(false)
    }
  }, [addToast])

  const fetchFeatures = useCallback(async () => {
    try {
      const data = await adminUsersApi.getFeatures()
      setFeatureMeta(data)
      setAvailableModels(data.models || [])
    } catch {
      // Features endpoint may not be available, use defaults
      setFeatureMeta({
        api_features: [
          { key: 'chat', label: 'Chat Completions', default: true },
          { key: 'images', label: 'Image Generation', default: true },
          { key: 'audio_speech', label: 'Audio Speech / TTS', default: true },
          { key: 'audio_transcription', label: 'Audio Transcription', default: true },
          { key: 'vad', label: 'Voice Activity Detection', default: true },
          { key: 'detection', label: 'Detection', default: true },
          { key: 'video', label: 'Video Generation', default: true },
          { key: 'embeddings', label: 'Embeddings', default: true },
          { key: 'sound', label: 'Sound Generation', default: true },
        ],
        agent_features: [
          { key: 'agents', label: 'Agents', default: false },
          { key: 'skills', label: 'Skills', default: false },
          { key: 'collections', label: 'Collections', default: false },
          { key: 'mcp_jobs', label: 'MCP CI Jobs', default: false },
        ],
        general_features: [
          { key: 'fine_tuning', label: 'Fine-Tuning', default: false },
        ],
      })
    }
  }, [])

  useEffect(() => {
    fetchUsers()
    fetchFeatures()
  }, [fetchUsers, fetchFeatures])

  const handleToggleRole = async (u) => {
    const newRole = u.role === 'admin' ? 'user' : 'admin'
    try {
      await adminUsersApi.setRole(u.id, newRole)
      setUsers(prev => prev.map(x => x.id === u.id ? { ...x, role: newRole } : x))
      addToast(`${u.name || u.email} is now ${newRole}`, 'success')
    } catch (err) {
      addToast(`Failed to update role: ${err.message}`, 'error')
    }
  }

  const handleToggleStatus = async (u) => {
    const newStatus = u.status === 'active' ? 'disabled' : 'active'
    const action = newStatus === 'active' ? 'Approve' : 'Disable'
    try {
      await adminUsersApi.setStatus(u.id, newStatus)
      setUsers(prev => prev.map(x => x.id === u.id ? { ...x, status: newStatus } : x))
      addToast(`${action}d ${u.name || u.email}`, 'success')
    } catch (err) {
      addToast(`Failed to ${action.toLowerCase()} user: ${err.message}`, 'error')
    }
  }

  const handleDelete = async (u) => {
    setConfirmDialog({
      title: 'Delete User',
      message: `Delete user "${u.name || u.email}"? This will also remove their sessions and API keys.`,
      confirmLabel: 'Delete',
      danger: true,
      onConfirm: async () => {
        setConfirmDialog(null)
        try {
          await adminUsersApi.delete(u.id)
          setUsers(prev => prev.filter(x => x.id !== u.id))
          addToast(`User deleted`, 'success')
        } catch (err) {
          addToast(`Failed to delete user: ${err.message}`, 'error')
        }
      },
    })
  }

  const handleResetPassword = (u) => {
    setPasswordResetUser(u)
    setNewPassword('')
  }

  const confirmResetPassword = async () => {
    if (!passwordResetUser || newPassword.length < 8) return
    setResettingPassword(true)
    try {
      await adminUsersApi.resetPassword(passwordResetUser.id, newPassword)
      addToast(`Password reset for ${passwordResetUser.name || passwordResetUser.email}`, 'success')
      setPasswordResetUser(null)
      setNewPassword('')
    } catch (err) {
      addToast(`Failed to reset password: ${err.message}`, 'error')
    } finally {
      setResettingPassword(false)
    }
  }

  const filtered = users.filter(u => {
    if (!search) return true
    const q = search.toLowerCase()
    return (u.name || '').toLowerCase().includes(q) || (u.email || '').toLowerCase().includes(q)
  })

  const handlePermissionSave = (userId, newPerms, newModels, newQuotas) => {
    setUsers(prev => prev.map(u => u.id === userId ? { ...u, permissions: newPerms, allowed_models: newModels, quotas: newQuotas } : u))
  }

  const isSelf = (u) => currentUser && (u.id === currentUser.id || u.email === currentUser.email)

  return (
    <div className="page page--wide">
      <div className="page-header">
        <h1 className="page-title">{t('users.title')}</h1>
        <p className="page-subtitle">{t('users.subtitle')}</p>
      </div>

      {/* Tab bar */}
      <div className="auth-tab-bar">
        <button
          className={`btn btn-sm auth-tab--pill ${activeTab === 'users' ? 'btn-primary' : 'btn-secondary'}`}
          onClick={() => setActiveTab('users')}
        >
          <i className="fas fa-users" /> Users
        </button>
        <button
          className={`btn btn-sm auth-tab--pill ${activeTab === 'invites' ? 'btn-primary' : 'btn-secondary'}`}
          onClick={() => setActiveTab('invites')}
        >
          <i className="fas fa-envelope-open-text" /> Invites
        </button>
      </div>

      {activeTab === 'invites' ? (
        <InvitesTab addToast={addToast} />
      ) : (
        <>
          <div className="auth-toolbar">
            <div className="search-field">
              <i className="fas fa-search search-field-icon" />
              <input
                type="text"
                className="input"
                placeholder="Search by name or email..."
                value={search}
                onChange={e => setSearch(e.target.value)}
              />
            </div>
            <button className="btn btn-secondary btn-sm" onClick={fetchUsers} disabled={loading}>
              <i className="fas fa-rotate" /> Refresh
            </button>
          </div>

          {loading ? (
            <div className="auth-loading">
              <LoadingSpinner size="lg" />
            </div>
          ) : filtered.length === 0 ? (
            <div className="empty-state">
              <div className="empty-state-icon"><i className="fas fa-users" /></div>
              <h2 className="empty-state-title">{search ? 'No matching users' : 'No users'}</h2>
              <p className="empty-state-text">{search ? 'Try a different search term.' : 'No registered users found.'}</p>
            </div>
          ) : (
            <div className="table-container">
              <table className="table">
                <thead>
                  <tr>
                    <th>User</th>
                    <th>Email</th>
                    <th>Provider</th>
                    <th>Role</th>
                    <th>Permissions</th>
                    <th>Status</th>
                    <th>Created</th>
                    <th className="cell-actions">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {filtered.map(u => (
                    <tr key={u.id}>
                      <td>
                        <div className="user-identity">
                          {u.avatarUrl ? (
                            <img src={u.avatarUrl} alt="" className="user-avatar" />
                          ) : (
                            <i className="fas fa-user-circle user-avatar-placeholder" />
                          )}
                          <span className="user-name">{u.name || '(no name)'}</span>
                        </div>
                      </td>
                      <td className="user-email">{u.email}</td>
                      <td><ProviderBadge provider={u.provider} /></td>
                      <td><RoleBadge role={u.role} /></td>
                      <td>
                        <PermissionSummary
                          user={u}
                          onClick={() => u.role !== 'admin' && setEditingUser(u)}
                        />
                      </td>
                      <td><StatusBadge status={u.status} /></td>
                      <td className="cell-muted">
                        {u.createdAt ? new Date(u.createdAt).toLocaleDateString() : '-'}
                      </td>
                      <td>
                        {!isSelf(u) && (
                          <div className="action-group">
                            {u.status !== 'active' ? (
                              <button
                                className="btn btn-sm btn-primary"
                                onClick={() => handleToggleStatus(u)}
                                title="Approve user"
                              >
                                <i className="fas fa-check" />
                              </button>
                            ) : (
                              <button
                                className="btn btn-sm btn-secondary"
                                onClick={() => handleToggleStatus(u)}
                                title="Disable user"
                              >
                                <i className="fas fa-ban" />
                              </button>
                            )}
                            <button
                              className={`btn btn-sm ${u.role === 'admin' ? 'btn-secondary' : 'btn-primary'}`}
                              onClick={() => handleToggleRole(u)}
                              title={u.role === 'admin' ? 'Demote to user' : 'Promote to admin'}
                            >
                              <i className={`fas fa-${u.role === 'admin' ? 'arrow-down' : 'arrow-up'}`} />
                            </button>
                            {(!u.provider || u.provider === 'local') && (
                              <button
                                className="btn btn-sm btn-secondary"
                                onClick={() => handleResetPassword(u)}
                                title="Reset password"
                              >
                                <i className="fas fa-key" />
                              </button>
                            )}
                            <button
                              className="btn btn-sm btn-danger"
                              onClick={() => handleDelete(u)}
                              title="Delete user"
                            >
                              <i className="fas fa-trash" />
                            </button>
                          </div>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}

      {editingUser && featureMeta && (
        <PermissionsModal
          user={editingUser}
          featureMeta={featureMeta}
          availableModels={availableModels}
          onClose={() => setEditingUser(null)}
          onSave={handlePermissionSave}
          addToast={addToast}
        />
      )}
      {passwordResetUser && (
        <Modal onClose={() => setPasswordResetUser(null)} maxWidth="400px">
          <div className="perm-modal-body">
            <h3>Reset Password</h3>
            <p style={{ margin: 'var(--spacing-sm) 0' }}>
              Set a new password for <strong>{passwordResetUser.name || passwordResetUser.email}</strong>.
              All existing sessions will be invalidated.
            </p>
            <input
              type="password"
              className="input"
              placeholder="New password (min 8 characters)"
              value={newPassword}
              onChange={e => setNewPassword(e.target.value)}
              onKeyDown={e => { if (e.key === 'Enter' && newPassword.length >= 8) confirmResetPassword() }}
              autoFocus
            />
            <div className="perm-modal-actions" style={{ marginTop: 'var(--spacing-md)' }}>
              <button className="btn btn-secondary" onClick={() => setPasswordResetUser(null)}>Cancel</button>
              <button
                className="btn btn-primary"
                onClick={confirmResetPassword}
                disabled={resettingPassword || newPassword.length < 8}
              >
                {resettingPassword ? 'Resetting...' : 'Reset Password'}
              </button>
            </div>
          </div>
        </Modal>
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
