/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button, StatusChip, Tag } from '../ui'
import Icon from '../Icon'
import { cssVars } from '../../utils/modelLedger'
import { upgradeFailureWords } from '../addons/updateReview'
import { isBusy, isEnded, needsAttention, nodePhase } from '../../utils/upgradeRun'
import { originText } from './UpdateRow'

const TILE_ICON = { gallery: 'server', library: 'server', addon: 'puzzle', model: 'brain' }

// The chip of each phase: tone, icon and words, one state per chip.
const CHIP = {
  queued: { tone: 'idle', icon: 'clock' },
  running: { status: 'updating' },
  updated: { tone: 'ok', icon: 'check' },
  failed: { status: 'failed' },
  rolled_back: { tone: 'warn', icon: 'history' },
  cancelled: { tone: 'idle', icon: 'close' },
  skipped: { tone: 'idle', icon: 'minus' },
  unconfirmed: { tone: 'warn', icon: 'help-circle' },
}
const TILE_TONE = { running: 'accent', updated: 'ok', failed: 'error', rolled_back: 'warn', unconfirmed: 'warn' }

const trimStop = (s) => String(s || '').trim().replace(/[.\s]+$/, '')

// The sentence of an item that did not reach the new version: what happened,
// the version it is on now, and that its data is unchanged. The reason is said
// with the one failure-words map of every kind.
export function failureText(item, name, t, ta) {
  const version = item.from || '?'
  if (item.phase === 'unconfirmed') return t('run.detail.unconfirmed', { name })
  // A rolled-back item whose new build did not start: the server's error is
  // the bare cause, after the one sentence that says what happened. Another
  // key (a build that could not be put in place) is said with its own words.
  if (item.phase === 'rolled_back' && item.error && (!item.error_key || item.error_key === 'start')) {
    return t('run.detail.rolledBack', { reason: trimStop(item.error), name, version })
  }
  if (item.phase === 'rolled_back') {
    return t('run.detail.failed', { reason: trimStop(upgradeFailureWords(item.error_key || 'start', ta, item.error_key === 'start' ? '' : item.error, 'run')), name, version })
  }
  const reason = trimStop(upgradeFailureWords(item.error_key || 'unknown', ta, item.error, 'run'))
  return t('run.detail.failed', { reason, name, version })
}

// Why a queued item waits, from the reason the planner gave.
// A reason this build does not know says only that the item waits.
function queuedText(item, items, t) {
  const before = (kind) => {
    const at = items.indexOf(item)
    return items.slice(0, at).reverse().find(i => i.kind === kind && !isEnded(i))
  }
  switch (item.queued_reason) {
    case 'after_backends':
      return item.kind === 'addon' ? `${t('run.queued.afterBackends')} ${t('run.queued.oneAddon')}` : t('run.queued.afterBackends')
    case 'after_models':
      return item.kind === 'addon' ? `${t('run.queued.afterModels')} ${t('run.queued.oneAddon')}` : t('run.queued.afterModels')
    case 'backend_limit': return t('run.queued.backendLimit')
    case 'same_backend': return t('run.queued.sameBackend')
    case 'one_model_at_a_time': {
      const prev = before('model')
      return prev ? t('run.queued.waitingFor', { name: prev.displayName || prev.name }) : t('run.queued.oneModel')
    }
    case 'one_addon_at_a_time': {
      const prev = before('addon')
      return prev ? t('run.queued.waitingFor', { name: prev.displayName || prev.name }) : t('run.queued.oneAddon')
    }
    default: return t('run.queued.plain')
  }
}

// What a running item does now: the server's message, and on a backend that
// runs on several nodes the node it is on.
function runningText(item, t) {
  const nodes = Array.isArray(item.nodes) ? item.nodes : []
  const message = item.message || t('run.detail.updating')
  if (nodes.length > 1) {
    const at = nodes.findIndex(n => nodePhase(n) === 'running')
    if (at >= 0) return t('run.detail.node', { index: at + 1, count: nodes.length, message: nodes[at].message || message })
  }
  return message
}

// Why one node of an item did not update: the server's message, or the words
// of its failure code.
function nodeReason(node, ta) {
  if (node.message) return `: ${node.message}`
  if (node.error_key) return `: ${upgradeFailureWords(node.error_key, ta, '', 'run')}`
  return ''
}

// Whether an item offers Cancel: a queued item always (the server drops it
// before it starts; it never marks a queued item cancellable), a running one
// only while its operation says it can stop.
export function canCancel(item) {
  if (!item || isEnded(item)) return false
  return item.phase === 'queued' || (item.phase === 'running' && Boolean(item.cancellable))
}

// One item of the update run, in the markup of the other rows of the page.
//   item      the run item, with displayName set by the page
//   row       the page row of the same key, when the lists still have one
//   items     every item of the run, in run order (for the queued reasons)
export default function RunItemRow({ item, row, items, runActive, busy, onCancel, onRetry }) {
  const { t } = useTranslation('updates')
  const { t: ta } = useTranslation('addons')
  const [open, setOpen] = useState(false)
  const name = item.displayName || item.name
  const origin = row ? originText(row, t) : ''
  const nodes = Array.isArray(item.nodes) ? item.nodes : []
  // An unconfirmed item that the server still checks (verifying) is in
  // progress, not a problem. One that no check could answer is settled and
  // needs a look, also while other items of the run go on.
  const checking = item.phase === 'unconfirmed' && isBusy(item)
  const attention = needsAttention(item)
  const chip = checking ? CHIP.running : (CHIP[item.phase] || CHIP.queued)
  let detail = null
  if (item.phase === 'queued') detail = { tone: 'muted', text: queuedText(item, items, t) }
  else if (item.phase === 'running') detail = { tone: 'accent', text: runningText(item, t) }
  else if (checking) detail = { tone: 'accent', text: item.message || t('run.detail.checking') }
  else if (attention) detail = { tone: item.phase === 'unconfirmed' ? 'warn' : 'error', text: failureText(item, name, t, ta) }
  else if (item.phase === 'cancelled') detail = { tone: 'muted', text: t('run.detail.cancelled', { name, version: item.from || '?' }) }
  else if (item.phase === 'skipped') {
    // A skipped item says why, in the words of its key when it has one.
    const why = item.error_key ? upgradeFailureWords(item.error_key, ta, item.error || item.message, 'run') : (item.error || item.message)
    detail = { tone: item.error_key ? 'warn' : 'muted', text: why ? t('run.detail.skippedWhy', { reason: trimStop(why) }) : t('run.detail.skipped') }
  }
  const progress = Math.max(0, Math.min(100, Math.round(Number(item.progress) || 0)))
  const moreId = `uc-run-more-${item.key}`
  return (
    <li
      className="uc-row"
      data-testid="run-item"
      data-key={item.key}
      data-kind={item.kind}
      data-phase={item.phase}
      data-state={item.phase === 'running' || checking ? 'updating' : item.phase}
    >
      <span className="uc-row__check" />
      <span className="uc-row__tile" data-tone={TILE_TONE[item.phase]}><Icon name={TILE_ICON[item.kind] || 'server'} /></span>
      <div className="uc-row__main">
        <div className="uc-row__name">
          <strong className="uc-row__title">{name}</strong>
          <span className="uc-row__tag">{t(`row.tag.${item.kind}`)}</span>
          {item.kind === 'addon' && <Tag tone="warn">{t('review.backsUp')}</Tag>}
        </div>
        <div className="uc-row__meta">
          <span className="uc-row__versions">{t('row.versions', { from: item.from || '?', to: item.to || '?' })}</span>
          {origin && <span>{origin}</span>}
          {nodes.length > 1 && <span>{t('row.nodes', { count: nodes.length })}</span>}
        </div>
        {detail && <p className="uc-row__detail" data-tone={detail.tone} data-testid="run-item-detail">{detail.text}</p>}
        {item.phase === 'running' && (
          <span
            className="dk-progress uc-row__bar"
            role="progressbar"
            aria-label={t('row.progress', { name })}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={progress}
            data-indeterminate={progress > 0 ? undefined : ''}
          >
            <span className="dk-progress-bar" style={progress > 0 ? cssVars({ '--dk-value': `${progress}%` }) : undefined} />
          </span>
        )}
        {attention && open && (
          <div className="uc-row__more" id={moreId} data-testid="run-item-details">
            {item.error && <p className="uc-row__more-line">{item.error}</p>}
            {item.error_key && <p className="uc-row__more-line">{t('run.details.code', { code: item.error_key })}</p>}
            {nodes.map(n => (
              <p key={n.id || n.name} className="uc-row__more-line">{t('run.details.node', { name: n.name || n.id, phase: t(`run.phase.${nodePhase(n)}`, { defaultValue: n.phase }) })}{nodeReason(n, ta)}</p>
            ))}
            {!item.error && !item.error_key && nodes.length === 0 && <p className="uc-row__more-line">{t('run.details.none')}</p>}
          </div>
        )}
      </div>
      <div className="uc-row__chip">
        <StatusChip {...chip} label={chip.status ? undefined : t(`run.phase.${item.phase}`)} testId="run-item-chip" />
      </div>
      <div className="uc-row__acts">
        {runActive && canCancel(item) && !checking && (
          <Button size="sm" variant="ghost" disabled={Boolean(busy)} onClick={() => onCancel(item.key)} aria-label={t('row.cancelNamed', { name })} data-testid="run-item-cancel">{t('row.cancel')}</Button>
        )}
        {!runActive && attention && item.retryable && (
          <Button size="sm" icon="refresh" disabled={Boolean(busy)} onClick={onRetry} aria-label={t('row.retryNamed', { name })} data-testid="run-item-retry">{t('row.retry')}</Button>
        )}
        {attention && (
          <Button size="sm" variant="ghost" aria-expanded={open} aria-controls={open ? moreId : undefined} onClick={() => setOpen(v => !v)} data-testid="run-item-details-toggle">
            {t('run.details.toggle')}
          </Button>
        )}
      </div>
    </li>
  )
}
