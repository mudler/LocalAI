import { useState } from 'react'
import { Link, useNavigate, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../components/Icon'
import PageHeader from '../components/PageHeader'
import { pagesApi } from '../utils/api'
import { pageIndexPath } from '../utils/pages'
import { refreshPages, usePageAge, usePages } from '../hooks/usePages'
import { usePinnedPages } from '../hooks/usePinnedPages'
import { folderLabel, sharedPagePath, useSharedPages } from '../hooks/useSharedPages'
import './pages.css'

const STARTER = 'task-board'

function PageRow({ page, pinned, onPin, age, t }) {
  const navigate = useNavigate()
  const author = page.author === 'assistant' ? 'assistant' : 'you'
  const ask = () => navigate('/app/chats', { state: { draft: t('view.askPrompt', { name: page.name }) } })
  return (
    <li className="pg-row" data-testid="page-card" data-page={page.name}>
      <span className="icon-chip tone-primary pg-row__chip"><Icon name={page.icon || 'file-code'} /></span>
      <div className="pg-row__main">
        <Link className="pg-row__title" to={`/app/pages/${encodeURIComponent(page.name)}`}>{page.title}</Link>
        <span className="pg-row__meta">
          <code className="pg-row__path">{pageIndexPath(page.name)}</code>
          <span aria-hidden="true">·</span>
          <span>{t('card.changed', { when: age(page.updated_at) })}</span>
          <span aria-hidden="true">·</span>
          <span data-testid="page-author">{t(`card.author.${author}`)}</span>
        </span>
        {page.description && <p className="pg-row__desc">{page.description}</p>}
      </div>
      <div className="pg-row__actions">
        <Link className="btn btn-primary btn-sm" to={`/app/pages/${encodeURIComponent(page.name)}`}>{t('card.open')}</Link>
        <button
          type="button" className="btn btn-secondary btn-sm" aria-pressed={pinned}
          onClick={() => onPin(page.name, !pinned)} data-testid="page-pin"
        >
          <Icon name="pin" /> {pinned ? t('card.unpin') : t('card.pin')}
        </button>
        <button type="button" className="btn btn-ghost btn-sm" onClick={ask} data-testid="page-ask">
          <Icon name="sparkles" /> {t('card.ask')}
        </button>
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
        <span className="pg-row__meta">
          <span>{t('shared.from', { author })}</span>
          <span aria-hidden="true">·</span>
          <code className="pg-row__path">{folderLabel(page.folder)}/pages/{page.name}</code>
          <span aria-hidden="true">·</span>
          <span>{t('card.changed', { when: age(page.updated_at) })}</span>
        </span>
        {page.description && <p className="pg-row__desc">{page.description}</p>}
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
  const [starting, setStarting] = useState(false)

  const pin = async (name, on) => {
    try {
      await setPin(name, on)
    } catch (e) {
      addToast?.(t('card.pinFailed', { message: e.message }), 'error')
    }
  }

  const start = async () => {
    setStarting(true)
    try {
      const p = await pagesApi.copyStarter(STARTER)
      await refreshPages()
      navigate(`/app/pages/${encodeURIComponent(p.name)}`)
    } catch (e) {
      addToast?.(t('empty.starterFailed', { message: e.message }), 'error')
    } finally {
      setStarting(false)
    }
  }

  const askNew = () => navigate('/app/chats', { state: { draft: t('empty.askPrompt') } })
  const sorted = [...pages].sort((a, b) => String(b.updated_at).localeCompare(String(a.updated_at)))

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
        <h2 className="pg-empty__title">{t('empty.title')}</h2>
        <p className="pg-empty__text">{t('empty.text')}</p>
        <div className="pg-empty__actions">
          <button type="button" className="btn btn-primary" onClick={start} disabled={starting} data-testid="pages-starter">
            <Icon name={starting ? 'spinner' : 'checklist'} spin={starting} /> {starting ? t('empty.starting') : t('empty.starter')}
          </button>
          <button type="button" className="btn btn-secondary" onClick={askNew}>
            <Icon name="sparkles" /> {t('empty.ask')}
          </button>
        </div>
      </div>
    )
  } else {
    body = (
      <>
        {pages.length > 0 && (
          <section className="pg-group" aria-labelledby="pg-yours">
            {shared.pages.length > 0 && <h2 className="pg-group__title" id="pg-yours">{t('shared.yours')}</h2>}
            <ul className="pg-list" aria-label={t('card.list')}>
              {sorted.map((p) => <PageRow key={p.name} page={p} pinned={pins.includes(p.name)} onPin={pin} age={age} t={t} />)}
            </ul>
          </section>
        )}
        {shared.pages.length > 0 && (
          <section className="pg-group" aria-labelledby="pg-shared" data-testid="shared-group">
            <h2 className="pg-group__title" id="pg-shared">{t('shared.group')}</h2>
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
      <PageHeader title={t('title')} supporting={t('subtitle')} />
      {body}
    </div>
  )
}
