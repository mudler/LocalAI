/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useMemo, useRef, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { backendControlApi } from '../../utils/api'
import { filterLocalModels, sortLocalModels } from '../../utils/localHost'
import ConfirmDialog from '../ConfirmDialog'
import LoadingSpinner from '../LoadingSpinner'
import LocalModelTable from './LocalModelTable'
import Icon from '../Icon'

// Running models on this machine: search, sort, logs and stop. Takes the
// polled data from useLocalMachine (or the Operate summary) rather than
// fetching it, so a page that also draws capacity from the same poll does not
// ask twice.
//
// `limit` turns it into a preview (the Status page): the heaviest models
// first, no search, and a link to the full view.
export default function LocalRunningModels({ machine, addToast, limit, moreHref }) {
  const { t } = useTranslation('operate')
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
      addToast?.(t('unload.done', { name: model.model_name }), 'success')
    } catch (err) {
      addToast?.(t('unload.failed', { name: model.model_name, message: err.message || err }), 'error')
    } finally {
      await refresh?.()
      setConfirmStop(null)
      setStoppingName(null)
      stoppingRef.current = false
    }
  }

  const hidden = limit ? Math.max(0, rows.length - visible.length) : 0

  return (
    <div className="op-running" data-testid="local-running-models">
      {!limit && (
        <div className="op-running__scope">
          <div>
            <strong>{t('machine.runningTitle')}</strong>
            <span>{t('machine.runningBody')}</span>
          </div>
          {state === 'loaded' && <span aria-live="polite">{t('machine.runningCount', { count: rows.length })}</span>}
        </div>
      )}
      {state === 'loading' && (
        <div className="op-state" role="status"><LoadingSpinner size="sm" /><strong>{t('machine.loading')}</strong></div>
      )}
      {state === 'error' && (
        <div className="op-state op-state--error" role="alert">
          <Icon name="warning" />
          <strong>{t('machine.loadFailed')}</strong>
          <span>{error}</span>
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={() => refresh?.()}>{t('machine.retry')}</button>
        </div>
      )}
      {state === 'loaded' && rows.length === 0 && (
        <div className="op-state" data-testid="local-running-empty">
          <Icon name="layers" />
          <strong>{t('machine.emptyTitle')}</strong>
          <span>
            {t('machine.emptyBefore')} <Link className="dk-link" to="/app/models?view=installed">{t('machine.emptyLink')}</Link>. {t('machine.emptyAfter')}
          </span>
        </div>
      )}
      {state === 'loaded' && rows.length > 0 && (
        <>
          {!limit && (
            <div className="op-running__tools">
              <input
                className="dk-input"
                type="search"
                aria-label={t('machine.searchLabel')}
                placeholder={t('machine.searchPlaceholder')}
                value={query}
                onChange={event => setQuery(event.target.value)}
              />
            </div>
          )}
          <LocalModelTable
            models={visible}
            sort={sort}
            onSortChange={setSort}
            stoppingName={stoppingName}
            onViewLogs={model => navigate(`/app/backend-logs/${encodeURIComponent(model.model_name)}`)}
            onStop={promptStop}
          />
          {moreHref && (
            <div className="op-running__more">
              {hidden > 0 && <span>{t('machine.moreHidden', { count: hidden })}</span>}
              <Link to={moreHref} className="dk-btn dk-btn--secondary dk-btn--sm">{t('machine.openMachine')} <Icon name="arrow-right" /></Link>
            </div>
          )}
        </>
      )}
      <ConfirmDialog
        open={!!confirmStop}
        title={confirmStop ? t('unload.title', { name: confirmStop.model_name }) : t('unload.titleFallback')}
        message={confirmStop ? t('unload.message', { backend: confirmStop.backend || t('unload.backendFallback'), name: confirmStop.model_name }) : ''}
        confirmLabel={t('unload.confirm')}
        pendingLabel={t('unload.pending')}
        pending={!!stoppingName}
        danger
        onConfirm={stop}
        onCancel={cancelStop}
      />
    </div>
  )
}
