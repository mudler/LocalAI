/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { modelsApi, nodesApi } from '../utils/api'
import { labelIndex } from '../utils/nodeLabelSuggestions'
import { loadedOn, rulePlan, ruleKind, ruleTarget, selectorOf } from '../utils/swarm'
import { useNodeList, useReplicas, useRules } from '../hooks/useSwarm'
import HomeUndoToast from '../components/home/HomeUndoToast'
import RuleSheet from '../components/swarm/RuleSheet'
import Icon from '../components/Icon'
import './swarm.css'

const UNDO_MS = 6000
const MATRIX_NODES = 8

// "{{model}} keeps ..." with the model and the labels drawn as real elements.
function fill(template, parts) {
  return template.split(/(\{\{\w+\}\})/).map((piece, index) => {
    const match = piece.match(/^\{\{(\w+)\}\}$/)
    return match ? <Fragment key={index}>{parts[match[1]]}</Fragment> : piece
  })
}

function Sentence({ rule, t }) {
  const kind = ruleKind(rule)
  const selector = selectorOf(rule)
  const pairs = Object.entries(selector)
  const model = <strong className="dk-mono">{rule.model_name}</strong>
  const labels = pairs.length > 0
    ? <span className="sw-sels">{pairs.map(([k, v]) => <code key={k} className="sw-sel dk-mono">{k}={v}</code>)}</span>
    : null
  const min = Number(rule.min_replicas) || 0
  const max = Number(rule.max_replicas) || 0
  const policy = rule.route_policy === 'prefix_cache' ? t('rules.byPrefix') : rule.route_policy === 'round_robin' ? t('rules.byRound') : ''
  let key
  if (kind === 'spread') key = labels ? 'rules.sentence.spreadLabels' : 'rules.sentence.spreadAll'
  else if (kind === 'autoscale') {
    const range = max === 0 ? 'atLeast' : min === max ? 'exactly' : min === 0 ? 'upTo' : 'between'
    key = `rules.sentence.auto.${range}${labels ? 'Labels' : 'Any'}`
  } else if (kind === 'placement') key = 'rules.sentence.placement'
  else key = 'rules.sentence.inactive'
  const text = t(key, { min, max })
  return <>{fill(text, { model, labels })}{policy && <>{' '}{policy}</>}</>
}

// Rules say which model may run where. Each is written as a sentence, shows
// where the model is loaded now, and opens in a side sheet to edit with a
// preview of what the draft would do. Deleting waits a few seconds so it can be
// taken back.
export default function Scheduling() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation('swarm')
  const [params, setParams] = useSearchParams()
  const { rules, loaded, refresh } = useRules()
  const { nodes, status: nodeStatus } = useNodeList({ interval: 15000 })
  const replicas = useReplicas()
  const [sheet, setSheet] = useState(null)
  const [aliases, setAliases] = useState({})
  const [pendingDelete, setPendingDelete] = useState(null)
  const pending = useRef(null)
  pending.current = pendingDelete

  const loadReplicas = replicas.load
  useEffect(() => { void loadReplicas() }, [loadReplicas])

  useEffect(() => {
    let cancelled = false
    modelsApi.listAliases()
      .then(data => { if (!cancelled && Array.isArray(data)) setAliases(Object.fromEntries(data.map(a => [a.name, a.target]))) })
      .catch(() => {})
    return () => { cancelled = true }
  }, [])

  // The shortcut from Failover: /app/scheduling?new=<model>&min=2 opens the
  // sheet on a rule that asks for that many replicas.
  useEffect(() => {
    const model = params.get('new')
    if (!model) return
    setSheet({ editing: false, initial: { model_name: model, min_replicas: Number(params.get('min')) || 2, max_replicas: 0 } })
    setParams(current => { const next = new URLSearchParams(current); next.delete('new'); next.delete('min'); return next }, { replace: true })
  }, [params, setParams])

  const labels = useMemo(() => labelIndex(nodes), [nodes])
  const ready = nodeStatus === 'ready'
  const visible = rules.filter(rule => rule.model_name !== pendingDelete?.model_name)

  const save = async config => {
    try {
      await nodesApi.setScheduling(config)
      addToast(t('rules.saved'), 'success')
      setSheet(null)
      refresh()
      replicas.load(true)
    } catch (err) {
      addToast(t('rules.saveFailed', { message: err.message }), 'error')
    }
  }

  // The server has no hold on a delete, so the wait is in the browser: the row
  // goes at once, the call runs when the time ends, Undo makes it stay. Asking
  // for a second delete, or leaving the page, finishes the first.
  const commit = useCallback(async entry => {
    if (!entry) return
    try {
      await nodesApi.deleteScheduling(entry.model_name)
      addToast(t('rules.removed'), 'success')
    } catch (err) {
      addToast(t('rules.removeFailed', { message: err.message }), 'error')
    } finally {
      refresh()
    }
  }, [addToast, refresh, t])

  const askDelete = rule => {
    if (pending.current) void commit(pending.current)
    setPendingDelete(rule)
  }
  const expire = () => { const entry = pending.current; setPendingDelete(null); void commit(entry) }
  const undo = () => setPendingDelete(null)
  const commitRef = useRef(commit)
  commitRef.current = commit
  useEffect(() => () => { if (pending.current) void commitRef.current(pending.current) }, [])

  const rows = replicas.state === 'loaded' ? replicas.rows : null
  const backendNodes = nodes.filter(node => (node.node_type || 'backend') === 'backend')
  const matrixNodes = backendNodes.slice(0, MATRIX_NODES)

  return (
    <div className="page page--wide sw-page" data-testid="placement-rules">
      <h1 className="dk-sr-only">{t('rules.title')}</h1>

      <div className="sw-bar sw-bar--lede">
        <p className="sw-note">{t('rules.lede')}</p>
        <div className="sw-bar__acts">
          <button type="button" className="dk-btn dk-btn--primary" onClick={() => setSheet({ editing: false, initial: undefined })}>
            <Icon name="plus" /> {t('rules.new')}
          </button>
        </div>
      </div>

      <section className="sw-section" aria-labelledby="sw-rules-h">
        <h2 className="sw-h2" id="sw-rules-h">{t('rules.heading')} <span className="sw-count dk-mono">{visible.length}</span></h2>
        {loaded && visible.length === 0 ? (
          <div className="dk-empty" data-testid="rules-empty">
            <div className="dk-empty-icon"><Icon name="pin" /></div>
            <h3 className="dk-empty-title">{t('rules.emptyTitle')}</h3>
            <p className="dk-empty-text">{t('rules.empty')}</p>
          </div>
        ) : (
          <ol className="sw-rules">
            {visible.map(rule => {
              const kind = ruleKind(rule)
              const target = ruleTarget(rule)
              const governs = rule.target_model && rule.target_model !== rule.model_name ? rule.target_model : null
              const dangling = rule.model_is_alias && !governs
              const until = rule.unsatisfiable_until ? new Date(rule.unsatisfiable_until) : null
              const unsatisfiable = until && until.getTime() > Date.now()
              const placed = rows ? loadedOn(rows, nodes, target) : null
              return (
                <li key={rule.id || rule.model_name} className="sw-rule" data-kind={kind} data-testid="rule-row">
                  <div className="sw-rule__main">
                    <p className="sw-rule__sentence"><Sentence rule={rule} t={t} /></p>
                    <p className="sw-rule__meta">
                      <span>{t(`rules.kind.${kind}`)}</span>
                      <span>{rule.route_policy ? t(`rules.route.${rule.route_policy}`, rule.route_policy) : t('rules.route.default')}</span>
                      {placed && (placed.length > 0
                        ? <span>{t('rules.placedOn')} <strong className="dk-mono">{placed.map(n => n.name).join(', ')}</strong></span>
                        : <span>{t('rules.notLoaded')}</span>)}
                      {governs && <span className="text-meta scheduling-rule-target"><Icon name="arrow-right" className="icon-before" />{governs}</span>}
                      {dangling && <span className="text-meta scheduling-rule-target--broken">{t('rules.aliasBroken')}</span>}
                      {rule.route_policy === 'prefix_cache' && !!(rule.min_prefix_match || rule.balance_abs_threshold || rule.balance_rel_threshold) && (
                        <span className="dk-mono">{t('rules.thresholds', {
                          match: rule.min_prefix_match || t('rules.inherit'),
                          abs: rule.balance_abs_threshold || t('rules.inherit'),
                          rel: rule.balance_rel_threshold || t('rules.inherit'),
                        })}</span>
                      )}
                      {rule.shadowed && (
                        <span className="scheduling-rule-shadowed" title={t('rules.shadowedTitle')}>
                          <Icon name="eye-off" className="icon-before" />{t('rules.shadowed')}
                        </span>
                      )}
                      {!rule.shadowed && unsatisfiable && (
                        <span className="sw-unsat" title={t('rules.unsatisfiableTitle', { when: until.toLocaleString() })}>
                          <Icon name="warning" className="icon-before" />
                          {t('rules.unsatisfiable', { time: until.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) })}
                        </span>
                      )}
                    </p>
                  </div>
                  <div className="sw-rule__acts scheduling-rule-actions">
                    <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon" aria-label={`Edit ${rule.model_name}`} title={t('rules.edit')} onClick={() => setSheet({ editing: true, initial: rule })}><Icon name="edit" /></button>
                    <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon" aria-label={`Delete ${rule.model_name}`} title={t('rules.delete')} onClick={() => askDelete(rule)}><Icon name="trash" /></button>
                  </div>
                </li>
              )
            })}
          </ol>
        )}
      </section>

      {visible.length > 0 && ready && matrixNodes.length > 0 && (
        <section className="sw-section" aria-labelledby="sw-matrix-h" data-testid="placement-preview">
          <h2 className="sw-h2" id="sw-matrix-h">
            {t('matrix.title')} <span className="sw-badge-preview">{t('preview.label')}</span>
          </h2>
          <div className="dk-table-wrap sw-wrap">
            <table className="dk-table dk-table--compact sw-table sw-matrix">
              <caption className="dk-sr-only">{t('matrix.caption')}</caption>
              <thead>
                <tr>
                  <th scope="col">{t('matrix.model')}</th>
                  {matrixNodes.map(node => <th key={node.id} scope="col" className="dk-mono">{node.name}</th>)}
                </tr>
              </thead>
              <tbody>
                {visible.map(rule => {
                  const plan = rulePlan(rule, nodes)
                  const planned = new Map(plan.planned.map(p => [p.node.id, p.replicas]))
                  const eligible = new Set(plan.eligible.map(n => n.id))
                  const here = rows ? new Set(loadedOn(rows, nodes, ruleTarget(rule)).map(n => n.id)) : new Set()
                  return (
                    <tr key={rule.id || rule.model_name} data-row>
                      <th scope="row" className="dk-mono">{rule.model_name}</th>
                      {matrixNodes.map(node => {
                        const state = here.has(node.id) ? 'loaded' : planned.has(node.id) ? 'planned' : eligible.has(node.id) ? 'eligible' : 'no'
                        return <td key={node.id} data-cell={state}>{state === 'no' ? '—' : t(`matrix.cell.${state}`)}</td>
                      })}
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
          <p className="sw-note sw-note--quiet">
            {t('matrix.how')}
            {backendNodes.length > matrixNodes.length && ` ${t('matrix.more', { shown: matrixNodes.length, total: backendNodes.length })}`}
          </p>
        </section>
      )}

      {sheet && (
        <RuleSheet
          key={sheet.initial?.model_name || 'new'}
          initial={sheet.initial}
          editing={sheet.editing}
          labels={labels}
          aliases={aliases}
          nodes={ready ? nodes : null}
          onSave={save}
          onClose={() => setSheet(null)}
        />
      )}

      {pendingDelete && (
        <HomeUndoToast
          message={t('rules.undoMessage', { name: pendingDelete.model_name })}
          undoLabel={t('rules.undo')}
          dismissLabel={t('rules.undoDismiss')}
          duration={UNDO_MS}
          testId="rule-undo-toast"
          onUndo={undo}
          onExpire={expire}
        />
      )}
    </div>
  )
}
