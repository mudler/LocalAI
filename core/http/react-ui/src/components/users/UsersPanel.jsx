/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState, useEffect, useCallback, useMemo } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { useAuth } from '../../context/AuthContext'
import { useViewAs } from '../../contexts/ViewAsContext'
import { adminUsersApi } from '../../utils/api'
import { canViewAs, filterUsers, filterCounts, accessInfo, accessText, initialOf, userState, USER_FILTERS } from '../../utils/access'
import { relativeTime } from '../../utils/p2p'
import ActionMenu from '../ActionMenu'
import Dialog from '../Dialog'
import HomeUndoToast from '../home/HomeUndoToast'
import AccessSheet from './AccessSheet'
import Icon from '../Icon'
import AdminFilterBar from '../admin/AdminFilterBar'
import { SummaryStrip } from '../admin/AdminPage'
import { Button, EmptyState, LoadError, SkeletonRows, Tag } from '../ui'

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
    { key: 'collections', label: 'Collections', default: false },
    { key: 'localai_assistant', label: 'LocalAI Assistant', default: false },
    { key: 'agents', label: 'Saved agents', default: false },
    { key: 'triggers', label: 'Scheduled triggers', default: false },
    { key: 'connectors', label: 'Chat connectors', default: false },
    { key: 'mcp_jobs', label: 'Agent tasks and jobs', default: false },
    { key: 'pages', label: 'Pages (HTML pages in the workspace)', default: false },
  ],
  general_features: [{ key: 'fine_tuning', label: 'Fine-Tuning', default: false }],
}

const time = (v) => (v ? new Date(v).getTime() : 0)
const SORTERS = {
  name: (a, b) => (a.name || a.email || '').localeCompare(b.name || b.email || ''),
  provider: (a, b) => (a.provider || '').localeCompare(b.provider || ''),
  role: (a, b) => (a.role || '').localeCompare(b.role || ''),
  status: (a, b) => (a.status || '').localeCompare(b.status || ''),
  created: (a, b) => time(a.createdAt) - time(b.createdAt),
  // Most recent first on the first press; a person never seen sorts last.
  lastActive: (a, b) => time(b.lastActiveAt) - time(a.lastActiveAt),
  keys: (a, b) => (b.apiKeyCount || 0) - (a.apiKeyCount || 0),
}

const SORT_IDS = ['name', 'role', 'status', 'provider', 'lastActive', 'keys']

function Avatar({ user }) {
  return user.avatarUrl
    ? <img src={user.avatarUrl} alt="" className="us-avatar" />
    : <span className="us-avatar us-avatar--initial" aria-hidden="true">{initialOf(user)}</span>
}

const stateTone = (state) => (state === 'active' ? 'active' : state === 'pending' ? 'warn' : 'muted')

// The people who can sign in: filter, search, sort, approve or disable, and
// open one person's access. Role, password reset and delete sit in the row's
// menu. `Last active` and `Keys` are read from the list: the server sends them
// to administrators only.
export default function UsersPanel({ addToast, registrationMode, initialQuery = '', onInvite }) {
  const { t, i18n } = useTranslation('admin')
  const { t: ta } = useTranslation('auth')
  const { t: tv } = useTranslation('viewas')
  const viewAs = useViewAs()
  const navigate = useNavigate()
  const { user: currentUser } = useAuth()
  const [users, setUsers] = useState([])
  const [loaded, setLoaded] = useState(false)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState(null)
  const [loadedAt, setLoadedAt] = useState(null)
  const [query, setQuery] = useState(initialQuery)
  const [filter, setFilter] = useState('all')
  const [provider, setProvider] = useState('')
  const [accessFilter, setAccessFilter] = useState('')
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
      setLoadError(null)
      setLoadedAt(Date.now())
    } catch (err) {
      setLoadError(err)
    } finally {
      setLoaded(true)
      setLoading(false)
    }
  }, [])

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

  // The default of a configurable feature applies to every user whose access
  // does not name it; the answer is the feature with its new default.
  const setFeatureDefault = async (key, enabled) => {
    try {
      const meta = await adminUsersApi.setFeatureDefault(key, enabled)
      const value = typeof meta?.default === 'boolean' ? meta.default : enabled
      setFeatureMeta(prev => {
        if (!prev) return prev
        const patch = list => (list || []).map(f => (f.key === key ? { ...f, default: value } : f))
        return { ...prev, api_features: patch(prev.api_features), agent_features: patch(prev.agent_features), general_features: patch(prev.general_features) }
      })
      addToast(t(value ? 'users.toast.defaultOn' : 'users.toast.defaultOff'), 'success')
    } catch (err) {
      addToast(t('users.toast.defaultFailed', { message: err.message }), 'error')
    }
  }

  const isSelf = (u) => currentUser && (u.id === currentUser.id || u.email === currentUser.email)
  const label = (u) => u.name || u.email
  const stateLabel = (state) => t(`users.state.${state}`)

  const setStatus = async (u, status, { undoable } = {}) => {
    try {
      await adminUsersApi.setStatus(u.id, status)
      setUsers(prev => prev.map(x => x.id === u.id ? { ...x, status } : x))
      if (undoable) setUndo({ id: Date.now(), user: u, from: u.status, text: t('users.toast.disabledUndo', { name: label(u) }) })
      else addToast(t(status === 'active' ? 'users.toast.approved' : 'users.toast.disabled', { name: label(u) }), 'success')
    } catch (err) {
      addToast(t(status === 'active' ? 'users.toast.approveFailed' : 'users.toast.disableFailed', { message: err.message }), 'error')
    }
  }

  const undoDisable = async () => {
    const entry = undo
    setUndo(null)
    if (!entry) return
    try {
      await adminUsersApi.setStatus(entry.user.id, entry.from === 'disabled' ? 'active' : entry.from)
      setUsers(prev => prev.map(x => x.id === entry.user.id ? { ...x, status: entry.from === 'disabled' ? 'active' : entry.from } : x))
      addToast(t('users.toast.restored', { name: label(entry.user) }), 'success')
    } catch (err) {
      addToast(t('users.toast.restoreFailed', { message: err.message }), 'error')
    }
  }

  const toggleRole = async (u) => {
    const role = u.role === 'admin' ? 'user' : 'admin'
    try {
      await adminUsersApi.setRole(u.id, role)
      setUsers(prev => prev.map(x => x.id === u.id ? { ...x, role } : x))
      addToast(t('users.toast.roleChanged', { name: label(u), role: t(`users.role.${role}`) }), 'success')
    } catch (err) {
      addToast(t('users.toast.roleFailed', { message: err.message }), 'error')
    }
  }

  const confirmDelete = async () => {
    const u = deleting
    setDeleteBusy(true)
    try {
      await adminUsersApi.delete(u.id)
      setUsers(prev => prev.filter(x => x.id !== u.id))
      addToast(t('users.toast.deleted'), 'success')
      setDeleting(null)
    } catch (err) {
      addToast(t('users.toast.deleteFailed', { message: err.message }), 'error')
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
      addToast(t('users.toast.passwordReset', { name: label(resetUser) }), 'success')
      setResetUser(null)
      setNewPassword('')
      setWeakWarning('')
      setAcknowledgeWeak(false)
    } catch (err) {
      if (err.body?.overridable) setWeakWarning(err.body.error || err.message)
      else addToast(t('users.toast.resetFailed', { message: err.message }), 'error')
    } finally {
      setResetBusy(false)
    }
  }

  const counts = useMemo(() => filterCounts(users), [users])
  const providers = useMemo(() => [...new Set(users.map(u => u.provider || 'local'))].sort(), [users])
  const filtered = useMemo(
    () => filterUsers(users, { query, filter, provider, access: accessFilter, featureMeta }),
    [users, query, filter, provider, accessFilter, featureMeta],
  )
  const rows = useMemo(() => (
    sort.key ? [...filtered].sort((a, b) => sort.dir === 'asc' ? SORTERS[sort.key](a, b) : SORTERS[sort.key](b, a)) : filtered
  ), [filtered, sort])
  const toggleSort = (key) => setSort(s => s.key === key ? { key, dir: s.dir === 'asc' ? 'desc' : 'asc' } : { key, dir: 'asc' })
  const narrowed = !!(query || filter !== 'all' || provider || accessFilter)
  const clearFilters = () => { setQuery(''); setFilter('all'); setProvider(''); setAccessFilter('') }
  const lang = i18n.language

  const sortTh = (key, text, className) => (
    <th scope="col" className={className} aria-sort={sort.key === key ? (sort.dir === 'asc' ? 'ascending' : 'descending') : undefined}>
      <button type="button" className="dk-table-sort" onClick={() => toggleSort(key)}>
        {text}{sort.key === key && <Icon name={`chevron-${sort.dir === 'asc' ? 'up' : 'down'}`} aria-hidden="true" />}
      </button>
    </th>
  )

  // View as: an active person who is not the viewer and not an administrator.
  // Read-only first; the bar then offers Allow changes.
  const canView = (u) => viewAs.canViewAs && canViewAs(u, currentUser)
  const startViewAs = (u) => {
    viewAs.enter({ id: u.id, name: label(u) })
    navigate('/app/projects')
  }

  const menuItems = (u) => [
    { key: 'viewas', icon: 'eye', label: tv('users.action'), hidden: !canView(u), onClick: () => startViewAs(u) },
    { key: 'access', icon: 'shield', label: t('users.menu.editAccess'), hidden: u.role === 'admin', onClick: () => setEditingUser(u) },
    { key: 'role', icon: u.role === 'admin' ? 'arrow-down' : 'arrow-up', label: u.role === 'admin' ? t('users.menu.makeUser') : t('users.menu.makeAdmin'), onClick: () => toggleRole(u) },
    { key: 'password', icon: 'key', label: t('users.menu.resetPassword'), hidden: !!u.provider && u.provider !== 'local', onClick: () => openReset(u) },
    { divider: true },
    { key: 'delete', icon: 'trash', label: t('users.menu.delete'), danger: true, onClick: () => { setDeleting(u); setTyped('') } },
  ]

  const handlePermissionSave = (userId, perms, models, quotas, explicit) => {
    setUsers(prev => prev.map(u => u.id === userId ? { ...u, permissions: perms, explicit_permissions: explicit, allowed_models: models, quotas } : u))
  }

  const sheetActions = (u) => (
    <>
      <p className="us-eyebrow">{t('users.sheet.account')}</p>
      <div className="us-account-acts">
        {(!u.provider || u.provider === 'local') && (
          <Button size="sm" onClick={() => { setEditingUser(null); openReset(u) }}>{t('users.menu.resetPassword')}</Button>
        )}
        <Button size="sm" onClick={() => { setEditingUser(null); toggleRole(u) }}>{u.role === 'admin' ? t('users.menu.makeUser') : t('users.menu.makeAdmin')}</Button>
        <Button size="sm" variant="danger" onClick={() => { setEditingUser(null); setDeleting(u); setTyped('') }}>{t('users.menu.delete')}</Button>
      </div>
    </>
  )

  const deleteName = deleting ? label(deleting) : ''
  const activeCount = users.filter(u => userState(u) === 'active').length
  const waitingCount = users.filter(u => userState(u) === 'pending').length
  const filtersSet = (provider ? 1 : 0) + (accessFilter ? 1 : 0)
  const firstLoad = !loaded && loading
  const failedFirst = loaded && loadError && users.length === 0

  return (
    <div className="us" data-testid="users-panel">
      {users.length > 0 && (
        <SummaryStrip
          label={t('users.summary.label')}
          items={[
            { key: 'total', label: t('users.summary.total'), value: users.length, unit: t('users.summary.totalUnit') },
            { key: 'active', label: t('users.summary.active'), value: activeCount, unit: t('users.summary.activeUnit') },
            { key: 'waiting', label: t('users.summary.waiting'), value: waitingCount, unit: t('users.summary.waitingUnit'), tone: waitingCount ? 'warn' : undefined },
            { key: 'admins', label: t('users.summary.admins'), value: counts.admins, unit: t('users.summary.adminsUnit', { count: users.length }) },
          ]}
        />
      )}

      <AdminFilterBar
        search={{ value: query, onChange: setQuery, label: t('users.search.label'), placeholder: t('users.search.placeholder') }}
        chips={{
          label: t('users.filters.label'),
          value: filter,
          onChange: setFilter,
          options: USER_FILTERS.map(f => ({ id: f, label: t(`users.filters.${f}`), count: f !== 'all' || counts.all ? counts[f] : undefined })),
        }}
        filters={{
          count: filtersSet,
          children: (
            <>
              <label className="admin-filters__field">
                <span>{t('users.popover.signIn')}</span>
                <span className="dk-select-wrap">
                  <select className="dk-select" value={provider} onChange={e => setProvider(e.target.value)} data-testid="users-filter-provider">
                    <option value="">{t('users.popover.any')}</option>
                    {providers.map(p => <option key={p} value={p}>{p}</option>)}
                  </select>
                </span>
              </label>
              <label className="admin-filters__field">
                <span>{t('users.popover.access')}</span>
                <span className="dk-select-wrap">
                  <select className="dk-select" value={accessFilter} onChange={e => setAccessFilter(e.target.value)} data-testid="users-filter-access">
                    <option value="">{t('users.popover.any')}</option>
                    <option value="custom">{t('users.popover.custom')}</option>
                  </select>
                </span>
              </label>
            </>
          ),
        }}
        sort={{
          label: t('users.sort.label'),
          value: sort.key || 'name',
          onChange: key => setSort({ key, dir: 'asc' }),
          options: SORT_IDS.map(id => ({ id, label: t(`users.sort.${id}`) })),
        }}
        extra={<Button variant="ghost" icon="refresh" aria-label={t('users.refresh')} onClick={fetchUsers} disabled={loading} />}
      />

      {loadError && users.length > 0 && (
        <LoadError
          title={t('users.error.stale')} error={loadError} nothingChanged={false} onRetry={fetchUsers} testId="users-stale-error"
        >
          {loadedAt ? <> {t('users.error.staleNote', { when: new Date(loadedAt).toLocaleTimeString(lang, { hour: '2-digit', minute: '2-digit' }) })}</> : null}
        </LoadError>
      )}

      {firstLoad ? (
        <SkeletonRows count={4} label={t('users.loading')} testId="users-loading" />
      ) : failedFirst ? (
        <LoadError title={t('users.error.title')} error={loadError} onRetry={fetchUsers} testId="users-load-error" />
      ) : users.length === 0 ? (
        <EmptyState
          icon="users" testId="users-empty" level={3}
          title={t('users.empty.title')} body={t('users.empty.body')}
          actions={onInvite ? <Button variant="primary" icon="plus" onClick={onInvite}>{t('users.invite')}</Button> : null}
        />
      ) : rows.length === 0 ? (
        <EmptyState
          variant="no-results" icon="users" testId="users-no-match" level={3}
          title={t('users.noMatch.title')} body={t('users.noMatch.body')}
          actions={<Button onClick={clearFilters}>{t('users.noMatch.clear')}</Button>}
        />
      ) : (
        <div className="dk-table-wrap dk-table-wrap--flat" role="region" aria-label={t('users.region')} tabIndex={0}>
          <table className="dk-table" data-testid="users-table" aria-busy={loading || undefined}>
            <caption className="dk-sr-only">{t('users.caption')}</caption>
            <thead>
              <tr>
                {sortTh('name', t('users.columns.user'))}
                {sortTh('role', t('users.columns.role'), 'dk-hide-phone')}
                <th scope="col" className="dk-hide-phone">{t('users.columns.access')}</th>
                {sortTh('status', t('users.columns.status'), 'dk-hide-phone')}
                {sortTh('lastActive', t('users.columns.lastActive'), 'dk-hide-phone')}
                {sortTh('keys', t('users.columns.keys'), 'dk-hide-phone dk-num')}
                <th scope="col"><span className="dk-sr-only">{t('users.columns.actions')}</span></th>
              </tr>
            </thead>
            <tbody>
              {rows.map(u => {
                const state = userState(u)
                const self = isSelf(u)
                const info = accessInfo(u, featureMeta)
                const accessLabel = accessText(t, info)
                return (
                  <tr key={u.id} data-row data-user={u.email}>
                    <td>
                      <div className="us-who">
                        <Avatar user={u} />
                        <div className="us-who__text">
                          <span className="dk-table-name">{u.name || t('users.noName')}{self && <span className="us-you">{t('users.you')}</span>}<Tag tone={u.role === 'admin' ? 'accent' : 'neutral'} className="us-role-phone">{t(`users.role.${u.role === 'admin' ? 'admin' : 'user'}`)}</Tag></span>
                          <span className="dk-table-sub">{u.email} {'\u00b7'} {u.provider || 'local'}</span>
                          <span className="us-state us-state-phone" data-state={stateTone(state)}>{stateLabel(state)}</span>
                        </div>
                      </div>
                    </td>
                    <td className="dk-hide-phone"><Tag tone={u.role === 'admin' ? 'accent' : 'neutral'}>{t(`users.role.${u.role === 'admin' ? 'admin' : 'user'}`)}</Tag></td>
                    <td className="dk-hide-phone">
                      {u.role === 'admin' ? (
                        <span className="us-access">{accessLabel}</span>
                      ) : (
                        <button type="button" className="us-access us-access--button" onClick={() => setEditingUser(u)} aria-label={t('users.editAccessFor', { name: label(u) })} title={t('users.menu.editAccess')}>
                          {accessLabel}
                        </button>
                      )}
                    </td>
                    <td className="dk-hide-phone">
                      <span className="us-state" data-state={stateTone(state)}>{stateLabel(state)}</span>
                      <span className="dk-table-sub">{u.createdAt ? t('users.created', { date: new Date(u.createdAt).toLocaleDateString(lang) }) : ''}</span>
                    </td>
                    <td className="dk-hide-phone" data-testid="user-last-active">
                      {u.lastActiveAt
                        ? <span title={new Date(u.lastActiveAt).toLocaleString(lang)}>{relativeTime(u.lastActiveAt, lang)}</span>
                        : <span className="us-never">{t('users.never')}</span>}
                    </td>
                    <td className="dk-hide-phone dk-num" data-testid="user-key-count">{u.apiKeyCount > 0 ? u.apiKeyCount : <span className="us-never">{'\u2013'}</span>}</td>
                    <td className="dk-num">
                      {!self && (
                        <div className="us-acts">
                          {canView(u) && (
                            <Button size="sm" icon="eye" className="us-viewas" onClick={() => startViewAs(u)} title={tv('users.hint', { name: label(u) })} aria-label={tv('users.actionFor', { name: label(u) })} data-testid="user-viewas">{tv('users.action')}</Button>
                          )}
                          {state !== 'active' ? (
                            <Button size="sm" variant="primary" onClick={() => setStatus(u, 'active')} title={t('users.approve')}>
                              {state === 'disabled' ? t('users.enable') : t('users.approveShort')}
                            </Button>
                          ) : (
                            <Button size="sm" variant="ghost" icon="ban" onClick={() => setStatus(u, 'disabled', { undoable: true })} title={t('users.disable')} aria-label={t('users.disableFor', { name: label(u) })} />
                          )}
                          <ActionMenu items={menuItems(u)} ariaLabel={t('users.actionsFor', { name: label(u) })} />
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

      {users.length > 0 && (
        <p className="us-foot" data-testid="users-count">
          {t('users.showing', { shown: rows.length, total: users.length })}
          {registrationMode && <span data-testid="registration-mode"> {ta('registration.label', { mode: registrationMode })} {ta(`registration.${registrationMode}`, { defaultValue: '' })}</span>}
        </p>
      )}
      {users.length === 0 && registrationMode && (
        <p className="us-foot" data-testid="registration-mode">{ta('registration.label', { mode: registrationMode })} {ta(`registration.${registrationMode}`, { defaultValue: '' })}</p>
      )}

      {editingUser && featureMeta && (
        <AccessSheet
          user={editingUser} featureMeta={featureMeta} availableModels={availableModels} addToast={addToast}
          onClose={() => setEditingUser(null)} onSave={handlePermissionSave} actions={sheetActions(editingUser)} onFeatureDefault={setFeatureDefault}
        />
      )}

      {resetUser && (
        <Dialog
          title={t('users.reset.title')} labelId="us-reset-title" onClose={() => setResetUser(null)} pending={resetBusy}
          description={t('users.reset.description', { name: label(resetUser) })}
          foot={<>
            <Button onClick={() => setResetUser(null)} disabled={resetBusy}>{t('users.cancel')}</Button>
            <Button variant="primary" onClick={confirmReset} disabled={resetBusy || newPassword.length === 0}>
              {resetBusy ? t('users.reset.busy') : t('users.reset.confirm')}
            </Button>
          </>}
        >
          <div className="dk-field">
            <label className="dk-label" htmlFor="us-reset-pw">{t('users.reset.label')}</label>
            <input
              id="us-reset-pw" type="password" className="dk-input" placeholder={t('users.reset.placeholder')} value={newPassword} autoComplete="new-password"
              onChange={e => { setNewPassword(e.target.value); setWeakWarning(''); setAcknowledgeWeak(false) }}
              onKeyDown={e => { if (e.key === 'Enter' && newPassword.length > 0) confirmReset() }}
            />
          </div>
          {weakWarning && (
            <div role="alert" className="us-weak">
              <p className="us-weak__text">{weakWarning}</p>
              <label className="dk-choice">
                <input type="checkbox" className="dk-check" checked={acknowledgeWeak} onChange={e => setAcknowledgeWeak(e.target.checked)} />
                {t('users.reset.anyway')}
              </label>
            </div>
          )}
        </Dialog>
      )}

      {deleting && (
        <Dialog
          role="alertdialog" title={t('users.delete.title')} labelId="us-delete-title" onClose={() => setDeleting(null)} pending={deleteBusy}
          description={t('users.delete.description')}
          foot={<>
            <Button onClick={() => setDeleting(null)} disabled={deleteBusy}>{t('users.cancel')}</Button>
            <Button variant="danger" onClick={confirmDelete} disabled={deleteBusy || typed.trim() !== deleteName}>
              {deleteBusy ? t('users.delete.busy') : t('users.delete.confirm')}
            </Button>
          </>}
        >
          <div className="dk-field">
            <label className="dk-label" htmlFor="us-delete-name"><Trans t={t} i18nKey="users.delete.type" values={{ name: deleteName }} components={{ n: <span className="dk-mono" /> }} /></label>
            <input id="us-delete-name" className="dk-input" value={typed} autoComplete="off" onChange={e => setTyped(e.target.value)} />
          </div>
        </Dialog>
      )}

      {undo && (
        <HomeUndoToast
          key={undo.id} duration={8000} testId="user-undo-toast" message={undo.text}
          undoLabel={t('users.undo')} dismissLabel={t('users.dismiss')} onUndo={undoDisable} onExpire={() => setUndo(null)}
        />
      )}
    </div>
  )
}
