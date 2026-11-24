/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Button, Callout, Tag } from '../ui'
import Icon from '../Icon'
import { useWhen } from '../../hooks/useWhen'
import { cssVars } from '../../utils/modelLedger'
import { attentionOf, busyCount, countsOf, isBusy, isRunning, retryableKeys, runProgress } from '../../utils/upgradeRun'

const SOURCE_KEYS = { gallery: 'gallery', library: 'library', addons: 'addons', models: 'models' }

// The sentence of the strip, from headlineOf.
function headlineText(h, t) {
  switch (h.kind) {
    case 'loading': return t('strip.headline.loading')
    case 'loadError': return t('strip.headline.loadError')
    case 'updates': return t('strip.headline.updates', { count: h.count })
    case 'current': return t('strip.headline.current')
    case 'never': return t('strip.headline.never')
    case 'couldNot': return t('strip.headline.couldNot')
    case 'noneButFailed': return t('strip.headline.noneButFailed', { sources: h.sources.map(s => t(`source.name.${SOURCE_KEYS[s]}`)).join(', ') })
    case 'noneButNever': return t('strip.headline.noneButNever', { sources: h.sources.map(s => t(`source.name.${SOURCE_KEYS[s]}`)).join(', ') })
    case 'unavailable': return t('strip.headline.unavailable')
    default: return ''
  }
}

// The one place that answers "is anything out of date, and can I trust that":
// the sentence, the breakdown, Check now and the primary action. Check now is
// the primary action when nothing has been checked yet.
export default function UpdateStrip({ headline, breakdown, lastChecked, checking, cooling, result, onCheck, primary }) {
  const { t } = useTranslation('updates')
  const when = useWhen()
  const checked = lastChecked ? when(lastChecked, Date.now()) : ''
  const parts = []
  if (headline.kind === 'updates') {
    if (breakdown.ready) parts.push(t('strip.ready', { count: breakdown.ready }))
    if (breakdown.updating) parts.push(t('strip.updating', { count: breakdown.updating }))
    if (breakdown.needs) parts.push(t('strip.needs', { count: breakdown.needs }))
  }
  const sub = [parts.join(', '), checked ? t('strip.lastCheck', { when: checked }) : ''].filter(Boolean).join('. ')
  const neverChecked = headline.kind === 'never'
  const icon = headline.kind === 'current' ? 'check' : headline.kind === 'updates' ? 'arrow-up' : (headline.kind === 'couldNot' || headline.kind === 'loadError') ? 'alert-circle' : 'clock'
  return (
    <section className="uc-strip" aria-label={t('strip.label')} data-testid="updates-strip" data-kind={headline.kind} aria-busy={checking || headline.kind === 'loading' || undefined}>
      <span className="uc-strip__tile" data-kind={headline.kind}><Icon name={icon} /></span>
      <div className="uc-strip__text">
        <p className="uc-strip__title" data-testid="updates-headline">{checking ? t('strip.checking') : headlineText(headline, t)}</p>
        {sub && <p className="uc-strip__sub" data-testid="updates-breakdown">{sub}</p>}
        <p className="uc-strip__result" role="status" data-testid="updates-check-result">{result}</p>
      </div>
      <div className="uc-strip__acts">
        <Button
          icon="refresh"
          variant={neverChecked ? 'primary' : 'secondary'}
          busy={checking}
          disabled={checking || cooling}
          onClick={onCheck}
          data-testid="updates-check-now"
        >
          {cooling && !checking ? t('strip.checkedJustNow') : t('strip.checkNow')}
        </Button>
        {primary}
      </div>
    </section>
  )
}

// The strip while an update run is shown: how many items, where they are, an
// overall bar, and what can be done (Cancel remaining while it runs; Retry
// failed and Dismiss once it ended). The run is the server's: closing the
// page does not stop it.
export function RunStrip({ run, busy, result, onCheck, checking, cooling, onCancel, onRetry, onDismiss }) {
  const { t } = useTranslation('updates')
  const when = useWhen()
  const counts = countsOf(run)
  const running = isRunning(run)
  // The same count as the end notification of the run.
  const attention = attentionOf(run)
  const progress = runProgress(run)
  const retryable = retryableKeys(run).length
  let title
  let kind
  if (running) {
    title = t('run.title', { count: counts.total })
    kind = 'updates'
  } else if (attention > 0) {
    title = t('run.result.attention', { updated: counts.updated, count: attention })
    kind = 'attention'
  } else if (run.state === 'cancelled') {
    title = t('run.result.cancelled', { count: counts.updated })
    kind = 'cancelled'
  } else {
    title = t('run.result.done', { count: counts.updated })
    kind = 'current'
  }
  const parts = []
  if (running) {
    if (counts.updated) parts.push(t('run.count.updated', { count: counts.updated }))
    // A busy item runs, or the server checks it; an unconfirmed item that
    // no check could answer needs a look.
    const busy = busyCount(run)
    if (busy) parts.push(t('run.count.running', { count: busy }))
    if (counts.queued) parts.push(t('run.count.waiting', { count: counts.queued }))
    const settled = (run.items || []).filter(i => i.phase === 'unconfirmed' && !isBusy(i)).length
    const failed = counts.failed + counts.rolled_back + settled
    if (failed) parts.push(t('run.count.attention', { count: failed }))
  } else {
    parts.push(counts.unconfirmed > 0 ? t('run.result.unknown', { count: counts.unconfirmed }) : t('run.result.clean'))
  }
  const at = running ? run.created_at : (run.finished_at || run.created_at)
  const ago = at ? when(at, Date.now()) : ''
  const sub = [parts.join(', '), ago ? t(running ? 'run.started' : 'run.finished', { when: ago }) : ''].filter(Boolean).join('. ').replace(/\.\./g, '.')
  const icon = kind === 'current' ? 'check' : kind === 'attention' ? 'alert-circle' : kind === 'cancelled' ? 'close' : 'arrow-up'
  return (
    <section className="uc-strip" aria-label={t('strip.label')} data-testid="updates-strip" data-kind={`run-${kind}`} data-run={run.id}>
      <span className="uc-strip__tile" data-kind={kind}><Icon name={icon} /></span>
      <div className="uc-strip__text">
        {run.source === 'auto' && <Tag icon="clock" className="uc-strip__source" testId="updates-run-auto">{t('run.auto')}</Tag>}
        <p className="uc-strip__title" data-testid="updates-headline">{title}</p>
        <p className="uc-strip__sub" data-testid="updates-breakdown">{sub}</p>
        <span
          className="dk-progress uc-strip__bar"
          role="progressbar"
          aria-label={t('run.progress')}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={progress}
          data-testid="updates-run-progress"
        >
          <span className="dk-progress-bar" style={cssVars({ '--dk-value': `${progress}%` })} />
        </span>
        <p className="uc-strip__result" role="status" data-testid="updates-check-result">{result}</p>
      </div>
      <div className="uc-strip__acts">
        {running && (
          <>
            <Button icon="refresh" busy={checking} disabled={checking || cooling} onClick={onCheck} data-testid="updates-check-now">
              {cooling && !checking ? t('strip.checkedJustNow') : t('strip.checkNow')}
            </Button>
            <Button icon="close" busy={busy === 'cancel'} disabled={Boolean(busy)} onClick={onCancel} data-testid="updates-cancel-run">{t('run.cancelRemaining')}</Button>
          </>
        )}
        {!running && retryable > 0 && (
          <Button variant="primary" icon="refresh" busy={busy === 'retry'} disabled={Boolean(busy)} onClick={onRetry} data-testid="updates-retry-failed">{t('run.retryFailed')}</Button>
        )}
        {!running && (
          <Button variant="ghost" busy={busy === 'dismiss'} disabled={Boolean(busy)} onClick={onDismiss} data-testid="updates-dismiss-run">{t('run.dismiss')}</Button>
        )}
      </div>
    </section>
  )
}

// One line per source (a dot, the name, what happened) and the automatic
// settings as read-only facts with a link to Settings. A failed source also
// gets a callout that says what is missing from the list.
export function UpdateSources({ sources, auto, onCheck, checking, installsDisabled }) {
  const { t } = useTranslation('updates')
  const { t: ta } = useTranslation('addons')
  const when = useWhen()
  const now = Date.now()
  // A backend source reports the registry's own words; the add-on source
  // reports a class that the add-on pages already have words for.
  const errorOf = (s) => (s.id === 'addons' ? ta(`updates.class.${s.error}`, { defaultValue: t('source.addonsUnreadable') }) : (s.error || ''))
  const failed = sources.filter(s => s.state === 'failed')
  return (
    <>
      <ul className="uc-sources" aria-label={t('source.label')} data-testid="updates-sources">
        {sources.map(s => {
          let text
          if (s.state === 'ok') text = s.checkedAt ? t('source.checked', { when: when(s.checkedAt, now) }) : t('source.ok')
          else if (s.state === 'never') text = s.loading ? t('source.loading') : t('source.never')
          else if (s.state === 'disabled') text = s.reason && s.id !== 'addons' ? t('source.unavailableWhy', { reason: s.reason }) : t('source.unavailable')
          else text = t('source.failed', { error: errorOf(s) })
          return (
            <li key={s.id} className="uc-source" data-source={s.id} data-state={s.state}>
              <span className="uc-source__dot" aria-hidden="true" />
              <span className="uc-source__name">{t(`source.name.${s.id}`)}</span>
              <span className="uc-source__text">{text}</span>
            </li>
          )
        })}
      </ul>
      <p className="uc-auto" data-testid="updates-auto">
        <span>{t('auto.backends')} <strong data-testid="auto-backends">{auto.backends ? t('auto.on') : t('auto.off')}</strong></span>
        <span>{t('auto.addons')} <strong data-testid="auto-addons">{auto.addons ? t('auto.on') : t('auto.off')}</strong></span>
        <Link className="uc-auto__link" to="/app/settings">{t('auto.settings')}</Link>
      </p>
      {installsDisabled && (
        <Callout tone="warn" testId="updates-installs-disabled" title={t('disabled.title')}>
          {t('disabled.body')}
        </Callout>
      )}
      {failed.map(s => (
        <Callout
          key={s.id}
          tone="error"
          role="alert"
          testId={`updates-source-failed-${s.id}`}
          title={t(`failedSource.title.${s.id}`)}
          actions={<Button size="sm" icon="refresh" disabled={checking} onClick={onCheck}>{t('failedSource.again')}</Button>}
        >
          {s.stale && (s.lastSuccessAt || s.checkedAt)
            ? t('failedSource.lastGood', { when: when(s.lastSuccessAt || s.checkedAt, now), error: errorOf(s) })
            : t('failedSource.noGood', { error: errorOf(s) })}
        </Callout>
      ))}
    </>
  )
}
