import { useMemo, useRef, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { backendControlApi } from '../../utils/api'
import { filterLocalModels, sortLocalModels } from '../../utils/localHost'
import ConfirmDialog from '../ConfirmDialog'
import LoadingSpinner from '../LoadingSpinner'
import LocalModelTable from './LocalModelTable'

// Running models on this machine: search, sort, logs and stop. Takes the
// polled data from useLocalMachine rather than fetching it, so a page that
// also draws gauges from the same poll does not ask twice.
//
// `limit` turns it into a preview (the Operate overview): the heaviest models
// first, no search, and a link to the full view.
export default function LocalRunningModels({ machine, addToast, limit, moreHref }) {
  const navigate = useNavigate()
  const { rows, state, error, refresh } = machine
  const [query, setQuery] = useState('')
  const [sort, setSort] = useState(limit ? { key: 'rss_bytes', direction: 'desc' } : { key: 'model_name', direction: 'asc' })
  const [confirmStop, setConfirmStop] = useState(null)
  const [stoppingName, setStoppingName] = useState(null)
  const stoppingRef = useRef(false)
  const invokerRef = useRef(null)

  const visible = useMemo(() => {
    const ordered = sortLocalModels(filterLocalModels(rows, limit ? '' : query), sort)
    return limit ? ordered.slice(0, limit) : ordered
  }, [rows, query, sort, limit])

  const promptStop = (model, invoker) => {
    invokerRef.current = invoker
    setConfirmStop(model)
  }

  const cancelStop = () => {
    setConfirmStop(null)
    requestAnimationFrame(() => invokerRef.current?.focus())
  }

  const stop = async () => {
    const model = confirmStop
    if (!model || stoppingRef.current) return
    stoppingRef.current = true
    setStoppingName(model.model_name)
    try {
      await backendControlApi.shutdown({ model: model.model_name })
      addToast?.(`Stopped ${model.model_name}`, 'success')
    } catch (err) {
      addToast?.(`Could not stop ${model.model_name}: ${err.message || err}`, 'error')
    } finally {
      await refresh()
      setConfirmStop(null)
      setStoppingName(null)
      stoppingRef.current = false
    }
  }

  const hidden = limit ? Math.max(0, rows.length - visible.length) : 0

  return (
    <div className="fleet-workbench local-running" data-testid="local-running-models">
      {/* A preview sits under its own section heading, which says this. */}
      {!limit && <div className="model-workbench__scope">
        <div><strong>Running models</strong><span>Loaded on this machine, with the memory and CPU each backend process is using</span></div>
        {state === 'loaded' && <span aria-live="polite">{rows.length} running</span>}
      </div>}
      {state === 'loading' && <div className="model-workbench__state" role="status"><LoadingSpinner size="sm" /><strong>Loading running models…</strong></div>}
      {state === 'error' && <div className="model-workbench__state model-workbench__state--error" role="alert"><i className="fas fa-triangle-exclamation" aria-hidden="true" /><strong>Unable to load running models</strong><span>{error}</span><button type="button" className="btn btn-secondary btn-sm" onClick={() => refresh()}>Retry</button></div>}
      {state === 'loaded' && rows.length === 0 && (
        <div className="model-workbench__state local-running__empty" data-testid="local-running-empty">
          <i className="fas fa-layer-group" aria-hidden="true" />
          <strong>No models running</strong>
          <span>A model loads on its first request, or when you start it from <Link to="/app/models?view=installed">Models</Link>. It will appear here while it is in memory.</span>
        </div>
      )}
      {state === 'loaded' && rows.length > 0 && <>
        {!limit && <div className="model-toolbar"><input className="input fleet-toolbar__search" type="search" aria-label="Search running models" placeholder="Search model or backend…" value={query} onChange={event => setQuery(event.target.value)} /></div>}
        <LocalModelTable models={visible} sort={sort} onSortChange={setSort} stoppingName={stoppingName}
          onViewLogs={model => navigate(`/app/backend-logs/${encodeURIComponent(model.model_name)}`)}
          onStop={promptStop} />
        {moreHref && (
          <div className="fleet-pagination local-running__more">
            {hidden > 0 && <span>{hidden} more not shown</span>}
            <Link to={moreHref} className="btn btn-secondary btn-sm">Open this machine <i className="fas fa-arrow-right" aria-hidden="true" /></Link>
          </div>
        )}
      </>}
      <ConfirmDialog open={!!confirmStop} title={confirmStop ? `Stop ${confirmStop.model_name}?` : 'Stop model?'}
        message={confirmStop ? `This stops the ${confirmStop.backend || 'backend'} process serving ${confirmStop.model_name} and frees its memory. The next request that uses it loads it again.` : ''}
        confirmLabel="Stop model" pendingLabel="Stopping…" pending={!!stoppingName} danger onConfirm={stop} onCancel={cancelStop} />
    </div>
  )
}
