/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useState } from 'react'
import { Link, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import ActionMenu from '../ActionMenu'
import { Button, EmptyState, LoadError, PaneLayout, SidePane, SkeletonRows, StatusChip, useLoad } from '../ui'
import { useAuth } from '../../context/AuthContext'
import { adminUsersApi, sharedFoldersApi } from '../../utils/api'
import { refreshSharedPages } from '../../hooks/useSharedPages'
import './sharedFolders.css'

// The Shared tab of Files. An administrator manages the shared folders
// (/shared/<name>) and who may read or edit each one; any other user sees the
// folders shared with them and what they may do there. The pages of a shared
// folder open from Pages, under "Shared with you".

const EVERYONE = '@everyone'
const PAGES_LINK = '/app/pages#pg-shared'
const NAME_MAX = 64
// The server's rule: letters, digits, spaces, dots, dashes, underscores;
// a letter or a digit first; no trailing dot.
const NAME_RE = /^[\p{L}\p{N}][\p{L}\p{N} ._-]*$/u
const validName = (n) => {
  const s = n.trim()
  return s.length > 0 && [...s].length <= NAME_MAX && NAME_RE.test(s) && !s.endsWith('.') && !s.includes('  ')
}

const folderLabel = (p) => String(p || '').replace(/^\/+/, '')
const memberName = (m, t) => (m.principal === EVERYONE ? t('shared.everyone') : (m.name || m.principal))

function PagesLink({ folder, t }) {
  if (!folder.pages) return <span>{t('shared.pagesNone')}</span>
  return <Link to={PAGES_LINK} className="sf-link" data-testid="sf-pages">{t('shared.pages', { count: folder.pages })}</Link>
}

// Who may use a folder, in a few words: "Everyone can read", "3 members".
function membersSummary(folder, t) {
  const members = folder.members || []
  const all = members.find(m => m.principal === EVERYONE)
  const people = members.length - (all ? 1 : 0)
  const parts = []
  if (all) parts.push(t(`shared.everyoneCan.${all.mode}`))
  if (people > 0) parts.push(t('shared.people', { count: people }))
  return parts.length ? parts.join(' · ') : t('shared.membersNone')
}

function NameForm({ initial = '', submitLabel, onSubmit, onCancel, t, testId }) {
  const [name, setName] = useState(initial)
  const [busy, setBusy] = useState(false)
  const ok = validName(name)
  const submit = async (e) => {
    e.preventDefault()
    if (!ok || busy) return
    setBusy(true)
    const done = await onSubmit(name.trim())
    setBusy(false)
    if (!done) return
    setName('')
  }
  return (
    <form className="sf-nameform" onSubmit={submit} data-testid={testId}>
      <label className="sf-sr" htmlFor={`${testId}-input`}>{t('shared.nameLabel')}</label>
      <input
        id={`${testId}-input`} className="input sf-nameform__input" value={name} autoFocus maxLength={NAME_MAX * 4}
        placeholder={t('shared.nameLabel')} aria-invalid={name !== '' && !ok} aria-describedby={`${testId}-hint`}
        onChange={(e) => setName(e.target.value)} onKeyDown={(e) => { if (e.key === 'Escape') onCancel() }}
      />
      <Button type="submit" variant="primary" size="sm" busy={busy} disabled={!ok}>{submitLabel}</Button>
      <Button variant="ghost" size="sm" onClick={onCancel}>{t('shared.cancel')}</Button>
      <span className="sf-hint" id={`${testId}-hint`}>{t('shared.nameHint')}</span>
    </form>
  )
}

function FolderRow({ folder, admin, onMembers, onRename, onRemove, t }) {
  const [renaming, setRenaming] = useState(false)
  return (
    <li className="sf-row" data-testid="sf-row" data-folder={folder.name}>
      <span className="icon-chip tone-primary sf-row__chip" aria-hidden="true"><Icon name="folder" /></span>
      <div className="sf-row__main">
        {renaming ? (
          <NameForm
            initial={folder.name} submitLabel={t('shared.renameSave')} t={t} testId={`sf-rename-${folder.id}`}
            onCancel={() => setRenaming(false)}
            onSubmit={async (name) => { const ok = name === folder.name || await onRename(folder, name); if (ok) setRenaming(false); return ok }}
          />
        ) : admin ? (
          <button type="button" className="sf-row__title" onClick={() => onMembers(folder)}>{folder.name}</button>
        ) : (
          <span className="sf-row__title sf-row__title--static">{folder.name}</span>
        )}
        <span className="sf-row__meta">
          <code className="sf-row__path">{folderLabel(folder.path)}</code>
          <span aria-hidden="true">·</span>
          <PagesLink folder={folder} t={t} />
          {admin && (
            <>
              <span aria-hidden="true">·</span>
              <span data-testid="sf-summary">{membersSummary(folder, t)}</span>
            </>
          )}
        </span>
      </div>
      <div className="sf-row__actions">
        {admin ? (
          <>
            <Button size="sm" icon="users" onClick={() => onMembers(folder)} data-testid="sf-members">{t('shared.manage')}</Button>
            <ActionMenu
              ariaLabel={t('shared.menu', { name: folder.name })}
              items={[
                { key: 'rename', icon: 'pencil', label: t('shared.rename'), onClick: () => setRenaming(true) },
                { divider: true },
                { key: 'remove', icon: 'trash', label: t('shared.remove'), danger: true, onClick: () => onRemove(folder) },
              ]}
            />
          </>
        ) : (
          <StatusChip tone={folder.mode === 'write' ? 'ok' : 'idle'} testId="sf-mode">{t(`shared.mode.${folder.mode}`)}</StatusChip>
        )}
      </div>
    </li>
  )
}

// The members of one folder: add a person or Everyone, change what they may
// do, take access away.
function MembersPane({ folder, users, usersFailed, onSet, onRemove, onClose, t }) {
  const members = useMemo(() => folder?.members || [], [folder])
  const taken = useMemo(() => new Set(members.map(m => m.principal)), [members])
  const choices = useMemo(() => {
    const out = []
    if (!taken.has(EVERYONE)) out.push({ id: EVERYONE, label: t('shared.everyoneOption') })
    for (const u of users) if (!taken.has(u.id)) out.push({ id: u.id, label: u.email && u.name ? `${u.name} (${u.email})` : (u.name || u.email || u.id) })
    return out
  }, [users, taken, t])
  const [who, setWho] = useState('')
  const [mode, setMode] = useState('read')
  const [busy, setBusy] = useState(false)
  useEffect(() => { setWho(choices[0]?.id || '') }, [choices])

  const add = async (e) => {
    e.preventDefault()
    if (!who || busy) return
    setBusy(true)
    await onSet(folder, who, mode, true)
    setBusy(false)
  }

  return (
    <SidePane
      open={!!folder} onClose={onClose} testId="sf-pane"
      title={folder?.name || ''}
      meta={folder && <span className="sf-pane__meta"><code>{folderLabel(folder.path)}</code> · <PagesLink folder={folder} t={t} /></span>}
    >
      {folder && (
        <div className="sf-pane">
          <form className="sf-add" onSubmit={add} data-testid="sf-add">
            <h3 className="sf-pane__heading">{t('shared.addTitle')}</h3>
            {choices.length > 0 ? (
              <div className="sf-add__fields">
                <label className="sf-field">
                  <span className="sf-field__label">{t('shared.person')}</span>
                  <select className="input" value={who} onChange={(e) => setWho(e.target.value)} data-testid="sf-add-who">
                    {choices.map(c => <option key={c.id} value={c.id}>{c.label}</option>)}
                  </select>
                </label>
                <label className="sf-field sf-field--mode">
                  <span className="sf-field__label">{t('shared.access')}</span>
                  <select className="input" value={mode} onChange={(e) => setMode(e.target.value)} data-testid="sf-add-mode">
                    <option value="read">{t('shared.mode.read')}</option>
                    <option value="write">{t('shared.mode.write')}</option>
                  </select>
                </label>
                <Button type="submit" variant="primary" size="sm" icon="plus" busy={busy} data-testid="sf-add-submit">{t('shared.add')}</Button>
              </div>
            ) : (
              <p className="sf-hint">{t('shared.allAdded')}</p>
            )}
            {usersFailed && <p className="sf-hint" role="note">{t('shared.usersUnavailable')}</p>}
          </form>

          <h3 className="sf-pane__heading">{t('shared.membersTitle')} <span className="sf-count">{members.length}</span></h3>
          {members.length === 0 ? (
            <p className="sf-hint" data-testid="sf-no-members">{t('shared.noMembers')}</p>
          ) : (
            <ul className="sf-members" aria-label={t('shared.membersTitle')}>
              {members.map(m => (
                <li key={m.principal} className="sf-member" data-testid="sf-member" data-principal={m.principal}>
                  <span className={`icon-chip sf-member__chip${m.everyone ? ' tone-primary' : ''}`} aria-hidden="true"><Icon name={m.everyone ? 'users' : 'user'} /></span>
                  <span className="sf-member__name">
                    {memberName(m, t)}
                    {m.everyone && <span className="sf-member__note">{t('shared.everyoneHint')}</span>}
                  </span>
                  <label className="sf-sr" htmlFor={`sf-mode-${m.principal}`}>{t('shared.accessFor', { name: memberName(m, t) })}</label>
                  <select
                    id={`sf-mode-${m.principal}`} className="input sf-member__mode" value={m.mode} data-testid="sf-member-mode"
                    onChange={(e) => onSet(folder, m.principal, e.target.value, false)}
                  >
                    <option value="read">{t('shared.mode.read')}</option>
                    <option value="write">{t('shared.mode.write')}</option>
                  </select>
                  <Button
                    variant="danger-ghost" size="sm" icon="close" aria-label={t('shared.removeMember', { name: memberName(m, t) })}
                    title={t('shared.removeMember', { name: memberName(m, t) })} onClick={() => onRemove(folder, m)} data-testid="sf-member-remove"
                  />
                </li>
              ))}
            </ul>
          )}
          <p className="sf-hint">{t('shared.paneNote')}</p>
        </div>
      )}
    </SidePane>
  )
}

function AdminFolders({ t }) {
  const { addToast } = useOutletContext() || {}
  const folders = useLoad(() => sharedFoldersApi.list(), [])
  const [creating, setCreating] = useState(false)
  const [openId, setOpenId] = useState(null)
  const [users, setUsers] = useState([])
  const [usersFailed, setUsersFailed] = useState(false)
  const { authEnabled } = useAuth()
  const [list, setList] = useState([])
  useEffect(() => { if (folders.status === 'ready') setList(folders.data?.folders || []) }, [folders.status, folders.data])

  useEffect(() => {
    if (!authEnabled) return undefined
    let cancelled = false
    adminUsersApi.list()
      .then(data => { if (!cancelled) setUsers(Array.isArray(data) ? data : (data?.users || [])) })
      .catch(() => { if (!cancelled) setUsersFailed(true) })
    return () => { cancelled = true }
  }, [authEnabled])

  const toast = (msg, type = 'success', options) => addToast?.(msg, type, undefined, options)
  const fail = (e) => toast(t('shared.failed', { message: e?.message || String(e) }), 'error')
  const put = (f) => setList(prev => {
    const i = prev.findIndex(x => x.id === f.id)
    const next = i < 0 ? [...prev, f] : prev.map(x => (x.id === f.id ? f : x))
    return next.sort((a, b) => a.name.localeCompare(b.name))
  })
  const changed = () => { refreshSharedPages() }

  const create = async (name) => {
    try {
      const f = await sharedFoldersApi.create(name)
      put(f)
      setCreating(false)
      setOpenId(f.id)
      toast(t('shared.created', { name: f.name }))
      return true
    } catch (e) { fail(e); return false }
  }
  const rename = async (folder, name) => {
    try {
      put(await sharedFoldersApi.rename(folder.id, name))
      changed()
      return true
    } catch (e) { fail(e); return false }
  }
  const restore = async (rev, name) => {
    try {
      put(await sharedFoldersApi.restore(rev))
      changed()
      toast(t('shared.restored', { name }))
    } catch (e) { fail(e) }
  }
  const remove = async (folder) => {
    try {
      const res = await sharedFoldersApi.remove(folder.id)
      setList(prev => prev.filter(x => x.id !== folder.id))
      if (openId === folder.id) setOpenId(null)
      changed()
      toast(t('shared.removed', { name: folder.name }), 'success', {
        action: { label: t('common:ui.undo'), onClick: () => restore(res.undo_rev, folder.name) },
        testId: 'sf-removed-toast',
      })
    } catch (e) { fail(e) }
  }
  const setMember = async (folder, principal, mode, added) => {
    try {
      const f = await sharedFoldersApi.setMember(folder.id, principal, mode)
      put(f)
      changed()
      const m = f.members.find(x => x.principal === principal)
      if (added) toast(t('shared.memberAdded', { name: m ? memberName(m, t) : principal, mode: t(`shared.mode.${mode}`) }))
    } catch (e) { fail(e) }
  }
  const removeMember = async (folder, m) => {
    try {
      put(await sharedFoldersApi.removeMember(folder.id, m.principal))
      changed()
      toast(t('shared.memberRemoved', { name: memberName(m, t), folder: folder.name }), 'success', {
        action: { label: t('common:ui.undo'), onClick: () => setMember(folder, m.principal, m.mode, false) },
      })
    } catch (e) { fail(e) }
  }

  const open = list.find(f => f.id === openId) || null
  let body
  if (folders.status === 'loading') body = <SkeletonRows count={3} />
  else if (folders.status === 'error') body = <LoadError title={t('shared.loadFailed')} error={folders.error} onRetry={folders.retry} />
  else if (list.length === 0 && !creating) {
    body = (
      <EmptyState
        icon="folder" title={t('shared.emptyAdminTitle')} body={t('shared.emptyAdminBody')} testId="sf-empty"
        facts={[{ text: t('shared.factMembers') }, { text: t('shared.factChats') }, { text: t('shared.factUndo') }]}
        actions={<Button variant="primary" icon="plus" onClick={() => setCreating(true)} data-testid="sf-new">{t('shared.new')}</Button>}
      />
    )
  } else {
    body = (
      <ul className="sf-list" aria-label={t('shared.adminTitle')}>
        {list.map(f => (
          <FolderRow key={f.id} folder={f} admin t={t} onMembers={(x) => setOpenId(x.id)} onRename={rename} onRemove={remove} />
        ))}
      </ul>
    )
  }

  return (
    <section className="sf" aria-labelledby="sf-title" data-testid="sf-admin">
      <header className="sf-head">
        <div className="sf-head__text">
          <h2 className="sf-title" id="sf-title">{t('shared.adminTitle')}{list.length > 0 && <span className="sf-count">{list.length}</span>}</h2>
          <p className="sf-lede">{t('shared.adminLede')}</p>
        </div>
        {list.length > 0 && !creating && (
          <Button size="sm" icon="plus" onClick={() => setCreating(true)} data-testid="sf-new">{t('shared.new')}</Button>
        )}
      </header>
      <PaneLayout className="sf-layout">
        <div className="sf-body">
          {creating && <NameForm submitLabel={t('shared.create')} onSubmit={create} onCancel={() => setCreating(false)} t={t} testId="sf-create" />}
          {body}
        </div>
        <MembersPane
          folder={open} users={users} usersFailed={usersFailed && authEnabled} t={t}
          onSet={setMember} onRemove={removeMember} onClose={() => setOpenId(null)}
        />
      </PaneLayout>
    </section>
  )
}

function MyFolders({ t }) {
  const folders = useLoad(() => sharedFoldersApi.mine(), [])
  const list = folders.data?.folders || []
  let body
  if (folders.status === 'loading') body = <SkeletonRows count={2} />
  else if (folders.status === 'error') body = <LoadError title={t('shared.loadFailed')} error={folders.error} onRetry={folders.retry} />
  else if (list.length === 0) body = <EmptyState icon="folder" title={t('shared.emptyUserTitle')} body={t('shared.emptyUserBody')} testId="sf-empty" />
  else {
    body = (
      <ul className="sf-list" aria-label={t('shared.userTitle')}>
        {list.map(f => <FolderRow key={f.id} folder={f} t={t} />)}
      </ul>
    )
  }
  return (
    <section className="sf" aria-labelledby="sf-title" data-testid="sf-mine">
      <header className="sf-head">
        <div className="sf-head__text">
          <h2 className="sf-title" id="sf-title">{t('shared.userTitle')}{list.length > 0 && <span className="sf-count">{list.length}</span>}</h2>
          <p className="sf-lede">{t('shared.userLede')}</p>
        </div>
      </header>
      {body}
    </section>
  )
}

export default function SharedFolders() {
  const { t } = useTranslation('workspace')
  const { isAdmin } = useAuth()
  return isAdmin ? <AdminFolders t={t} /> : <MyFolders t={t} />
}
