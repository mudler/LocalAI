/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import LoadingSpinner from '../LoadingSpinner'
import CatalogSearchBox from './CatalogSearchBox'
import CatalogPager from './CatalogPager'
import CatalogEmpty from './CatalogEmpty'
import { PAGE_SIZE, normalizeQuery, queryKey, totalPages } from './catalogState'
import './catalog.css'

// A searchable, paged listing over a resource adapter. It knows nothing about
// what an entry is: the adapter searches, the caller draws each entry.
//
//   adapter           { search(query, signal) => { entries, total } } and, when
//                     libraries filter the list, listSources(signal)
//   query             { text, sourceId, page }, owned by the caller (the URL)
//   onQueryChange     (nextQuery) => void
//   renderEntry       (entry) => node, one list item body
//   reloadToken       change it to search again with the same query
//   describeError     (error) => string for a failed search
//
// Every search is cancelled when the query changes or the view unmounts.
export default function CatalogView({ adapter, query, onQueryChange, renderEntry, reloadToken = 0, describeError, emptyTitle, emptyText, searchLabel, pageSize = adapter.pageSize || PAGE_SIZE }) {
  const { t } = useTranslation('addons')
  const normalized = useMemo(() => normalizeQuery(query), [query])
  const key = queryKey(normalized)
  const [draft, setDraft] = useState(normalized.text)
  const [state, setState] = useState({ status: 'loading', entries: [], total: 0, error: null })
  const [sources, setSources] = useState([])
  const [attempt, setAttempt] = useState(0)
  const queryRef = useRef(normalized)
  queryRef.current = normalized

  // The field follows the URL (back, forward, a clear) and is debounced the
  // other way, so typing does not search on every key.
  useEffect(() => { setDraft(d => (d.trim() === normalized.text ? d : normalized.text)) }, [normalized.text])
  useEffect(() => {
    if (draft.trim() === queryRef.current.text) return undefined
    const timer = setTimeout(() => onQueryChange({ ...queryRef.current, text: draft.trim(), page: 1 }), 250)
    return () => clearTimeout(timer)
  }, [draft, onQueryChange])

  useEffect(() => {
    if (!adapter.listSources) return undefined
    const ctl = new AbortController()
    adapter.listSources(ctl.signal)
      .then(list => setSources(list.filter(s => s.enabled)))
      .catch(() => { if (!ctl.signal.aborted) setSources([]) })
    return () => ctl.abort()
  }, [adapter, reloadToken])

  useEffect(() => {
    const ctl = new AbortController()
    setState(s => ({ ...s, status: 'loading', error: null }))
    adapter.search(queryRef.current, ctl.signal)
      .then(page => setState({ status: 'ready', entries: page.entries, total: page.total, error: null }))
      .catch(error => {
        if (ctl.signal.aborted || (error && error.name === 'AbortError')) return
        setState({ status: 'error', entries: [], total: 0, error })
      })
    return () => ctl.abort()
  }, [adapter, key, reloadToken, attempt])

  const pages = totalPages(state.total, pageSize)
  const filtered = Boolean(normalized.text || normalized.sourceId)
  const label = searchLabel || t('catalog.search')

  return (
    <div className="cat-view">
      <div className="cat-bar">
        <CatalogSearchBox className="cat-search" value={draft} onChange={setDraft} label={label} />
        {sources.length > 1 && (
          <div className="cat-chips" role="group" aria-label={t('catalog.libraries')}>
            <button type="button" className="dk-chip" aria-pressed={!normalized.sourceId} onClick={() => onQueryChange({ ...normalized, sourceId: '', page: 1 })}>
              {t('catalog.allLibraries')}
            </button>
            {sources.map(s => (
              <button key={s.id} type="button" className="dk-chip" aria-pressed={normalized.sourceId === s.id} onClick={() => onQueryChange({ ...normalized, sourceId: s.id, page: 1 })}>
                {s.name}
              </button>
            ))}
          </div>
        )}
      </div>

      <p className="cat-count" role="status" aria-live="polite">
        {state.status === 'ready' ? t('catalog.count', { count: state.total }) : ''}
      </p>

      {state.status === 'error' ? (
        <div className="op-error" role="alert">
          <Icon name="alert-circle" />
          <span>{describeError ? describeError(state.error) : state.error?.message}</span>
          <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => setAttempt(a => a + 1)}>
            <Icon name="refresh" /> {t('catalog.retry')}
          </button>
        </div>
      ) : state.status === 'loading' && state.entries.length === 0 ? (
        <div className="op-loading" role="status" aria-label={t('catalog.loading')}><LoadingSpinner size="lg" /></div>
      ) : state.entries.length === 0 ? (
        <CatalogEmpty
          icon={filtered ? 'filter' : 'store'}
          title={emptyTitle || t('catalog.emptyTitle')}
          text={filtered ? t('catalog.emptyFiltered') : (emptyText || t('catalog.emptyBody'))}
        />
      ) : (
        <ul className="cat-list" aria-busy={state.status === 'loading'} aria-label={label}>
          {state.entries.map(entry => (
            <li key={entry.key} className="cat-item">{renderEntry(entry)}</li>
          ))}
        </ul>
      )}

      <CatalogPager
        page={normalized.page}
        totalPages={pages}
        onPage={page => onQueryChange({ ...normalized, page })}
        labels={{ previous: t('catalog.previous'), next: t('catalog.next') }}
      />
    </div>
  )
}
