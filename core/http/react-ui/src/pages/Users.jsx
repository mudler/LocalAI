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

const FEATURES = [
  { key: 'agents', label: 'Agents' },
  { key: 'skills', label: 'Skills' },
  { key: 'collections', label: 'Collections' },
  { key: 'mcp_jobs', label: 'MCP CI Jobs' },
]

function PermissionToggles({ user, onUpdate, addToast }) {
  const permissions = user.permissions || {}

  const handleToggle = async (featureKey) => {
    const updated = { ...permissions, [featureKey]: !permissions[featureKey] }
    try {
      await adminUsersApi.setPermissions(user.id, updated)
      onUpdate(user.id, updated)
      addToast(`Permissions updated for ${user.name || user.email}`, 'success')
    } catch (err) {
      addToast(`Failed to update permissions: ${err.message}`, 'error')
    }
  }

  if (user.role === 'admin') {
    return (
      <span style={{ fontSize: '0.75rem', color: 'var(--color-text-secondary)', fontStyle: 'italic' }}>
        All (admin)
      </span>
    )
  }

  return (
    <div style={{ display: 'flex', gap: '6px', flexWrap: 'wrap' }}>
      {FEATURES.map(f => (
        <button
          key={f.key}
          className={`btn btn-sm ${permissions[f.key] ? 'btn-primary' : 'btn-secondary'}`}
          onClick={() => handleToggle(f.key)}
          title={`${permissions[f.key] ? 'Disable' : 'Enable'} ${f.label}`}
          style={{ fontSize: '0.7rem', padding: '2px 6px' }}
        >
          {f.label}
        </button>
      ))}
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

  useEffect(() => {
    fetchUsers()
  }, [fetchUsers])

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

  const handlePermissionUpdate = (userId, newPerms) => {
    setUsers(prev => prev.map(u => u.id === userId ? { ...u, permissions: newPerms } : u))
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
                        <PermissionToggles
                          user={u}
                          onUpdate={handlePermissionUpdate}
                          addToast={addToast}
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
    </div>
  )
}
