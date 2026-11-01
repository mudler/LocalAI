/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useMemo, useState } from 'react'
import { Link, useNavigate, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../components/Icon'
import PageHeader from '../components/PageHeader'
import ActionMenu from '../components/ActionMenu'
import ConfirmDialog from '../components/ConfirmDialog'
import { filesApi, pagesApi } from '../utils/api'
import { pageIndexPath } from '../utils/pages'
import { DOCS_MAKE_YOURS } from '../utils/discovery'
import { refreshPages, usePageAge, usePages } from '../hooks/usePages'
import { usePinnedPages } from '../hooks/usePinnedPages'
import { folderLabel, sharedPagePath, useSharedPages } from '../hooks/useSharedPages'
import './pages.css'

// The ready pages that ship with LocalAI.
const STARTERS = [
  { id: 'task-board', icon: 'checklist', key: 'empty.starter' },
  { id: 'reading-list', icon: 'book', key: 'empty.starterReading' },
]
// Above this many pages, a filter field shows over the list.
const FILTER_FROM = 8
const NAME_RE = /^[a-z0-9][a-z0-9-]{0,63}$/

function PageRow({ page, pinned, onPin, onDelete, onRename, age, t }) {
  const navigate = useNavigate()
  const [renaming, setRenaming] = useState(false)
  const [name, setName] = useState(page.name)
  const author = page.author === 'assistant' ? 'assistant' : 'you'
  const open = `/app/pages/${encodeURIComponent(page.name)}`
  const ask = () => navigate('/app/chats', { state: { draft: t('view.askPrompt', { name: page.name }) } })
  const saveName = async (e) => {
    e.preventDefault()
    if (name === page.name) { setRenaming(false); return }
    if (await onRename(page.name, name)) setRenaming(false)
  }
  return (
    <li className="pg-row" data-testid="page-card" data-page={page.name}>
      <span className="icon-chip tone-primary pg-row__chip"><Icon name={page.icon || 'file-code'} /></span>
      <div className="pg-row__main">
        {renaming ? (
          <form className="pg-rename" onSubmit={saveName}>
            <label className="pg-sr" htmlFor={`pg-rename-${page.name}`}>{t('card.renameLabel')}</label>
            <input
              id={`pg-rename-${page.name}`} className="dk-input pg-rename__input" value={name} autoFocus spellCheck={false}
              onChange={(e) => setName(e.target.value.toLowerCase())} data-testid="page-rename-input"
              aria-invalid={!NAME_RE.test(name)} aria-describedby={`pg-rename-${page.name}-hint`}
            />
            <button type="submit" className="btn btn-primary btn-sm" disabled={!NAME_RE.test(name)}>{t('card.renameSave')}</button>
            <button type="button" className="btn btn-ghost btn-sm" onClick={() => { setName(page.name); setRenaming(false) }}>{t('card.renameCancel')}</button>
            <span className="pg-row__meta" id={`pg-rename-${page.name}-hint`}>{t('card.renameHint')}</span>
          </form>
        ) : (
          <Link className="pg-row__title" to={open}>{page.title}</Link>
        )}
        {page.description && <p className="pg-row__desc">{page.description}</p>}
        <span className="pg-row__meta">
          <code className="pg-row__path">{pageIndexPath(page.name).replace(/\/index\.html$/, '')}</code>
          <span data-testid="page-author">{t(`card.changedBy.${author}`, { when: age(page.updated_at) })}</span>
        </span>
      </div>
      <div className="pg-row__actions">
        <Link className="btn btn-secondary btn-sm" to={open}>{t('card.open')}</Link>
        <button
          type="button" className={`btn btn-ghost btn-sm pg-pin${pinned ? ' pg-pin--on' : ''}`} aria-pressed={pinned}
          aria-label={pinned ? t('card.unpin') : t('card.pin')} title={pinned ? t('card.unpin') : t('card.pin')}
          onClick={() => onPin(page.name, !pinned)} data-testid="page-pin"
        >
          <Icon name="pin" />
        </button>
        <ActionMenu
          ariaLabel={t('card.menu', { title: page.title })}
          items={[
            { key: 'ask', icon: 'sparkles', label: t('card.ask'), onClick: ask },
            { key: 'files', icon: 'folder', label: t('card.files'), onClick: () => navigate(`/app/files?path=${encodeURIComponent(`/pages/${page.name}`)}`) },
            { key: 'rename', icon: 'pencil', label: t('card.rename'), onClick: () => setRenaming(true) },
            { divider: true },
            { key: 'delete', icon: 'trash', label: t('card.delete'), danger: true, onClick: () => onDelete(page) },
          ]}
        />
      </div>
    </li>
  )
}

const BADGE = {
  none: { key: 'notAllowed', tone: 'warn', icon: 'warning' },
  changed: { key: 'changed', tone: 'warn', icon: 'warning' },
  allowed: { key: 'allowed', tone: 'ok', icon: 'check-circle' },
}

// A page of a shared folder: its author and folder, and whether the user
// allowed it. A page that is not allowed (or changed since) only offers
// Review; nothing in it runs from this screen.
function SharedRow({ page, enabled, age, t }) {
  const b = BADGE[page.consent] || BADGE.none
  const allowed = page.consent === 'allowed'
  const author = page.author_name || page.author_id
  return (
    <li className={`pg-row${enabled ? '' : ' pg-row--off'}`} data-testid="shared-card" data-page={page.name} data-consent={page.consent}>
      <span className="icon-chip tone-primary pg-row__chip"><Icon name={page.icon || 'users'} /></span>
      <div className="pg-row__main">
        <span className="pg-row__titleline">
          {enabled
            ? <Link className="pg-row__title" to={sharedPagePath(page)}>{page.title}</Link>
            : <span className="pg-row__title">{page.title}</span>}
          <span className={`pg-badge pg-badge--${b.tone}`} data-testid="shared-badge"><Icon name={b.icon} /> {t(`shared.badge.${b.key}`)}</span>
        </span>
        {page.description && <p className="pg-row__desc">{page.description}</p>}
        <span className="pg-row__meta">
          <span>{t('shared.from', { author })}</span>
          <span aria-hidden="true">·</span>
          <code className="pg-row__path">{folderLabel(page.folder)}/pages/{page.name}</code>
          <span aria-hidden="true">·</span>
          <span>{t('card.changed', { when: age(page.updated_at) })}</span>
        </span>
      </div>
      {enabled && (
        <div className="pg-row__actions">
          <Link className={`btn btn-sm ${allowed ? 'btn-primary' : 'btn-secondary'}`} to={sharedPagePath(page)} data-testid="shared-open">
            {allowed ? t('card.open') : t('shared.review')}
          </Link>
        </div>
      )}
    </li>
  )
}

// /app/pages: the user's pages, newest change first, or the empty state that
// starts one; then the pages of the user's shared folders.
export default function Pages() {
  const { t } = useTranslation('pages')
  const navigate = useNavigate()
  const { addToast } = useOutletContext() || {}
  const { pages, loaded, error, off } = usePages(true)
  const { pins, setPin } = usePinnedPages(true)
  const shared = useSharedPages(true)
  const age = usePageAge()
  const [starting, setStarting] = useState('')
  const [filter, setFilter] = useState('')
  const [deleting, setDeleting] = useState(null)

  const pin = async (name, on) => {
    try {
      await setPin(name, on)
    } catch (e) {
      addToast?.(t('card.pinFailed', { message: e.message }), 'error')
    }
  }

  const start = async (id) => {
    setStarting(id)
    try {
      const p = await pagesApi.copyStarter(id)
      await refreshPages()
      navigate(`/app/pages/${encodeURIComponent(p.name)}`)
    } catch (e) {
      addToast?.(t('empty.starterFailed', { message: e.message }), 'error')
    } finally {
      setStarting('')
    }
  }

  // A page is a folder: renaming moves it, deleting moves it to the Trash
  // (Files, Trash restores it).
  const rename = async (from, to) => {
    try {
      await filesApi.move(`/pages/${from}`, `/pages/${to}`)
      if (pins.includes(from)) { await setPin(to, true); await setPin(from, false) }
      await refreshPages()
      return true
    } catch (e) {
      addToast?.(t('card.renameFailed', { message: e.message }), 'error')
      return false
    }
  }
  const remove = async () => {
    const p = deleting
    setDeleting(null)
    try {
      await filesApi.remove(`/pages/${p.name}`)
      if (pins.includes(p.name)) await setPin(p.name, false).catch(() => {})
      await refreshPages()
      addToast?.(t('card.deleted', { title: p.title }), 'success')
    } catch (e) {
      addToast?.(t('card.deleteFailed', { message: e.message }), 'error')
    }
  }

  const askNew = () => navigate('/app/chats', { state: { draft: t('empty.askPrompt') } })
  const sorted = useMemo(() => {
    const q = filter.trim().toLowerCase()
    return [...pages]
      .filter(p => !q || `${p.title} ${p.name} ${p.description || ''}`.toLowerCase().includes(q))
      .sort((a, b) => String(b.updated_at).localeCompare(String(a.updated_at)))
  }, [pages, filter])

  let body
  if (!loaded) {
    body = <div className="loading-center" role="status"><Icon name="spinner" spin /> <span className="pg-sr">{t('loading')}</span></div>
  } else if (off) {
    body = <div className="pg-note" role="note" data-testid="pages-off">{t('off', { reason: off })}</div>
  } else if (error) {
    body = (
      <div className="pg-note pg-note--error" role="alert">
        <span>{t('error', { message: error })}</span>
        <button type="button" className="btn btn-secondary btn-sm" onClick={refreshPages}>{t('retry')}</button>
      </div>
    )
  } else if (pages.length === 0 && shared.pages.length === 0) {
    body = (
      <div className="pg-empty" data-testid="pages-empty">
        <span className="icon-chip pg-empty__chip" aria-hidden="true"><Icon name="layers" /></span>
        <h2 className="pg-empty__title">{t('empty.title')}</h2>
        <p className="pg-empty__text">{t('empty.text')}</p>
        <button type="button" className="btn btn-primary" onClick={askNew} data-testid="pages-ask">
          <Icon name="sparkles" /> {t('empty.ask')}
        </button>
        <div className="pg-empty__actions">
          {STARTERS.map(s => (
            <button
              key={s.id} type="button" className="btn btn-secondary btn-sm" onClick={() => start(s.id)} disabled={!!starting}
              data-testid={s.id === 'task-board' ? 'pages-starter' : 'pages-starter-reading'}
            >
              <Icon name={starting === s.id ? 'spinner' : s.icon} spin={starting === s.id} /> {starting === s.id ? t('empty.starting') : t(s.key)}
            </button>
          ))}
        </div>
        <p className="pg-empty__note">{t('empty.note')} <a href={DOCS_MAKE_YOURS} target="_blank" rel="noopener noreferrer">{t('empty.learn')}</a></p>
      </div>
    )
  } else {
    body = (
      <>
        {pages.length > 0 && (
          <section className="pg-section" aria-labelledby="pg-yours">
            <h2 className="pg-group" id="pg-yours">{t('yours')} <span className="pg-group__count">{pages.length}</span></h2>
            {pages.length > FILTER_FROM && (
              <div className="pg-filter">
                <label className="pg-sr" htmlFor="pg-filter">{t('filter')}</label>
                <input id="pg-filter" className="dk-input" type="search" placeholder={t('filter')} value={filter} onChange={(e) => setFilter(e.target.value)} data-testid="pages-filter" />
              </div>
            )}
            <ul className="pg-list" aria-label={t('card.list')}>
              {sorted.map((p) => <PageRow key={p.name} page={p} pinned={pins.includes(p.name)} onPin={pin} onDelete={setDeleting} onRename={rename} age={age} t={t} />)}
            </ul>
          </section>
        )}
        {shared.pages.length > 0 && (
          <section className="pg-section" aria-labelledby="pg-shared" data-testid="shared-group">
            <h2 className="pg-group" id="pg-shared">{t('shared.group')} <span className="pg-group__count">{shared.pages.length}</span></h2>
            {!shared.enabled && <p className="pg-note" role="note" data-testid="shared-off">{t('shared.offNotice')}</p>}
            <ul className="pg-list" aria-label={t('shared.group')}>
              {shared.pages.map((p) => <SharedRow key={`${p.folder_id}/${p.name}`} page={p} enabled={shared.enabled} age={age} t={t} />)}
            </ul>
            {shared.enabled && <p className="pg-group__note">{t('shared.note')}</p>}
          </section>
        )}
      </>
    )
  }

  return (
    <div className="page page--medium pg-page" data-testid="pages-page">
      <PageHeader
        title={t('title')} supporting={t('subtitle')}
        actions={pages.length > 0 || shared.pages.length > 0 ? (
          <button type="button" className="btn btn-secondary" onClick={askNew} data-testid="pages-ask-new"><Icon name="sparkles" /> {t('askNew')}</button>
        ) : undefined}
      />
      {body}
      <ConfirmDialog
        open={!!deleting}
        title={t('card.deleteTitle', { title: deleting?.title || '' })}
        message={t('card.deleteMessage', { folder: deleting ? `pages/${deleting.name}/` : '' })}
        confirmLabel={t('card.delete')}
        danger
        onConfirm={remove}
        onCancel={() => setDeleting(null)}
      />
    </div>
  )
}
