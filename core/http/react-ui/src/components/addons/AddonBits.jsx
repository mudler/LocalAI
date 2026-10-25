import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { stateChip } from './addonState'
import { installSteps } from './installSteps'
import './addons.css'

// The process state of an add-on as a chip. The colour is never the only
// signal: the text says it and the label names what it is the state of.
export function StateChip({ app }) {
  const { t } = useTranslation('addons')
  const chip = stateChip(app)
  const text = t(`state.${chip.key}`)
  return (
    <span className="ad-chip" data-tone={chip.tone} data-state={chip.key} role="img" aria-label={t('state.label', { state: text })}>
      <span className="ad-chip__dot" aria-hidden="true" />
      <span aria-hidden="true">{text}</span>
    </span>
  )
}

// The mark of an update the owner's check found. It is a separate chip from the
// process state: an add-on can be running and have an update pending.
export function UpdateChip() {
  const { t } = useTranslation('addons')
  const text = t('state.update-pending')
  return (
    <span className="ad-chip" data-tone="warn" data-state="update-pending" data-testid="update-chip" role="img" aria-label={t('state.label', { state: text })}>
      <span className="ad-chip__dot" aria-hidden="true" />
      <span aria-hidden="true">{text}</span>
    </span>
  )
}

// The icon of an add-on from the host's own icon route, or a placeholder.
// Never a remote address: the server sends only a same-origin path.
export function AppIcon({ src, size = 40 }) {
  if (src && src.startsWith('/')) {
    return <img className="ad-icon" src={src} alt="" width={size} height={size} loading="lazy" decoding="async" />
  }
  return (
    <span className="ad-icon ad-icon--empty" aria-hidden="true">
      <Icon name="puzzle" />
    </span>
  )
}

// A small fact chip: platform, signature policy and so on.
export function Fact({ icon, children, tone }) {
  return (
    <span className="ad-fact" data-tone={tone}>
      {icon && <Icon name={icon} aria-hidden="true" />}
      {children}
    </span>
  )
}

// An action whose outcome the owner has not confirmed. The earlier operation
// may still be applied, so the page offers to read it again (never to submit it
// again) or to dismiss it; no other action on the add-on is sent until then.
export function UnknownNotice({ addonId, actions, className = 'ad-entry__note' }) {
  const { t } = useTranslation('addons')
  const key = actions.unknown[addonId]?.key
  const checking = key ? actions.state[key]?.phase === 'working' : false
  return (
    <div className={`${className} ad-unknown`} data-tone="error" role="alert" data-testid="unknown-outcome">
      <span>{t('actions.unknown')}</span>
      <span className="ad-unknown__acts">
        <button type="button" className="dk-btn dk-btn--sm" disabled={checking} onClick={() => actions.checkStatus(addonId)}>
          <Icon name="refresh" /> {t('actions.checkStatus')}
        </button>
        <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" disabled={checking} onClick={() => actions.dismiss(addonId)}>
          {t('actions.dismiss')}
        </button>
      </span>
    </div>
  )
}

// The steps of a backend install and where the job is among them. The host
// names the step; the page marks the ones before it as left, the one it is in
// as running and the one that failed as failed. The text says it as well as
// the mark does.
export function InstallSteps({ job, signed }) {
  const { t } = useTranslation('addons')
  const steps = installSteps(job, { signed })
  return (
    <ol className="ad-steps" data-testid="install-steps" aria-label={t('install.stepsLabel')}>
      {steps.map(step => (
        <li key={step.id} className="ad-steps__item" data-step={step.id} data-state={step.state}>
          <Icon name={step.state === 'done' ? 'check-circle' : step.state === 'failed' ? 'alert-circle' : step.state === 'active' ? 'spinner' : 'circle'} spin={step.state === 'active'} />
          <span>{t(`install.step.${step.id}`)}</span>
          {step.progress !== undefined && <span className="ad-steps__pct dk-mono">{step.progress}%</span>}
          <span className="dk-sr-only">{t(`install.state.${step.state}`)}</span>
        </li>
      ))}
    </ol>
  )
}
