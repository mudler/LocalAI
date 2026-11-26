import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { nodesApi } from '../../utils/api'
import { Button, Switch, Tag, LoadError } from '../ui'
import { draftOf, changesOf } from './nodeLimits'
import './nodes.css'

// The memory limits of one node: the value in force for each per-node memory
// setting, where it comes from, and an editor for the admin overrides. A
// value set here wins over the worker's environment and the cluster settings.
const SOURCE_TONE = { override: 'accent', worker: 'warn', cluster: 'neutral', default: 'neutral' }

export default function NodeLimitsPane({ node, addToast }) {
  const { t } = useTranslation('swarm')
  const [limits, setLimits] = useState(null)
  const [error, setError] = useState(null)
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState(draftOf(null))
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    try {
      const l = await nodesApi.getLimits(node.id)
      setLimits(l)
      setError(null)
    } catch (err) {
      setError(err)
    }
  }, [node.id])

  useEffect(() => { load() }, [load])
  useEffect(() => { if (!editing) setDraft(draftOf(limits)) }, [limits, editing])

  const save = useCallback(async () => {
    const changes = changesOf(limits, draft)
    if (Object.keys(changes).length === 0) { setEditing(false); return }
    setBusy(true)
    try {
      setLimits(await nodesApi.setLimits(node.id, changes))
      addToast?.(t('limits.saved', { name: node.name }), 'success')
      setEditing(false)
    } catch (err) {
      addToast?.(t('limits.failed', { error: err.message || String(err) }), 'error')
    } finally {
      setBusy(false)
    }
  }, [limits, draft, node, addToast, t])

  const reset = useCallback(async () => {
    setBusy(true)
    try {
      setLimits(await nodesApi.resetLimits(node.id))
      addToast?.(t('limits.resetDone', { name: node.name }), 'success')
      setEditing(false)
    } catch (err) {
      addToast?.(t('limits.failed', { error: err.message || String(err) }), 'error')
    } finally {
      setBusy(false)
    }
  }, [node, addToast, t])

  if (error) return <LoadError title={t('limits.loadFailed')} error={error} onRetry={load} />
  if (!limits) return null

  const sources = limits.sources || {}
  const hasOverride = Object.values(sources).includes('override')
  const source = (key) => (
    <Tag tone={SOURCE_TONE[sources[key]] || 'neutral'} testId={`limits-source-${key}`}>
      {t(`limits.source.${sources[key] || 'cluster'}`)}
    </Tag>
  )
  const id = (key) => `limits-${key}-${node.id}`

  const rows = [
    {
      key: 'max_active_backends',
      label: t('limits.maxActive'),
      value: limits.max_active_backends > 0 ? String(limits.max_active_backends) : t('limits.noLimit'),
      input: (
        <input id={id('max_active_backends')} type="number" min="0" className="dk-input cap-row__input"
          value={draft.max_active_backends} disabled={busy}
          onChange={(e) => setDraft({ ...draft, max_active_backends: e.target.value })} />
      ),
    },
    {
      key: 'vram_budget',
      label: t('limits.vramBudget'),
      value: limits.vram_budget || t('limits.none'),
      input: (
        <input id={id('vram_budget')} type="text" className="dk-input cap-row__input cap-row__input--text"
          placeholder={t('limits.vramBudgetPlaceholder')} value={draft.vram_budget} disabled={busy}
          onChange={(e) => setDraft({ ...draft, vram_budget: e.target.value })} />
      ),
    },
    {
      key: 'memory_reclaimer_threshold',
      label: t('limits.threshold'),
      value: limits.memory_reclaimer_enabled
        ? `${Math.round(limits.memory_reclaimer_threshold * 100)}%`
        : t('limits.thresholdOff', { pct: Math.round(limits.memory_reclaimer_threshold * 100) }),
      input: (
        <input id={id('memory_reclaimer_threshold')} type="number" min="1" max="100" className="dk-input cap-row__input"
          value={draft.memory_reclaimer_threshold} disabled={busy}
          onChange={(e) => setDraft({ ...draft, memory_reclaimer_threshold: e.target.value })} />
      ),
    },
    {
      key: 'model_swap',
      label: t('limits.swap'),
      value: limits.model_swap ? t('limits.on') : t('limits.off'),
      input: (
        <Switch id={id('model_swap')} checked={draft.model_swap} disabled={busy} label={t('limits.swap')}
          onChange={(v) => setDraft({ ...draft, model_swap: v })} testId="limits-swap-switch" />
      ),
    },
  ]

  return (
    <div className="sw-limits" data-testid="node-limits">
      {rows.map(row => (
        <div className="cap-row" key={row.key}>
          <div className="cap-row__body">
            <div className="cap-row__line">
              <label htmlFor={id(row.key)} className="cap-row__label">{row.label}</label>
              {editing ? row.input : <span className="cell-mono text-sub" data-testid={`limits-value-${row.key}`}>{row.value}</span>}
              {!editing && source(row.key)}
            </div>
          </div>
        </div>
      ))}
      <div className="cap-row__line">
        {editing ? (
          <>
            <Button variant="primary" size="sm" icon="check" busy={busy} onClick={save}>{t('limits.save')}</Button>
            <Button size="sm" disabled={busy} onClick={() => setEditing(false)}>{t('limits.cancel')}</Button>
          </>
        ) : (
          <>
            <Button size="sm" icon="pencil" onClick={() => setEditing(true)} data-testid="limits-edit">{t('limits.edit')}</Button>
            {hasOverride && (
              <Button size="sm" icon="undo" busy={busy} onClick={reset} data-testid="limits-reset">{t('limits.reset')}</Button>
            )}
          </>
        )}
      </div>
    </div>
  )
}
