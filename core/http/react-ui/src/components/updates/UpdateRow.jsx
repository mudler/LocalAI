/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import { Button, StatusChip } from '../ui'
import Icon from '../Icon'
import { cssVars } from '../../utils/modelLedger'
import { upgradeFailureWords } from '../addons/updateReview'
import { reasonText } from '../addons/UpgradeControls'
import { hasNodeView } from '../../utils/nodeUpdates'
import ModelRowActions from './ModelRowActions'
import { modelChip, useModelDetail } from './modelWords'

const TILE_ICON = { gallery: 'server', library: 'server', addon: 'puzzle', model: 'brain' }
const STATE_ICON = { failed: 'alert-circle', held: 'lock', blocked: 'warning', 'on-hold': 'lock', gone: 'warning', cleanup: 'trash' }

// The chip of a backend whose new build did not start and was replaced by the
// previous one: the word and the look of the item of an update run.
const ROLLED_BACK_CHIP = { tone: 'warn', icon: 'history' }

// The chip word of a row. One word, never two states.
function chipOf(row) {
  if (row.state === 'ready') return row.subtype === 'drift' ? 'differs' : 'update-ready'
  if (row.state === 'current') return null
  return row.state
}

const trimStop = (s) => String(s || '').trim().replace(/[.\s]+$/, '')

// The origin line: where the new version comes from.
export function originText(row, t) {
  if (row.kind === 'model') return row.gallery ? t('row.origin.model', { gallery: row.gallery }) : t('row.origin.modelUnnamed')
  if (row.kind === 'gallery') return t('row.origin.gallery')
  const source = row.sourceId ? t('row.origin.library', { id: row.sourceId }) : t('row.origin.libraryUnnamed')
  return row.signed ? `${source} · ${t('row.signed')}` : source
}

function useOrigin(row) {
  const { t } = useTranslation('updates')
  return originText(row, t)
}

// The words under the name: why the row is where it is. Empty when the row says
// everything in its first two lines.
function useDetail(row) {
  const { t } = useTranslation('updates')
  const { t: ta } = useTranslation('addons')
  switch (row.state) {
    case 'failed': {
      // The same sentence as the rolled-back item of a run: the error of the
      // operation is the bare cause.
      if (row.rolledBack && row.error) {
        return { tone: 'error', text: t('run.detail.rolledBack', { reason: trimStop(row.error), name: row.name, version: row.from || '?' }) }
      }
      const why = row.errorKey ? upgradeFailureWords(row.errorKey, ta) : (row.error || t('row.noReason'))
      return { tone: 'error', text: t('row.failedWhy', { message: why }) }
    }
    case 'held':
      return { tone: 'warn', text: t('row.held', { version: row.to, count: row.heldCount || 0 }) }
    case 'blocked':
      if (row.blocked === 'installsDisabled') return { tone: 'warn', text: t('row.blocked.installsDisabled') }
      if (row.blocked === 'incompatible') return { tone: 'warn', text: `${t('row.blocked.incompatible', { version: row.to })}${row.reasons && row.reasons.length ? ` ${row.reasons.map(r => ta(`updates.reason.${r}`)).join('; ')}.` : ''}` }
      return { tone: 'warn', text: t('row.blocked.integrity') }
    case 'updating':
      if (row.queued) return { tone: 'muted', text: t('row.queued') }
      return { tone: 'accent', text: row.phase ? t('row.updatingPhase', { phase: row.phase }) : t('row.updatingPlain') }
    case 'ready':
      if (row.heldBack) return { tone: 'warn', text: t('row.heldBack') }
      if (row.needsCheck) return { tone: 'muted', text: reasonText(row.availability, ta) }
      if (row.reason) return { tone: 'muted', text: row.reason }
      return null
    default:
      return null
  }
}

// One thing that can be updated. The same markup for a backend, a library
// backend and an add-on: the kind only changes the tag, the origin and the
// words under the name. The row is one list item with one chip and at most one
// button to act on it; Update opens the review of that row. The first column
// stays empty so the rows line up with the items of an update run. A backend
// on several nodes adds a link to its per-node page, where each node can be
// updated on its own.
export default function UpdateRow({ row, onUpdate, onRetry, onReview, onCancel, onCheck, checking = false, locked = false, lockedReason = '', model = null }) {
  const { t } = useTranslation('updates')
  const origin = useOrigin(row)
  const otherDetail = useDetail(row)
  const modelDetail = useModelDetail(row)
  const isModel = row.kind === 'model'
  const detail = isModel ? modelDetail : otherDetail
  const rolledBack = !isModel && row.state === 'failed' && row.rolledBack
  const chip = isModel || rolledBack ? null : chipOf(row)
  const ownChip = isModel ? modelChip(row, t) : rolledBack ? { ...ROLLED_BACK_CHIP, label: t('run.phase.rolled_back') } : null
  const tone = row.state === 'ready' ? 'accent' : row.state === 'updating' ? 'accent' : row.state === 'current' ? 'ok' : row.state === 'failed' ? 'error' : (row.state === 'on-hold' || row.state === 'unknown') ? 'idle' : row.state === 'cleanup' ? 'accent' : 'warn'
  const icon = STATE_ICON[row.state] || TILE_ICON[row.kind]
  const nodes = row.nodeCount > 1 ? t('row.nodes', { count: row.nodeCount }) : ''
  const drift = row.nodeCount > 1 && row.driftNodes > 0 ? t('row.drift', { count: row.driftNodes }) : ''
  const versions = (row.state === 'current' || (isModel && !row.hasUpdate))
    ? row.from
    : t('row.versions', { from: row.from || '?', to: row.to || '?' })
  return (
    <li
      className="uc-row"
      data-testid="update-row"
      data-id={row.id}
      data-kind={row.kind}
      data-state={row.state}
      data-name={row.name}
    >
      <span className="uc-row__check" />
      <span className="uc-row__tile" data-tone={tone}><Icon name={icon} /></span>
      <div className="uc-row__main">
        <div className="uc-row__name">
          <strong className="uc-row__title">{row.name}</strong>
          <span className="uc-row__tag">{t(`row.tag.${row.kind}`)}</span>
        </div>
        <div className="uc-row__meta">
          <span className="uc-row__versions">{versions}</span>
          <span>{origin}</span>
          {nodes && <span>{nodes}{drift ? `, ${drift}` : ''}</span>}
          {row.old && <span className="uc-row__old">{t('row.old')}</span>}
        </div>
        {detail && <p className="uc-row__detail" data-tone={detail.tone}>{detail.text}</p>}
        {row.state === 'updating' && (
          <span
            className="dk-progress uc-row__bar"
            role="progressbar"
            aria-label={t('row.progress', { name: row.name })}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={row.progress || 0}
            data-queued={row.queued ? '' : undefined}
            data-indeterminate={row.queued || row.progress > 0 ? undefined : ''}
          >
            <span className="dk-progress-bar" style={!row.queued && row.progress > 0 ? cssVars({ '--dk-value': `${row.progress}%` }) : undefined} />
          </span>
        )}
      </div>
      <div className="uc-row__chip">
        {chip && <StatusChip status={chip} testId="update-row-chip" />}
        {ownChip && <StatusChip {...ownChip} testId="update-row-chip" />}
      </div>
      {isModel ? <ModelRowActions row={row} actions={model} locked={locked} /> : <div className="uc-row__acts">
        {hasNodeView(row) && (row.state === 'ready' || row.state === 'failed') && (
          <Button size="sm" variant="ghost" to={`/app/updates/backend/${encodeURIComponent(row.name)}`} aria-label={t('row.nodesNamed', { name: row.name })} data-testid="update-row-nodes">{t('row.nodesAction')}</Button>
        )}
        {row.state === 'ready' && row.startable && (
          <Button size="sm" disabled={locked} title={lockedReason || undefined} onClick={() => onUpdate(row)} aria-label={t('row.updateNamed', { name: row.name })}>{t('row.update')}</Button>
        )}
        {row.state === 'ready' && !row.startable && row.needsCheck && row.kind === 'addon' && (
          <Button size="sm" icon="refresh" busy={checking} disabled={locked || checking} onClick={() => onCheck(row)} aria-label={t('row.checkNamed', { name: row.name })}>{t('row.check')}</Button>
        )}
        {row.state === 'failed' && (
          <Button size="sm" icon="refresh" disabled={locked} title={lockedReason || undefined} onClick={() => onRetry(row)} aria-label={t('row.retryNamed', { name: row.name })}>{t('row.retry')}</Button>
        )}
        {row.state === 'held' && row.grant && (
          <Button size="sm" onClick={() => onReview(row)} aria-label={t('row.reviewNamed', { name: row.name })}>{t('row.review')}</Button>
        )}
        {row.state === 'updating' && row.cancellable && (
          <Button size="sm" variant="ghost" onClick={() => onCancel(row)} aria-label={t('row.cancelNamed', { name: row.name })}>{t('row.cancel')}</Button>
        )}
      </div>}
    </li>
  )
}
