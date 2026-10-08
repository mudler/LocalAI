/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState, useEffect, useCallback, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { useAuth } from '../../context/AuthContext'
import { adminUsersApi } from '../../utils/api'
import { filterUsers, filterCounts, accessSummary, initialOf, userState, USER_FILTERS } from '../../utils/access'
import LoadingSpinner from '../LoadingSpinner'
import ActionMenu from '../ActionMenu'
import Dialog from '../Dialog'
import HomeUndoToast from '../home/HomeUndoToast'
import AccessSheet from './AccessSheet'
import Icon from '../Icon'

const FILTER_LABELS = { all: 'All', pending: 'Pending', admins: 'Admins', disabled: 'Disabled' }
const STATE_LABELS = { active: 'Active', pending: 'Pending', disabled: 'Disabled' }

const FALLBACK_FEATURES = {
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
  general_features: [{ key: 'fine_tuning', label: 'Fine-Tuning', default: false }],
}

const SORTERS = {
  name: (a, b) => (a.name || a.email || '').localeCompare(b.name || b.email || ''),
  provider: (a, b) => (a.provider || '').localeCompare(b.provider || ''),
  role: (a, b) => (a.role || '').localeCompare(b.role || ''),
  status: (a, b) => (a.status || '').localeCompare(b.status || ''),
  created: (a, b) => new Date(a.createdAt || 0) - new Date(b.createdAt || 0),
}

function Avatar({ user }) {
  return user.avatarUrl
    ? <img src={user.avatarUrl} alt="" className="us-avatar" />
    : <span className="us-avatar us-avatar--initial" aria-hidden="true">{initialOf(user)}</span>
}

// The people who can sign in: filter, search, sort, approve or disable, and
// open one person's access. Role, password reset and delete sit in the row's
// menu. Every call is the one the page made before.
export default function UsersPanel({ addToast, onInvite, registrationMode }) {
  const { t } = useTranslation('auth')
  const { user: currentUser } = useAuth()
  const [users, setUsers] = useState([])
  const [loading, setLoading] = useState(true)
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState('all')
  const [sort, setSort] = useState({ key: null, dir: 'asc' })
  const [editingUser, setEditingUser] = useState(null)
  const [featureMeta, setFeatureMeta] = useState(null)
  const [availableModels, setAvailableModels] = useState([])
  const [deleting, setDeleting] = useState(null)
  const [typed, setTyped] = useState('')
  const [deleteBusy, setDeleteBusy] = useState(false)
  const [resetUser, setResetUser] = useState(null)
  const [newPassword, setNewPassword] = useState('')
  const [resetBusy, setResetBusy] = useState(false)
  const [weakWarning, setWeakWarning] = useState('')
  const [acknowledgeWeak, setAcknowledgeWeak] = useState(false)
  const [undo, setUndo] = useState(null)

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
      // The features endpoint may be unavailable: use the built-in list.
      setFeatureMeta(FALLBACK_FEATURES)
    }
  }, [])

  useEffect(() => { fetchUsers(); fetchFeatures() }, [fetchUsers, fetchFeatures])

  const isSelf = (u) => currentUser && (u.id === currentUser.id || u.email === currentUser.email)
  const label = (u) => u.name || u.email

  const setStatus = async (u, status, { undoable } = {}) => {
    try {
      await adminUsersApi.setStatus(u.id, status)
      setUsers(prev => prev.map(x => x.id === u.id ? { ...x, status } : x))
      if (undoable) setUndo({ id: Date.now(), user: u, from: u.status, text: `Disabled ${label(u)}. Undo sets them back to active.` })
      else addToast(`${status === 'active' ? 'Approved' : 'Disabled'} ${label(u)}`, 'success')
    } catch (err) {
      addToast(`Failed to ${status === 'active' ? 'approve' : 'disable'} user: ${err.message}`, 'error')
    }
  }

  const undoDisable = async () => {
    const entry = undo
    setUndo(null)
    if (!entry) return
    try {
      await adminUsersApi.setStatus(entry.user.id, entry.from === 'disabled' ? 'active' : entry.from)
      setUsers(prev => prev.map(x => x.id === entry.user.id ? { ...x, status: entry.from === 'disabled' ? 'active' : entry.from } : x))
      addToast(`${label(entry.user)} is active again`, 'success')
    } catch (err) {
      addToast(`Failed to restore user: ${err.message}`, 'error')
    }
  }

  const toggleRole = async (u) => {
    const role = u.role === 'admin' ? 'user' : 'admin'
    try {
      await adminUsersApi.setRole(u.id, role)
      setUsers(prev => prev.map(x => x.id === u.id ? { ...x, role } : x))
      addToast(`${label(u)} is now ${role}`, 'success')
    } catch (err) {
      addToast(`Failed to update role: ${err.message}`, 'error')
    }
  }

  const confirmDelete = async () => {
    const u = deleting
    setDeleteBusy(true)
    try {
      await adminUsersApi.delete(u.id)
      setUsers(prev => prev.filter(x => x.id !== u.id))
      addToast('User deleted', 'success')
      setDeleting(null)
    } catch (err) {
      addToast(`Failed to delete user: ${err.message}`, 'error')
    } finally {
      setDeleteBusy(false)
    }
  }

  const openReset = (u) => { setResetUser(u); setNewPassword(''); setWeakWarning(''); setAcknowledgeWeak(false) }

  const confirmReset = async () => {
    if (!resetUser || newPassword.length === 0) return
    setResetBusy(true)
    try {
      await adminUsersApi.resetPassword(resetUser.id, newPassword, acknowledgeWeak)
      addToast(`Password reset for ${label(resetUser)}`, 'success')
      setResetUser(null)
      setNewPassword('')
      setWeakWarning('')
      setAcknowledgeWeak(false)
    } catch (err) {
      if (err.body?.overridable) setWeakWarning(err.body.error || err.message)
      else addToast(`Failed to reset password: ${err.message}`, 'error')
    } finally {
      setResetBusy(false)
    }
  }

  const counts = useMemo(() => filterCounts(users), [users])
  const filtered = useMemo(() => filterUsers(users, { query, filter }), [users, query, filter])
  const rows = useMemo(() => (
    sort.key ? [...filtered].sort((a, b) => sort.dir === 'asc' ? SORTERS[sort.key](a, b) : SORTERS[sort.key](b, a)) : filtered
  ), [filtered, sort])
  const toggleSort = (key) => setSort(s => s.key === key ? { key, dir: s.dir === 'asc' ? 'desc' : 'asc' } : { key, dir: 'asc' })

  const sortTh = (key, text, className) => (
    <th scope="col" className={className} aria-sort={sort.key === key ? (sort.dir === 'asc' ? 'ascending' : 'descending') : undefined}>
      <button type="button" className="dk-table-sort" onClick={() => toggleSort(key)}>
        {text}{sort.key === key && <Icon name={`chevron-${sort.dir === 'asc' ? 'up' : 'down'}`} aria-hidden="true" />}
      </button>
    </th>
  )

  const menuItems = (u) => [
    { key: 'access', icon: 'shield', label: 'Edit access', hidden: u.role === 'admin', onClick: () => setEditingUser(u) },
    { key: 'role', icon: u.role === 'admin' ? 'arrow-down' : 'arrow-up', label: u.role === 'admin' ? 'Make user' : 'Make admin', onClick: () => toggleRole(u) },
    { key: 'password', icon: 'key', label: 'Reset password', hidden: !!u.provider && u.provider !== 'local', onClick: () => openReset(u) },
    { divider: true },
    { key: 'delete', icon: 'trash', label: 'Delete…', danger: true, onClick: () => { setDeleting(u); setTyped('') } },
  ]

  const handlePermissionSave = (userId, perms, models, quotas) => {
    setUsers(prev => prev.map(u => u.id === userId ? { ...u, permissions: perms, allowed_models: models, quotas } : u))
  }

  const sheetActions = (u) => (
    <>
      <p className="us-eyebrow">Account</p>
      <div className="us-account-acts">
        {(!u.provider || u.provider === 'local') && (
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => { setEditingUser(null); openReset(u) }}>Reset password</button>
        )}
        <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => { setEditingUser(null); toggleRole(u) }}>Make admin</button>
        <button type="button" className="dk-btn dk-btn--danger dk-btn--sm" onClick={() => { setEditingUser(null); setDeleting(u); setTyped('') }}>Delete…</button>
      </div>
    </>
  )

  const deleteName = deleting ? label(deleting) : ''

  return (
    <div className="us" data-testid="users-panel">
      <div className="us-bar">
        <div className="dk-input-icon us-search">
          <Icon name="search" className="dk-icon" />
          <input className="dk-input" type="search" aria-label="Search by name or email" placeholder="Search name or email" value={query} onChange={e => setQuery(e.target.value)} />
        </div>
        <div className="us-chips" role="group" aria-label="Filter users">
          {USER_FILTERS.map(f => (
            <button key={f} type="button" className="dk-chip" aria-pressed={filter === f} data-filter={f} onClick={() => setFilter(f)}>
              {FILTER_LABELS[f]}{f !== 'all' || counts.all ? <span className="us-chip-count">{counts[f]}</span> : null}
            </button>
          ))}
        </div>
        <div className="us-bar__acts">
          <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon" aria-label="Refresh users" onClick={fetchUsers} disabled={loading}><Icon name="refresh" spin={loading} /></button>
          <button type="button" className="dk-btn dk-btn--primary" onClick={onInvite}><Icon name="plus" /> Invite someone</button>
        </div>
      </div>

      {loading && users.length === 0 ? (
        <div className="ak-loading"><LoadingSpinner size="lg" /></div>
      ) : rows.length === 0 ? (
        <div className="dk-empty">
          <div className="dk-empty-icon"><Icon name="users" /></div>
          <h3 className="dk-empty-title">{query || filter !== 'all' ? 'No matching users' : 'No users'}</h3>
          <p className="dk-empty-text">{query || filter !== 'all' ? 'Try a different search or filter.' : 'No registered users found.'}</p>
          {(query || filter !== 'all') && <button type="button" className="dk-btn dk-btn--secondary" onClick={() => { setQuery(''); setFilter('all') }}>Clear filters</button>}
        </div>
      ) : (
        <div className="dk-table-wrap dk-table-wrap--flat" role="region" aria-label="Users" tabIndex={0}>
          <table className="dk-table" data-testid="users-table" aria-busy={loading || undefined}>
            <caption className="dk-sr-only">Users with sign-in method, role, access and status</caption>
            <thead>
              <tr>
                {sortTh('name', 'User')}
                {sortTh('provider', 'Sign-in', 'dk-hide-phone')}
                {sortTh('role', 'Role')}
                <th scope="col" className="dk-hide-phone">Access</th>
                {sortTh('status', 'Status')}
                <th scope="col"><span className="dk-sr-only">Actions</span></th>
              </tr>
            </thead>
            <tbody>
              {rows.map(u => {
                const state = userState(u)
                const self = isSelf(u)
                return (
                  <tr key={u.id} data-row data-user={u.email}>
                    <td>
                      <div className="us-who">
                        <Avatar user={u} />
                        <div className="us-who__text">
                          <span className="dk-table-name">{u.name || '(no name)'}{self && <span className="us-you">you</span>}</span>
                          <span className="dk-table-sub">{u.email}</span>
                        </div>
                      </div>
                    </td>
                    <td className="dk-hide-phone"><span className="dk-badge">{u.provider || 'local'}</span></td>
                    <td><span className={`dk-badge${u.role === 'admin' ? ' dk-badge--accent' : ''}`}>{u.role}</span></td>
                    <td className="dk-hide-phone">
                      {u.role === 'admin' ? (
                        <span className="us-access">{accessSummary(u, featureMeta)}</span>
                      ) : (
                        <button type="button" className="us-access us-access--button" onClick={() => setEditingUser(u)} aria-label={`Edit access for ${label(u)}`} title="Edit access">
                          {accessSummary(u, featureMeta)}
                        </button>
                      )}
                    </td>
                    <td>
                      <span className="us-state" data-state={state === 'active' ? 'active' : state === 'pending' ? 'warn' : 'muted'}>{STATE_LABELS[state]}</span>
                      <span className="dk-table-sub">{u.createdAt ? `Created ${new Date(u.createdAt).toLocaleDateString()}` : ''}</span>
                    </td>
                    <td className="dk-num">
                      {!self && (
                        <div className="us-acts">
                          {state !== 'active' ? (
                            <button type="button" className="dk-btn dk-btn--primary dk-btn--sm" onClick={() => setStatus(u, 'active')} title="Approve user">
                              {state === 'disabled' ? 'Enable' : 'Approve'}
                            </button>
                          ) : (
                            <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" onClick={() => setStatus(u, 'disabled', { undoable: true })} title="Disable user" aria-label={`Disable ${label(u)}`}>
                              <Icon name="ban" />
                            </button>
                          )}
                          <ActionMenu items={menuItems(u)} ariaLabel={`Actions for ${label(u)}`} />
                        </div>
                      )}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      {registrationMode && (
        <p className="us-foot" data-testid="registration-mode">{t('registration.label', { mode: registrationMode })} {t(`registration.${registrationMode}`, { defaultValue: '' })}</p>
      )}

      {editingUser && featureMeta && (
        <AccessSheet
          user={editingUser} featureMeta={featureMeta} availableModels={availableModels} addToast={addToast}
          onClose={() => setEditingUser(null)} onSave={handlePermissionSave} actions={sheetActions(editingUser)}
        />
      )}

      {resetUser && (
        <Dialog
          title="Reset Password" labelId="us-reset-title" onClose={() => setResetUser(null)} pending={resetBusy}
          description={`Set a new password for ${label(resetUser)}. All existing sessions will be invalidated.`}
          foot={<>
            <button type="button" className="dk-btn dk-btn--secondary" onClick={() => setResetUser(null)} disabled={resetBusy}>Cancel</button>
            <button type="button" className="dk-btn dk-btn--primary" onClick={confirmReset} disabled={resetBusy || newPassword.length === 0}>
              {resetBusy ? 'Resetting...' : 'Reset Password'}
            </button>
          </>}
        >
          <div className="dk-field">
            <label className="dk-label" htmlFor="us-reset-pw">New password</label>
            <input
              id="us-reset-pw" type="password" className="dk-input" placeholder="New password (min 12 characters)" value={newPassword} autoComplete="new-password"
              onChange={e => { setNewPassword(e.target.value); setWeakWarning(''); setAcknowledgeWeak(false) }}
              onKeyDown={e => { if (e.key === 'Enter' && newPassword.length > 0) confirmReset() }}
            />
          </div>
          {weakWarning && (
            <div role="alert" className="us-weak">
              <p className="us-weak__text">{weakWarning}</p>
              <label className="dk-choice">
                <input type="checkbox" className="dk-check" checked={acknowledgeWeak} onChange={e => setAcknowledgeWeak(e.target.checked)} />
                Use this password anyway
              </label>
            </div>
          )}
        </Dialog>
      )}

      {deleting && (
        <Dialog
          role="alertdialog" title="Delete User" labelId="us-delete-title" onClose={() => setDeleting(null)} pending={deleteBusy}
          description="This also removes their sessions and API keys. It cannot be undone."
          foot={<>
            <button type="button" className="dk-btn dk-btn--secondary" onClick={() => setDeleting(null)} disabled={deleteBusy}>Cancel</button>
            <button type="button" className="dk-btn dk-btn--danger" onClick={confirmDelete} disabled={deleteBusy || typed.trim() !== deleteName}>
              {deleteBusy ? 'Deleting...' : 'Delete'}
            </button>
          </>}
        >
          <div className="dk-field">
            <label className="dk-label" htmlFor="us-delete-name">Type <span className="dk-mono">{deleteName}</span> to confirm</label>
            <input id="us-delete-name" className="dk-input" value={typed} autoComplete="off" onChange={e => setTyped(e.target.value)} />
          </div>
        </Dialog>
      )}

      {undo && (
        <HomeUndoToast
          key={undo.id} duration={8000} testId="user-undo-toast" message={undo.text}
          undoLabel="Undo" dismissLabel="Dismiss" onUndo={undoDisable} onExpire={() => setUndo(null)}
        />
      )}
    </div>
  )
}
