import { useState } from 'react'
import { Link, useNavigate, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../components/Icon'
import PageHeader from '../components/PageHeader'
import { pagesApi } from '../utils/api'
import { pageIndexPath } from '../utils/pages'
import { refreshPages, usePageAge, usePages } from '../hooks/usePages'
import { usePinnedPages } from '../hooks/usePinnedPages'
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

// /app/pages: the user's pages, newest change first, or the empty state that
// starts one.
export default function Pages() {
  const { t } = useTranslation('pages')
  const navigate = useNavigate()
  const { addToast } = useOutletContext() || {}
  const { pages, loaded, error, off } = usePages(true)
  const { pins, setPin } = usePinnedPages(true)
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
  } else if (pages.length === 0) {
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
      <ul className="pg-list" aria-label={t('card.list')}>
        {sorted.map((p) => <PageRow key={p.name} page={p} pinned={pins.includes(p.name)} onPin={pin} age={age} t={t} />)}
      </ul>
    )
  }

  return (
    <div className="page page--medium pg-page" data-testid="pages-page">
      <PageHeader title={t('title')} supporting={t('subtitle')} />
      {body}
    </div>
  )
}
