import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import StatusChip from '../ui/StatusChip'
import Button from '../ui/Button'
import { addedByEntry, hueOf, monogram, stateChip, versionLabel } from './addonState'
import { installSteps } from './installSteps'
import './addons.css'

// The process state of an add-on as a chip: one state, in words, with the
// tone and the glyph of the shared vocabulary. The colour is never the only
// signal, and the label names what it is the state of.
const CHIP_STATUS = { running: 'running', starting: 'starting', failed: 'failed', stopped: 'stopped', 'update-pending': 'update-available' }

export function StateChip({ app, className = '' }) {
  const { t } = useTranslation('addons')
  const chip = stateChip(app)
  const text = t(`state.${chip.key}`)
  return (
    <span className={`ad-state ${className}`.trim()} data-tone={chip.tone} data-state={chip.key} role="img" aria-label={t('state.label', { state: text })}>
      <StatusChip status={CHIP_STATUS[chip.key]} label={text} dot />
    </span>
  )
}

// The mark of an update the owner's check found. It is a separate chip from the
// process state: an add-on can be running and have an update pending. With the
// version known (administrators), the chip names it.
export function UpdateChip({ version }) {
  const { t } = useTranslation('addons')
  const text = version ? t('state.updateTo', { version }) : t('state.update-pending')
  return (
    <span className="ad-state" data-tone="warn" data-state="update-pending" data-testid="update-chip" role="img" aria-label={t('state.label', { state: text })}>
      <StatusChip status="update-available" label={text} />
    </span>
  )
}

// The identity tile of an add-on: its icon from the host's own icon route, or
// its first letter on a colour of its own (a hue from its id), so two add-ons
// in the sidebar or the list never look the same. Never a remote address: the
// server sends only a same-origin path.
export function AppIcon({ src, id, name, size = 36 }) {
  if (src && src.startsWith('/')) {
    return <img className="ad-icon" data-size={size} src={src} alt="" width={size} height={size} loading="lazy" decoding="async" />
  }
  return (
    <span className="ad-icon ad-icon--mark" data-size={size} data-hue={hueOf(id || name)} aria-hidden="true">
      {monogram(name || id)}
    </span>
  )
}

// A built-in page add-on: its own glyph on the neutral tile.
export function BuiltinIcon({ name, size = 36 }) {
  return <span className="ad-icon ad-icon--glyph" data-size={size} aria-hidden="true"><Icon name={name} /></span>
}

// A version as a meta item: "v1.2.0", or a quiet "Development build" chip.
export function VersionText({ version }) {
  const { t } = useTranslation('addons')
  const v = versionLabel(version)
  if (!v.text) return null
  if (v.dev) return <span className="ad-dev" title={v.text}>{t('meta.devBuild')}</span>
  return <span className="ad-version">{v.text}</span>
}

// What a library entry adds, before it is installed: "Adds a page" when it has
// a main page, "Adds N tools" when it declares chat tools. It shows nothing
// for an entry that says neither.
export function EntryAdds({ entry }) {
  const { t } = useTranslation('addons')
  const adds = addedByEntry(entry)
  if (!adds.page && adds.tools === 0) return null
  return (
    <p className="ad-meta" data-testid="entry-adds-line">
      {adds.page && <span className="ad-meta__item"><Icon name="file" aria-hidden="true" /> {t('card.addsPage')}</span>}
      {adds.tools > 0 && <span className="ad-meta__item" data-testid="entry-tools"><Icon name="wrench" aria-hidden="true" /> {t('card.addsTools', { count: adds.tools })}</span>}
    </p>
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
        <Button size="sm" icon="refresh" busy={checking} disabled={checking} onClick={() => actions.checkStatus(addonId)}>{t('actions.checkStatus')}</Button>
        <Button size="sm" variant="ghost" disabled={checking} onClick={() => actions.dismiss(addonId)}>{t('actions.dismiss')}</Button>
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
