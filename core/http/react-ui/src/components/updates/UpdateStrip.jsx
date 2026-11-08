/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Button, Callout } from '../ui'
import Icon from '../Icon'
import { useWhen } from '../../hooks/useWhen'

const SOURCE_KEYS = { gallery: 'gallery', library: 'library', addons: 'addons' }

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
