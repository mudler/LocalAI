/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import ModelPicker from './ModelPicker'
import KeyValueChips from '../nodes/KeyValueChips'
import { rulePlan, selectorOf } from '../../utils/swarm'
import Icon from '../Icon'

function selectorObject(value) {
  return selectorOf({ node_selector: value })
}

function configMode(config) {
  if (!config) return 'placement'
  if (config.spread_all) return 'spread'
  if (config.min_replicas > 0 || config.max_replicas > 0) return 'autoscaling'
  return 'placement'
}

function Presets({ value, onChange, presets, label }) {
  return (
    <div className="sw-presets" role="group" aria-label={label}>
      {presets.map(({ v, l }) => (
        <button key={v} type="button" className="dk-chip dk-chip--sm dk-mono" aria-pressed={value === v} onClick={() => onChange(v)}>{l || v}</button>
      ))}
    </div>
  )
}

// What the draft rule would do with the nodes as they are now, in sentences. It
// is a reading of the roster and the labels; the scheduler also weighs free
// memory and disk when a model loads, which this cannot see.
export function RulePreview({ draft, nodes }) {
  const { t } = useTranslation('swarm')
  const plan = useMemo(() => rulePlan(draft, nodes || []), [draft, nodes])
  const ready = Array.isArray(nodes)
  const names = list => list.map(item => item.name).join(', ')
  let body
  if (!draft.model_name) body = <li>{t('sheet.preview.pickModel')}</li>
  else if (!ready) body = <li>{t('sheet.preview.noRoster')}</li>
  else if (plan.kind === 'placement' && Object.keys(plan.selector).length === 0) body = <li>{t('sheet.preview.needLabels')}</li>
  else if (plan.eligible.length === 0) {
    body = <li data-level="error">{t('sheet.preview.none', { labels: Object.entries(plan.selector).map(([k, v]) => `${k}=${v}`).join(', ') || t('sheet.preview.anyNode') })}</li>
  } else if (plan.kind === 'spread') {
    body = <li><strong className="dk-mono">{draft.model_name}</strong> {t('sheet.preview.spread', { count: plan.eligible.length, nodes: names(plan.eligible) })}</li>
  } else if (plan.kind === 'autoscale') {
    body = <>
      <li><strong className="dk-mono">{draft.model_name}</strong> {t('sheet.preview.autoscale', { count: plan.wanted, nodes: plan.planned.map(p => `${p.node.name}${p.replicas > 1 ? ` ×${p.replicas}` : ''}`).join(', ') })}</li>
      {plan.shortfall > 0 && <li data-level="warn">{t('sheet.preview.shortfall', { count: plan.shortfall })}</li>}
    </>
  } else {
    body = <li><strong className="dk-mono">{draft.model_name}</strong> {t('sheet.preview.placement', { count: plan.eligible.length, nodes: names(plan.eligible) })}</li>
  }
  return (
    <section className="sw-preview" aria-label={t('sheet.preview.title')} data-testid="rule-preview">
      <h3 className="sw-preview__title">{t('sheet.preview.title')} <span className="sw-badge-preview">{t('preview.label')}</span></h3>
      <ul className="sw-preview__list">{body}</ul>
      <p className="sw-note sw-note--quiet">{t('sheet.preview.how')}</p>
    </section>
  )
}

// The editor for one rule, in a side sheet so the list stays in view behind it
// and a rule is easy to look at twice. On a phone it rises from the bottom.
//
//   initial   the rule being edited, or a starting draft for a new one
//   editing   true when `initial` is a saved rule (its model is then fixed)
export default function RuleSheet({ initial, editing, labels, aliases, nodes, onSave, onClose }) {
  const { t } = useTranslation('swarm')
  const [mode, setMode] = useState(() => configMode(initial))
  const [modelName, setModelName] = useState(initial?.model_name || '')
  const [selector, setSelector] = useState(() => selectorObject(initial?.node_selector))
  const [minReplicas, setMinReplicas] = useState(initial?.min_replicas ?? 1)
  const [maxReplicas, setMaxReplicas] = useState(initial?.max_replicas ?? 0)
  // An empty routing policy means "inherit the cluster default", and a
  // threshold of 0 inherits too, so they stay out of effect until set.
  const [routePolicy, setRoutePolicy] = useState(initial?.route_policy || '')
  const [balanceAbs, setBalanceAbs] = useState(initial?.balance_abs_threshold ?? 0)
  const [balanceRel, setBalanceRel] = useState(initial?.balance_rel_threshold ?? 0)
  const [minPrefix, setMinPrefix] = useState(initial?.min_prefix_match ?? 0)
  const [saving, setSaving] = useState(false)
  const sheetRef = useRef(null)
  const closeRef = useRef(onClose)
  closeRef.current = onClose

  useEffect(() => {
    const opener = document.activeElement
    const sheet = sheetRef.current
    sheet?.querySelector('input:not([readonly]), button.dk-seg')?.focus()
    const onKey = (e) => {
      if (e.key === 'Escape') {
        // An open suggestion list takes its own Escape and closes first.
        if (e.target?.closest?.('[role="listbox"]') || e.target?.getAttribute?.('aria-expanded') === 'true') return
        e.preventDefault(); e.stopPropagation(); closeRef.current(); return
      }
      if (e.key !== 'Tab' || !sheet) return
      const focusable = Array.from(sheet.querySelectorAll('button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea, [tabindex]:not([tabindex="-1"])'))
      if (focusable.length === 0) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus() }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus() }
    }
    document.addEventListener('keydown', onKey, true)
    return () => {
      document.removeEventListener('keydown', onKey, true)
      if (opener && document.contains(opener)) opener.focus?.()
    }
  }, [])

  const hasSelector = Object.keys(selector).length > 0
  const aliasHints = Object.fromEntries(Object.entries(aliases || {}).map(([name, target]) => [name, t('sheet.aliasOf', { target })]))
  const aliasTarget = (aliases || {})[modelName]

  const valid = () => {
    if (!modelName) return false
    if (mode === 'placement') return hasSelector
    if (mode === 'spread') return true
    return minReplicas > 0 || maxReplicas > 0
  }

  const config = {
    model_name: modelName,
    node_selector: hasSelector ? selector : undefined,
    min_replicas: mode === 'autoscaling' ? minReplicas : 0,
    max_replicas: mode === 'autoscaling' ? maxReplicas : 0,
    spread_all: mode === 'spread',
    route_policy: routePolicy,
    balance_abs_threshold: balanceAbs,
    balance_rel_threshold: balanceRel,
    min_prefix_match: minPrefix,
  }

  const submit = async () => {
    setSaving(true)
    try { await onSave(config) } finally { setSaving(false) }
  }

  const modeHelp = mode === 'placement' ? t('sheet.help.placement') : mode === 'spread' ? t('sheet.help.spread') : t('sheet.help.autoscaling')

  return createPortal(
    <div className="dk-sheet-veil" data-state="open" onMouseDown={onClose}>
      <div
        ref={sheetRef}
        className="dk-sheet sw-sheet"
        role="dialog"
        aria-modal="true"
        aria-labelledby="sw-sheet-title"
        data-state="open"
        data-testid="rule-sheet"
        onMouseDown={e => e.stopPropagation()}
      >
        <span className="dk-sheet-grip" aria-hidden="true" />
        <div className="dk-sheet-head">
          <div>
            <h2 className="dk-sheet-title" id="sw-sheet-title">{editing ? t('sheet.editTitle') : t('sheet.newTitle')}</h2>
            <p className="dk-sheet-desc">{t('sheet.desc')}</p>
          </div>
          <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" aria-label={t('sheet.close')} onClick={onClose}><Icon name="close" /></button>
        </div>
        <div className="dk-sheet-body sw-sheet__body">
          <div className="dk-field">
            <label className="dk-label" htmlFor="sched-model">{t('sheet.model')}</label>
            {editing ? (
              <input id="sched-model" className="dk-input dk-input--mono" value={modelName} readOnly />
            ) : (
              <ModelPicker id="sched-model" value={modelName} onChange={setModelName} hints={aliasHints} />
            )}
            {/* An alias is a stable name for whichever model serves it, so a rule
                on one is a rule on a slot. Say so where it is chosen. */}
            {aliasTarget && (
              <span className="sw-hint"><Icon name="link" /> {t('sheet.aliasNote', { name: modelName, target: aliasTarget })}</span>
            )}
          </div>

          <div className="dk-field">
            <span className="dk-label" id="sw-mode-label">{t('sheet.how')}</span>
            <div className="dk-segmented" role="radiogroup" aria-labelledby="sw-mode-label">
              {['placement', 'autoscaling', 'spread'].map(key => (
                <button key={key} type="button" role="radio" aria-checked={mode === key} className="dk-seg" onClick={() => setMode(key)}>{t(`sheet.mode.${key}`)}</button>
              ))}
            </div>
            <span className="sw-hint">{modeHelp}</span>
          </div>

          <div className="dk-field">
            <span className="dk-label">{mode === 'placement' ? t('sheet.selector') : t('sheet.selectorOptional')}</span>
            <KeyValueChips
              pairs={selector}
              onAdd={(k, v) => setSelector(prev => ({ ...prev, [k]: v }))}
              onRemove={k => setSelector(prev => { const next = { ...prev }; delete next[k]; return next })}
              placeholderKey={t('sheet.selectorKey')}
              placeholderValue={t('sheet.selectorValue')}
              ariaLabel="Node selector"
              ariaLabelKey="Selector key"
              ariaLabelValue="Selector value"
              addLabel="Add selector"
              suggestions={labels}
            />
            <span className="sw-hint">
              {mode === 'placement' ? t('sheet.selectorHelp.placement') : hasSelector ? t('sheet.selectorHelp.some') : t('sheet.selectorHelp.empty')}
            </span>
          </div>

          {mode === 'autoscaling' && (
            <div className="sw-two">
              <div className="dk-field">
                <label className="dk-label" htmlFor="sched-min">{t('sheet.min')}</label>
                <input id="sched-min" className="dk-input" type="number" min={0} value={minReplicas} onChange={e => setMinReplicas(parseInt(e.target.value) || 0)} />
                <Presets label={t('sheet.minPresets')} value={minReplicas} onChange={setMinReplicas} presets={[{ v: 1 }, { v: 2 }, { v: 3 }, { v: 4 }]} />
              </div>
              <div className="dk-field">
                <label className="dk-label" htmlFor="sched-max">{t('sheet.max')}</label>
                <input id="sched-max" className="dk-input" type="number" min={0} value={maxReplicas} onChange={e => setMaxReplicas(parseInt(e.target.value) || 0)} />
                <Presets label={t('sheet.maxPresets')} value={maxReplicas} onChange={setMaxReplicas} presets={[{ v: 0, l: t('sheet.noLimit') }, { v: 2 }, { v: 4 }, { v: 8 }]} />
              </div>
            </div>
          )}

          <RulePreview draft={config} nodes={nodes} />

          <div className="dk-field">
            <label className="dk-label" htmlFor="sched-route-policy">{t('sheet.routing')}</label>
            <select id="sched-route-policy" className="dk-select" value={routePolicy} onChange={e => setRoutePolicy(e.target.value)}>
              <option value="">{t('sheet.routingDefault')}</option>
              <option value="round_robin">{t('sheet.routingRound')}</option>
              <option value="prefix_cache">{t('sheet.routingPrefix')}</option>
            </select>
            <span className="sw-hint">{t('sheet.routingHelp')}</span>
          </div>

          {routePolicy === 'prefix_cache' && (
            <div className="sw-three">
              <div className="dk-field">
                <label className="dk-label" htmlFor="sched-min-prefix-match">{t('sheet.minPrefix')}</label>
                <input id="sched-min-prefix-match" className="dk-input" type="number" step="0.05" min="0" max="1" value={minPrefix} onChange={e => setMinPrefix(parseFloat(e.target.value) || 0)} />
                <span className="sw-hint">{t('sheet.minPrefixHelp')}</span>
              </div>
              <div className="dk-field">
                <label className="dk-label" htmlFor="sched-balance-abs">{t('sheet.balanceAbs')}</label>
                <input id="sched-balance-abs" className="dk-input" type="number" min="0" value={balanceAbs} onChange={e => setBalanceAbs(parseInt(e.target.value) || 0)} />
                <span className="sw-hint">{t('sheet.balanceAbsHelp')}</span>
              </div>
              <div className="dk-field">
                <label className="dk-label" htmlFor="sched-balance-rel">{t('sheet.balanceRel')}</label>
                <input id="sched-balance-rel" className="dk-input" type="number" step="0.1" min="0" value={balanceRel} onChange={e => setBalanceRel(parseFloat(e.target.value) || 0)} />
                <span className="sw-hint">{t('sheet.balanceRelHelp')}</span>
              </div>
            </div>
          )}

        </div>
        <div className="dk-sheet-foot">
          <button type="button" className="dk-btn dk-btn--secondary" onClick={onClose}>{t('actions.cancel')}</button>
          <button type="button" className="dk-btn dk-btn--primary" disabled={!valid() || saving} onClick={submit}>{t('sheet.save')}</button>
        </div>
      </div>
    </div>,
    document.body,
  )
}
