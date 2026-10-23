/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import Icon from '../components/Icon'
import LoadingSpinner from '../components/LoadingSpinner'
import CatalogEmpty from '../components/catalog/CatalogEmpty'
import StatusPill from '../components/StatusPill'

// Small wrappers over components the host already ships: an add-on gets the
// shape it needs and nothing else, so no host component, hook or context is
// reachable from its module. The SDK (sdk.js) re-wraps each once per instance.

const VARIANTS = new Set(['primary', 'secondary', 'ghost', 'danger'])

export function Button({ variant = 'secondary', size, icon, busy, children, type, disabled, onClick, title, ...rest }) {
  const classes = ['dk-btn', `dk-btn--${VARIANTS.has(variant) ? variant : 'secondary'}`, size === 'sm' && 'dk-btn--sm'].filter(Boolean).join(' ')
  return (
    <button
      type={type === 'submit' ? 'submit' : 'button'}
      className={classes}
      disabled={disabled || busy === true}
      aria-busy={busy === true ? 'true' : undefined}
      aria-label={rest['aria-label']}
      title={title}
      onClick={onClick}
    >
      {icon ? <Icon name={icon} /> : null}
      {children}
    </button>
  )
}

export function Spinner({ size }) {
  return <LoadingSpinner size={size === 'sm' || size === 'lg' ? size : 'md'} />
}

export function EmptyState({ icon, title, text, children }) {
  return <CatalogEmpty icon={icon} title={title} text={text}>{children}</CatalogEmpty>
}

const TONES = new Set(['healthy', 'warning', 'error', 'idle', 'loading'])
export function Badge({ tone = 'idle', children }) {
  return <StatusPill status={TONES.has(tone) ? tone : 'idle'} label={children} />
}

export function Stack({ gap = 'md', children }) {
  return <div className={`ad-stack ad-stack--${gap === 'sm' || gap === 'lg' ? gap : 'md'}`}>{children}</div>
}

export function Text({ muted, children }) {
  return <p className={muted ? 'ad-text ad-text--muted' : 'ad-text'}>{children}</p>
}

// Shared primitives and widgets that more than one add-on uses. They are the
// very components the core pages import: one copy, so a core page and an
// add-on page draw the same thing.
export { default as PageHeader } from '../components/PageHeader'
export { default as ConfirmDialog } from '../components/ConfirmDialog'
export { default as ErrorWithTraceLink } from '../components/ErrorWithTraceLink'
export { default as ModelSelector } from '../components/ModelSelector'
export { default as MediaInput } from '../components/biometrics/MediaInput'
export { default as WaveformPlayer } from '../components/audio/WaveformPlayer'
export { default as DistributionBars } from '../components/biometrics/DistributionBars'
export { default as EmbeddingInspector } from '../components/biometrics/EmbeddingInspector'
export { default as TabSwitch } from '../components/biometrics/TabSwitch'
