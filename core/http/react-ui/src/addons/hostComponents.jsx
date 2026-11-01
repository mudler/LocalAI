/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import Icon from '../components/Icon'
import LoadingSpinner from '../components/LoadingSpinner'
import CatalogEmpty from '../components/catalog/CatalogEmpty'
import StatusPill from '../components/StatusPill'
import { useId } from 'react'
import HostSwitch from '../components/ui/Switch'
import HostCallout from '../components/ui/Callout'
import HostStatusChip from '../components/ui/StatusChip'

// Small wrappers over components the host already ships: an add-on gets the
// shape it needs and nothing else, so no host component, hook or context is
// reachable from its module. The SDK (sdk.js) re-wraps each once per instance.

const VARIANTS = new Set(['primary', 'secondary', 'ghost', 'danger'])

export function Button({ variant = 'secondary', size, icon, busy, children, type, disabled, onClick, title, testId, ...rest }) {
  const classes = ['dk-btn', `dk-btn--${VARIANTS.has(variant) ? variant : 'secondary'}`, size === 'sm' && 'dk-btn--sm'].filter(Boolean).join(' ')
  return (
    <button
      type={type === 'submit' ? 'submit' : 'button'}
      className={classes}
      disabled={disabled || busy === true}
      aria-busy={busy === true ? 'true' : undefined}
      aria-label={rest['aria-label']}
      title={title}
      data-testid={testId}
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

// ---- The settings kit -------------------------------------------------------
// What an add-on needs to draw a settings section or a form the way the host
// pages draw theirs: a titled section, a labelled field with its unit and help
// line, a select, a text input, a switch, a list of choices with a line of
// explanation each, rows with a status chip and collapsed details, a callout,
// a monospace block, a table and a link out. They use only the host's classes
// and tokens (shell.css), so an add-on never needs a style sheet to look like
// the host, in both themes and at phone width.

const CHIP_TONES = new Set(['ok', 'warn', 'error', 'idle', 'accent'])
const CALLOUT_TONES = new Set(['info', 'ok', 'warn', 'error'])
const text = (v) => (typeof v === 'string' || typeof v === 'number' ? v : null)

export function Section({ title, description, actions, children, testId }) {
  const id = useId()
  return (
    <section className="ad-kit-section" aria-labelledby={title ? id : undefined} data-testid={testId}>
      {(title || actions) && (
        <header className="ad-kit-section__head">
          <div className="ad-kit-section__words">
            {title && <h3 className="ad-kit-section__title" id={id}>{title}</h3>}
            {description && <p className="ad-kit-section__desc">{description}</p>}
          </div>
          {actions && <div className="ad-kit-actions">{actions}</div>}
        </header>
      )}
      <div className="ad-kit-section__body">{children}</div>
    </section>
  )
}

// A label, the control, its unit and a help line. The label wraps the control,
// so the control is named by it.
export function Field({ label, help, unit, error, children, testId }) {
  return (
    <label className="ad-kit-field" data-testid={testId}>
      <span className="ad-kit-field__label">{label}</span>
      <span className="ad-kit-field__control">
        {children}
        {unit && <span className="ad-kit-field__unit">{unit}</span>}
      </span>
      {help && <span className="ad-kit-field__help">{help}</span>}
      {error && <span className="ad-kit-field__error" role="alert">{error}</span>}
    </label>
  )
}

export function Select({ value, onChange, options = [], disabled, testId, label }) {
  return (
    <select
      className="dk-select ad-kit-select"
      value={value}
      disabled={disabled}
      aria-label={label}
      data-testid={testId}
      onChange={e => onChange?.(e.target.value)}
    >
      {options.map(o => <option key={o.value} value={o.value} disabled={o.disabled}>{o.label}</option>)}
    </select>
  )
}

const INPUT_TYPES = new Set(['text', 'password', 'number', 'url', 'search'])
export function TextInput({ value, onChange, type = 'text', placeholder, disabled, testId, label, autoComplete = 'off', min, max, mono }) {
  return (
    <input
      className={mono ? 'dk-input dk-input--mono ad-kit-input' : 'dk-input ad-kit-input'}
      type={INPUT_TYPES.has(type) ? type : 'text'}
      value={value ?? ''}
      placeholder={placeholder}
      disabled={disabled}
      aria-label={label}
      autoComplete={autoComplete}
      spellCheck={false}
      min={min}
      max={max}
      data-testid={testId}
      onChange={e => onChange?.(e.target.value)}
    />
  )
}

export function Switch({ checked, onChange, label, disabled, testId }) {
  return <HostSwitch checked={checked} onChange={onChange} label={label} disabled={disabled} testId={testId} />
}

// One choice out of a few, each with a line that says what it does. An option
// can be unavailable: it stays in the list, disabled, with its note.
// hideLegend keeps the legend for screen readers only, under a Section whose
// title already says it.
export function Choice({ legend, hideLegend, value, onChange, options = [], disabled, testId }) {
  const name = useId()
  return (
    <fieldset className="ad-kit-choice" data-testid={testId} disabled={disabled}>
      {legend && <legend className={hideLegend ? 'dk-sr-only' : 'ad-kit-field__label'}>{legend}</legend>}
      {options.map(o => (
        <label key={o.value} className="ad-kit-choice__option" data-value={o.value} data-selected={value === o.value || undefined} data-disabled={o.disabled || undefined}>
          <input type="radio" name={name} value={o.value} checked={value === o.value} disabled={o.disabled} onChange={() => onChange?.(o.value)} />
          <span className="ad-kit-choice__words">
            <span className="ad-kit-choice__label">{o.label}{o.chip && <> {o.chip}</>}</span>
            {o.description && <span className="ad-kit-choice__desc">{o.description}</span>}
          </span>
        </label>
      ))}
    </fieldset>
  )
}

export function Callout({ tone = 'info', title, actions, role, testId, children }) {
  return <HostCallout tone={CALLOUT_TONES.has(tone) ? tone : 'info'} title={title} actions={actions} role={role === 'alert' ? 'alert' : undefined} testId={testId}>{children}</HostCallout>
}

export function StatusChip({ tone = 'idle', children, testId, title }) {
  return <HostStatusChip tone={CHIP_TONES.has(tone) ? tone : 'idle'} testId={testId} title={text(title) ?? undefined}>{children}</HostStatusChip>
}

// Rows: a name and a line under it on the left, a status chip on the right,
// and details collapsed under a "Details" toggle.
export function List({ label, children, testId }) {
  return <ul className="ad-kit-list" aria-label={label} data-testid={testId}>{children}</ul>
}

export function ListRow({ title, meta, status, details, detailsLabel = 'Details', children, testId }) {
  return (
    <li className="ad-kit-row" data-testid={testId}>
      <div className="ad-kit-row__main">
        <div className="ad-kit-row__head">
          <span className="ad-kit-row__title">{title}</span>
          {status && <span className="ad-kit-row__status">{status}</span>}
        </div>
        {meta && <div className="ad-kit-row__meta">{meta}</div>}
        {children}
        {details && (
          <details className="ad-kit-details">
            <summary className="ad-kit-details__summary">{detailsLabel}</summary>
            <div className="ad-kit-details__body">{details}</div>
          </details>
        )}
      </div>
    </li>
  )
}

export function Code({ children, testId }) {
  return <pre className="ad-kit-code" data-testid={testId}>{children}</pre>
}

export function Actions({ children }) {
  return <div className="ad-kit-actions">{children}</div>
}

export function Table({ caption, columns = [], rows = [], testId }) {
  return (
    <div className="dk-table-wrap" data-testid={testId}>
      <table className="dk-table dk-table--compact">
        {caption && <caption className="dk-sr-only">{caption}</caption>}
        <thead><tr>{columns.map(c => <th key={c.key} scope="col">{c.label}</th>)}</tr></thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={r.key ?? i}>{columns.map(c => <td key={c.key}>{r[c.key]}</td>)}</tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

// A link out, to documentation or a provider's page: https only, in a new tab.
export function ExternalLink({ href, children, testId }) {
  const safe = typeof href === 'string' && /^https:\/\/[^\s"'<>]+$/.test(href) ? href : null
  if (!safe) return <span>{children}</span>
  return (
    <a className="ad-kit-link" href={safe} target="_blank" rel="noopener noreferrer" data-testid={testId}>
      {children}<Icon name="external-link" className="ad-kit-link__icon" />
    </a>
  )
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
