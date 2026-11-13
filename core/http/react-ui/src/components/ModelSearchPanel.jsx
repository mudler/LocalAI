/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState, useRef, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { modelsApi } from '../utils/api'
import { SEARCH_PROVIDERS, searchParams, resultFacts, failedProviders, resultKey } from '../utils/modelSearch'
import { Button, Callout, EmptyState, SearchField, Segmented, Switch } from './ui'
import './modelSearch.css'

// Search of the remote model providers, above the source field of the import
// page. A result is not imported from here: "Import" puts its uri in the source
// field, where the usual review and the Import button take over.
export default function ModelSearchPanel({ onPick, disabled }) {
  const { t } = useTranslation('importModel')
  const [query, setQuery] = useState('')
  const [providers, setProviders] = useState({ huggingface: true, pirateface: true })
  const [ggufOnly, setGgufOnly] = useState(true)
  const [sort, setSort] = useState('downloads')
  const [state, setState] = useState({ phase: 'idle', data: null, error: '' })
  const abortRef = useRef(null)

  useEffect(() => () => abortRef.current?.abort(), [])

  const anyProvider = SEARCH_PROVIDERS.some(p => providers[p])
  const canSearch = query.trim() !== '' && anyProvider && state.phase !== 'loading'

  const run = async (next = {}) => {
    const params = searchParams({ query, providers, ggufOnly, sort, ...next })
    if (!params.q || !anyProvider) return
    abortRef.current?.abort()
    const ctl = new AbortController()
    abortRef.current = ctl
    setState(s => ({ phase: 'loading', data: s.data, error: '' }))
    try {
      const data = await modelsApi.search(params, { signal: ctl.signal })
      if (ctl.signal.aborted) return
      setState({ phase: 'done', data, error: '' })
    } catch (err) {
      if (ctl.signal.aborted) return
      setState({ phase: 'error', data: null, error: err.message || String(err) })
    }
  }

  const data = state.data
  const results = data?.results || []
  const failed = failedProviders(data)

  return (
    <section className="import-search dk-card" aria-labelledby="import-search-title" data-testid="model-search">
      <div className="import-search__head">
        <h2 className="bt-h2" id="import-search-title">{t('search.title')}</h2>
        <p className="dk-hint">{t('search.hint')}</p>
      </div>

      {/* Not a <form>: the panel sits inside the import form, and forms do not nest. */}
      <div className="import-search__bar" role="search">
        <SearchField
          value={query}
          onChange={setQuery}
          label={t('search.label')}
          placeholder={t('search.placeholder')}
          disabled={disabled}
          testId="model-search-input"
          onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); if (canSearch && !disabled) run() } }}
          autoComplete="off"
          spellCheck="false"
        />
        <Button variant="primary" icon="search" onClick={() => run()} disabled={disabled || !canSearch} busy={state.phase === 'loading'} data-testid="model-search-submit">
          {t('search.go')}
        </Button>
      </div>

      <div className="import-search__opts">
        {SEARCH_PROVIDERS.map(p => (
          <span className="import-search__opt" key={p}>
            <Switch
              checked={providers[p]}
              onChange={(v) => setProviders(prev => ({ ...prev, [p]: v }))}
              labelledBy={`import-search-prov-${p}`}
              disabled={disabled}
              testId={`model-search-provider-${p}`}
            />
            <span id={`import-search-prov-${p}`}>{t(`search.providers.${p}`)}</span>
          </span>
        ))}
        <span className="import-search__opt">
          <Switch checked={ggufOnly} onChange={setGgufOnly} labelledBy="import-search-gguf" disabled={disabled} testId="model-search-gguf" />
          <span id="import-search-gguf">{t('search.ggufOnly')}</span>
        </span>
        <Segmented
          className="import-search__sort"
          label={t('search.sortLabel')}
          value={sort}
          onChange={(id) => { setSort(id); if (state.data) run({ sort: id }) }}
          options={[
            { id: 'downloads', label: t('search.sort.downloads') },
            { id: 'likes', label: t('search.sort.likes') },
            { id: 'updated', label: t('search.sort.updated') },
          ]}
        />
      </div>
      {!anyProvider && <p className="dk-hint" role="status">{t('search.noProvider')}</p>}

      <div className="import-search__out" aria-live="polite" data-testid="model-search-out">
        {state.phase === 'error' && (
          <Callout tone="error" role="alert" title={t('search.failed')} testId="model-search-error">{state.error}</Callout>
        )}
        {failed.length > 0 && (
          <Callout tone="warn" testId="model-search-partial">
            {failed.map(p => t('search.providerDown', { name: t(`search.providers.${p.name}`, { defaultValue: p.name }), error: p.error || '' })).join(' ')}
          </Callout>
        )}
        {state.phase === 'done' && results.length === 0 && failed.length < (data?.providers?.length || 0) && (
          <EmptyState variant="no-results" title={t('search.none')} body={t('search.noneBody')} testId="model-search-empty" />
        )}
        {results.length > 0 && (
          <ul className="import-search__list" data-testid="model-search-results">
            {results.map(r => {
              const facts = resultFacts(r)
              return (
                <li className="import-search__row" key={resultKey(r)} data-provider={r.provider}>
                  <div className="import-search__main">
                    <div className="import-search__name">
                      <a className="dk-mono import-search__id" href={r.url} target="_blank" rel="noopener noreferrer">{r.title || r.id}</a>
                      <span className="dk-badge dk-badge--accent">{t(`search.providers.${r.provider}`, { defaultValue: r.provider })}</span>
                      {r.gated && <span className="dk-badge dk-badge--warn" title={t('search.gatedHint')}>{t('search.gated')}</span>}
                    </div>
                    {facts.length > 0 && (
                      <dl className="import-search__facts">
                        {facts.map(f => (
                          <div key={f.key}><dt>{t(`search.facts.${f.key}`)}</dt><dd>{f.value}</dd></div>
                        ))}
                      </dl>
                    )}
                  </div>
                  <Button size="sm" variant="secondary" icon="import" disabled={disabled} onClick={() => onPick(r.uri)} data-testid="model-search-pick" aria-label={t('search.pickLabel', { name: r.id })}>
                    {t('actions.import')}
                  </Button>
                </li>
              )
            })}
          </ul>
        )}
      </div>
    </section>
  )
}
