import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { stateChip } from './addonState'

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
