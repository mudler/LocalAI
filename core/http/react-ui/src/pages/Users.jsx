import { useState, useEffect, useCallback } from 'react'
import { useOutletContext } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'
import { adminUsersApi, adminInvitesApi } from '../utils/api'
import LoadingSpinner from '../components/LoadingSpinner'

function RoleBadge({ role }) {
  const isPrimary = role === 'admin'
  return (
    <span className={`badge ${isPrimary ? 'badge-primary' : 'badge-secondary'}`}>
      {role}
    </span>
  )
}

function StatusBadge({ status }) {
  const color = status === 'active'
    ? 'var(--color-success, #22c55e)'
    : status === 'disabled'
      ? 'var(--color-danger, #ef4444)'
      : 'var(--color-warning, #eab308)'
  return (
    <span
      style={{
        display: 'inline-block',
        padding: '2px 8px',
        borderRadius: 'var(--radius-sm, 4px)',
        fontSize: '0.75rem',
        fontWeight: 600,
        background: `${color}22`,
        color,
      }}
    >
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
    return (
      <span style={{ fontSize: '0.75rem', color: 'var(--color-text-secondary)', fontStyle: 'italic' }}>
        All (admin)
      </span>
    )
  }

  const perms = user.permissions || {}
  const apiFeatures = ['chat', 'images', 'audio_speech', 'audio_transcription', 'vad', 'detection', 'video', 'embeddings', 'sound']
  const agentFeatures = ['agents', 'skills', 'collections', 'mcp_jobs']

  const apiOn = apiFeatures.filter(f => perms[f] !== false && (perms[f] === true || perms[f] === undefined)).length
  const agentOn = agentFeatures.filter(f => perms[f]).length

  const modelRestricted = user.allowed_models?.enabled

  return (
    <button
      className="btn btn-sm btn-secondary"
      onClick={onClick}
      style={{ fontSize: '0.7rem', padding: '2px 8px' }}
      title="Edit permissions"
    >
      <i className="fas fa-shield-halved" style={{ marginRight: 4 }} />
      {apiOn}/{apiFeatures.length} API, {agentOn}/{agentFeatures.length} Agent
      {modelRestricted && ' | Models restricted'}
    </button>
  )
}

function PermissionsModal({ user, featureMeta, availableModels, onClose, onSave, addToast }) {
  const [permissions, setPermissions] = useState({ ...(user.permissions || {}) })
  const [allowedModels, setAllowedModels] = useState(user.allowed_models || { enabled: false, models: [] })
  const [saving, setSaving] = useState(false)

  const apiFeatures = featureMeta?.api_features || []
  const agentFeatures = featureMeta?.agent_features || []

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

  const handleSave = async () => {
    setSaving(true)
    try {
      await adminUsersApi.setPermissions(user.id, permissions)
      await adminUsersApi.setModels(user.id, allowedModels)
      onSave(user.id, permissions, allowedModels)
      addToast(`Permissions updated for ${user.name || user.email}`, 'success')
      onClose()
    } catch (err) {
      addToast(`Failed to update permissions: ${err.message}`, 'error')
    } finally {
      setSaving(false)
    }
  }

  const overlayStyle = {
    position: 'fixed', top: 0, left: 0, right: 0, bottom: 0,
    background: 'rgba(0,0,0,0.6)', display: 'flex', alignItems: 'center',
    justifyContent: 'center', zIndex: 1000,
  }

  const modalStyle = {
    background: 'var(--color-bg-secondary)', borderRadius: 'var(--radius-lg)',
    border: '1px solid var(--color-border-default)',
    padding: 'var(--spacing-lg)', maxWidth: 600, width: '90vw',
    maxHeight: '80vh', overflow: 'auto', boxShadow: '0 8px 32px rgba(0,0,0,0.4)',
  }

  const sectionStyle = {
    marginBottom: 'var(--spacing-md)',
    padding: 'var(--spacing-sm) var(--spacing-md)',
    background: 'var(--color-bg-tertiary)',
    border: '1px solid var(--color-border-subtle)',
    borderRadius: 'var(--radius-md)',
  }

  const headerStyle = {
    display: 'flex', justifyContent: 'space-between', alignItems: 'center',
    marginBottom: 'var(--spacing-sm)',
  }

  const gridStyle = {
    display: 'flex', gap: '6px', flexWrap: 'wrap',
  }

  return (
    <div style={overlayStyle} onClick={onClose}>
      <div style={modalStyle} onClick={e => e.stopPropagation()}>
        <h3 style={{ margin: '0 0 var(--spacing-md) 0', fontSize: '1.1rem', color: 'var(--color-text-primary)' }}>
          Permissions for &ldquo;{user.name || user.email}&rdquo;
        </h3>

        {/* API Endpoints */}
        <div style={sectionStyle}>
          <div style={headerStyle}>
            <strong style={{ fontSize: '0.85rem', color: 'var(--color-text-primary)' }}>API Endpoints</strong>
            <div style={{ display: 'flex', gap: 4 }}>
              <button className="btn btn-sm btn-secondary" onClick={() => setAllFeatures(apiFeatures, true)} style={{ fontSize: '0.65rem', padding: '1px 5px' }}>All</button>
              <button className="btn btn-sm btn-secondary" onClick={() => setAllFeatures(apiFeatures, false)} style={{ fontSize: '0.65rem', padding: '1px 5px' }}>None</button>
            </div>
          </div>
          <div style={gridStyle}>
            {apiFeatures.map(f => (
              <button
                key={f.key}
                className={`btn btn-sm ${permissions[f.key] ? 'btn-primary' : 'btn-secondary'}`}
                onClick={() => toggleFeature(f.key)}
                style={{ fontSize: '0.7rem', padding: '3px 8px' }}
              >
                {f.label}
              </button>
            ))}
          </div>
        </div>

        {/* Agent Features */}
        <div style={sectionStyle}>
          <div style={headerStyle}>
            <strong style={{ fontSize: '0.85rem', color: 'var(--color-text-primary)' }}>Agent Features</strong>
            <div style={{ display: 'flex', gap: 4 }}>
              <button className="btn btn-sm btn-secondary" onClick={() => setAllFeatures(agentFeatures, true)} style={{ fontSize: '0.65rem', padding: '1px 5px' }}>All</button>
              <button className="btn btn-sm btn-secondary" onClick={() => setAllFeatures(agentFeatures, false)} style={{ fontSize: '0.65rem', padding: '1px 5px' }}>None</button>
            </div>
          </div>
          <div style={gridStyle}>
            {agentFeatures.map(f => (
              <button
                key={f.key}
                className={`btn btn-sm ${permissions[f.key] ? 'btn-primary' : 'btn-secondary'}`}
                onClick={() => toggleFeature(f.key)}
                style={{ fontSize: '0.7rem', padding: '3px 8px' }}
              >
                {f.label}
              </button>
            ))}
          </div>
        </div>

        {/* Model Access */}
        <div style={sectionStyle}>
          <div style={headerStyle}>
            <strong style={{ fontSize: '0.85rem', color: 'var(--color-text-primary)' }}>Model Access</strong>
          </div>
          <div style={{ marginBottom: 'var(--spacing-sm, 8px)' }}>
            <label style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: '0.8rem', cursor: 'pointer', color: 'var(--color-text-primary)' }}>
              <input
                type="checkbox"
                checked={allowedModels.enabled}
                onChange={() => setAllowedModels(prev => ({ ...prev, enabled: !prev.enabled }))}
              />
              Restrict to specific models
            </label>
          </div>
          {allowedModels.enabled && (
            <>
              <div style={{ display: 'flex', gap: 4, marginBottom: 8 }}>
                <button className="btn btn-sm btn-secondary" onClick={() => setAllModels(true)} style={{ fontSize: '0.65rem', padding: '1px 5px' }}>All</button>
                <button className="btn btn-sm btn-secondary" onClick={() => setAllModels(false)} style={{ fontSize: '0.65rem', padding: '1px 5px' }}>None</button>
              </div>
              <div style={{ ...gridStyle, maxHeight: 200, overflow: 'auto' }}>
                {(availableModels || []).map(m => (
                  <button
                    key={m}
                    className={`btn btn-sm ${(allowedModels.models || []).includes(m) ? 'btn-primary' : 'btn-secondary'}`}
                    onClick={() => toggleModel(m)}
                    style={{ fontSize: '0.7rem', padding: '3px 8px' }}
                  >
                    {m}
                  </button>
                ))}
                {(!availableModels || availableModels.length === 0) && (
                  <span style={{ fontSize: '0.75rem', color: 'var(--color-text-secondary)' }}>No models available</span>
                )}
              </div>
            </>
          )}
        </div>

        {/* Actions */}
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--spacing-sm)', marginTop: 'var(--spacing-md)' }}>
          <button className="btn btn-secondary" onClick={onClose}>Cancel</button>
          <button className="btn btn-primary" onClick={handleSave} disabled={saving}>
            {saving ? 'Saving...' : 'Save'}
          </button>
        </div>
      </div>
    </div>
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
    return (
      <span
        style={{
          display: 'inline-block',
          padding: '2px 8px',
          borderRadius: 'var(--radius-sm, 4px)',
          fontSize: '0.75rem',
          fontWeight: 600,
          background: 'var(--color-danger, #ef4444)22',
          color: 'var(--color-danger, #ef4444)',
        }}
      >
        expired
      </span>
    )
  }
  return (
    <span
      style={{
        display: 'inline-block',
        padding: '2px 8px',
        borderRadius: 'var(--radius-sm, 4px)',
        fontSize: '0.75rem',
        fontWeight: 600,
        background: 'var(--color-success, #22c55e)22',
        color: 'var(--color-success, #22c55e)',
      }}
    >
      available
    </span>
  )
}

function isInviteAvailable(invite) {
  return !invite.usedBy && new Date(invite.expiresAt) > new Date()
}

function InvitesTab({ addToast }) {
  const [invites, setInvites] = useState([])
  const [loading, setLoading] = useState(true)
  const [creating, setCreating] = useState(false)

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
      await adminInvitesApi.create(168) // 7 days
      addToast('Invite link created', 'success')
      fetchInvites()
    } catch (err) {
      addToast(`Failed to create invite: ${err.message}`, 'error')
    } finally {
      setCreating(false)
    }
  }

  const handleRevoke = async (invite) => {
    if (!window.confirm('Revoke this invite link?')) return
    try {
      await adminInvitesApi.delete(invite.id)
      setInvites(prev => prev.filter(x => x.id !== invite.id))
      addToast('Invite revoked', 'success')
    } catch (err) {
      addToast(`Failed to revoke invite: ${err.message}`, 'error')
    }
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
      <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}>
        <LoadingSpinner size="lg" />
      </div>
    )
  }

  return (
    <>
      <div style={{ display: 'flex', gap: 'var(--spacing-sm)', marginBottom: 'var(--spacing-md)', alignItems: 'center' }}>
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
                <th style={{ width: 120 }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {invites.map(inv => (
                <tr key={inv.id}>
                  <td style={{ fontSize: '0.8rem', maxWidth: 320 }}>
                    {isInviteAvailable(inv) ? (
                      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                        <span
                          style={{
                            fontFamily: 'JetBrains Mono, monospace',
                            overflow: 'hidden',
                            textOverflow: 'ellipsis',
                            whiteSpace: 'nowrap',
                            flex: 1,
                            color: 'var(--color-text-secondary)',
                          }}
                          title={`${window.location.origin}/invite/${inv.code}`}
                        >
                          {`${window.location.origin}/invite/${inv.code}`}
                        </span>
                        <button
                          className="btn btn-sm btn-secondary"
                          onClick={() => handleCopyUrl(inv.code)}
                          title="Copy invite URL"
                          style={{ fontSize: '0.7rem', padding: '2px 6px', flexShrink: 0 }}
                        >
                          <i className="fas fa-copy" /> Copy
                        </button>
                      </div>
                    ) : (
                      <span style={{ fontFamily: 'JetBrains Mono, monospace', color: 'var(--color-text-secondary)' }}>
                        {inv.code.substring(0, 16)}...
                      </span>
                    )}
                  </td>
                  <td><InviteStatusBadge invite={inv} /></td>
                  <td style={{ fontSize: '0.8125rem' }}>
                    {inv.createdBy?.name || inv.createdBy?.id || '-'}
                  </td>
                  <td style={{ fontSize: '0.8125rem' }}>
                    {inv.usedBy?.name || inv.usedBy?.id || '\u2014'}
                  </td>
                  <td style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>
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
    </>
  )
}

export default function Users() {
  const { addToast } = useOutletContext()
  const { user: currentUser } = useAuth()
  const [users, setUsers] = useState([])
  const [loading, setLoading] = useState(true)
  const [search, setSearch] = useState('')
  const [activeTab, setActiveTab] = useState('users')
  const [editingUser, setEditingUser] = useState(null)
  const [featureMeta, setFeatureMeta] = useState(null)
  const [availableModels, setAvailableModels] = useState([])

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
    if (!window.confirm(`Delete user "${u.name || u.email}"? This will also remove their sessions and API keys.`)) return
    try {
      await adminUsersApi.delete(u.id)
      setUsers(prev => prev.filter(x => x.id !== u.id))
      addToast(`User deleted`, 'success')
    } catch (err) {
      addToast(`Failed to delete user: ${err.message}`, 'error')
    }
  }

  const filtered = users.filter(u => {
    if (!search) return true
    const q = search.toLowerCase()
    return (u.name || '').toLowerCase().includes(q) || (u.email || '').toLowerCase().includes(q)
  })

  const handlePermissionSave = (userId, newPerms, newModels) => {
    setUsers(prev => prev.map(u => u.id === userId ? { ...u, permissions: newPerms, allowed_models: newModels } : u))
  }

  const isSelf = (u) => currentUser && (u.id === currentUser.id || u.email === currentUser.email)

  return (
    <div className="page">
      <div className="page-header">
        <h1 className="page-title">Users</h1>
        <p className="page-subtitle">Manage registered users, roles, and invites</p>
      </div>

      {/* Tab bar */}
      <div style={{ display: 'flex', gap: 'var(--spacing-xs)', marginBottom: 'var(--spacing-md)', borderBottom: '1px solid var(--color-border)' }}>
        <button
          className={`btn btn-sm ${activeTab === 'users' ? 'btn-primary' : 'btn-secondary'}`}
          onClick={() => setActiveTab('users')}
          style={{ borderRadius: 'var(--radius-sm) var(--radius-sm) 0 0' }}
        >
          <i className="fas fa-users" /> Users
        </button>
        <button
          className={`btn btn-sm ${activeTab === 'invites' ? 'btn-primary' : 'btn-secondary'}`}
          onClick={() => setActiveTab('invites')}
          style={{ borderRadius: 'var(--radius-sm) var(--radius-sm) 0 0' }}
        >
          <i className="fas fa-envelope-open-text" /> Invites
        </button>
      </div>

      {activeTab === 'invites' ? (
        <InvitesTab addToast={addToast} />
      ) : (
        <>
          <div style={{ display: 'flex', gap: 'var(--spacing-sm)', marginBottom: 'var(--spacing-md)', alignItems: 'center' }}>
            <div style={{ position: 'relative', flex: 1, maxWidth: 360 }}>
              <i className="fas fa-search" style={{ position: 'absolute', left: 10, top: '50%', transform: 'translateY(-50%)', color: 'var(--color-text-secondary)', fontSize: '0.8rem' }} />
              <input
                type="text"
                className="input"
                placeholder="Search by name or email..."
                value={search}
                onChange={e => setSearch(e.target.value)}
                style={{ paddingLeft: 32 }}
              />
            </div>
            <button className="btn btn-secondary btn-sm" onClick={fetchUsers} disabled={loading}>
              <i className="fas fa-rotate" /> Refresh
            </button>
          </div>

          {loading ? (
            <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}>
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
                    <th style={{ width: 140 }}>Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {filtered.map(u => (
                    <tr key={u.id}>
                      <td>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
                          {u.avatarUrl ? (
                            <img src={u.avatarUrl} alt="" style={{ width: 28, height: 28, borderRadius: '50%' }} />
                          ) : (
                            <i className="fas fa-user-circle" style={{ fontSize: '1.5rem', color: 'var(--color-text-secondary)' }} />
                          )}
                          <span style={{ fontSize: '0.875rem', fontWeight: 500 }}>{u.name || '(no name)'}</span>
                        </div>
                      </td>
                      <td style={{ fontSize: '0.8125rem', fontFamily: 'JetBrains Mono, monospace' }}>{u.email}</td>
                      <td><ProviderBadge provider={u.provider} /></td>
                      <td><RoleBadge role={u.role} /></td>
                      <td>
                        <PermissionSummary
                          user={u}
                          onClick={() => u.role !== 'admin' && setEditingUser(u)}
                        />
                      </td>
                      <td><StatusBadge status={u.status} /></td>
                      <td style={{ fontSize: '0.8125rem', color: 'var(--color-text-secondary)' }}>
                        {u.createdAt ? new Date(u.createdAt).toLocaleDateString() : '-'}
                      </td>
                      <td>
                        {!isSelf(u) && (
                          <div style={{ display: 'flex', gap: 'var(--spacing-xs)' }}>
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
    </div>
  )
}
