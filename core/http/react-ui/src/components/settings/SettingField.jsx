/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import SearchableModelSelect from '../SearchableModelSelect'
import Icon from '../Icon'
import { CAP_CHAT } from '../../utils/capabilities'
import {
  FIELD_BY_KEY, getValue, setValue, isChanged, hasDefault, groupOf, OLD_SECTIONS,
} from '../../utils/settingsSchema'
import { displayValue } from '../../utils/settingsPending'

// What applying the field does, in words. Only a field the code speaks about
// has a hint (see settingsSchema.js); the others say nothing.
function ApplyHint({ field }) {
  if (field.apply === 'restart') {
    return <span className="st-tag" data-tone="warn" title="This setting is read when LocalAI starts.">Needs restart</span>
  }
  if (field.apply === 'live') {
    return <span className="st-tag" title={field.note || 'The server applies this when you apply.'}>Applies now</span>
  }
  return null
}

function coerceInt(field, text) {
  const floor = field.min ?? 0
  return Math.max(floor, parseInt(text, 10) || floor)
}

function Control({ field, settings, onChange, id, labelId, disabled, assets }) {
  const value = getValue(field, settings)
  const set = (v) => onChange(setValue(field, settings, v))

  switch (field.kind) {
    case 'bool':
      return (
        <button
          type="button" role="switch" id={id} className="dk-switch"
          aria-checked={!!value} aria-labelledby={labelId} disabled={disabled}
          onClick={() => set(!value)}
        />
      )
    case 'int':
      return (
        <input
          id={id} type="number" className="dk-input st-num" min={field.min} value={value}
          placeholder={field.placeholder} disabled={disabled}
          onChange={(e) => set(coerceInt(field, e.target.value))}
        />
      )
    case 'duration':
      return (
        <input
          id={id} className="dk-input dk-input--mono st-short" value={value} placeholder={field.placeholder}
          disabled={disabled} spellCheck={false} autoComplete="off"
          onChange={(e) => set(e.target.value)}
        />
      )
    case 'percent': {
      const pct = Math.round(Number(value) * 100)
      return (
        <div className="dk-range-wrap st-range">
          <input
            id={id} type="range" className="dk-range" min={field.min} max={field.max} value={pct} disabled={disabled}
            style={{ '--dk-fill': `${((pct - field.min) / (field.max - field.min)) * 100}%` }}
            onChange={(e) => set(parseInt(e.target.value, 10) / 100)}
          />
          <output className="dk-range-value" htmlFor={id}>{pct}%</output>
        </div>
      )
    }
    case 'select':
      return (
        <div className="dk-select-wrap st-short">
          <select id={id} className="dk-select" value={value} disabled={disabled} onChange={(e) => set(e.target.value)}>
            {field.options.map(o => <option key={o} value={o}>{o}</option>)}
          </select>
        </div>
      )
    case 'model':
      return (
        <div className="st-model">
          <SearchableModelSelect
            value={value} onChange={set} placeholder={field.placeholder}
            capability={field.key === 'agent_pool_default_model' ? CAP_CHAT : undefined}
          />
        </div>
      )
    case 'json':
    case 'lines':
      return (
        <textarea
          id={id} className="dk-textarea dk-input--mono st-area" rows={4} value={value}
          placeholder={field.placeholder} spellCheck={false} disabled={disabled}
          autoComplete={field.sensitive ? 'off' : undefined}
          onChange={(e) => set(e.target.value)}
        />
      )
    case 'token':
      return (
        <div className="st-token">
          <input
            id={id} className="dk-input dk-input--mono" value={value} placeholder="No token set"
            autoComplete="off" spellCheck={false} onChange={(e) => set(e.target.value)}
          />
          <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" title="Generate a new P2P token (applied when you apply)" onClick={() => set('0')}>
            <Icon name="refresh" /> Generate
          </button>
          {value && (
            <button type="button" className="dk-btn dk-btn--ghost dk-btn--icon dk-btn--sm" aria-label="Clear token" title="Clear token (stops P2P when you apply)" onClick={() => set('')}>
              <Icon name="close" />
            </button>
          )}
        </div>
      )
    case 'asset': {
      const a = assets.state(field.asset)
      return (
        <div className="st-asset">
          <span className="st-asset__preview">
            {a.url ? <img src={a.url} alt="" /> : <Icon name="image" />}
          </span>
          <label className="dk-btn dk-btn--secondary dk-btn--sm st-upload" aria-busy={a.busy || undefined}>
            <Icon name="upload" /> {a.busy ? 'Uploading…' : 'Upload'}
            <input
              type="file" className="dk-sr-only" disabled={a.busy} aria-label={`Upload ${field.label}`}
              accept="image/png,image/jpeg,image/svg+xml,image/webp,image/x-icon,.ico"
              onChange={(e) => {
                const file = e.target.files?.[0]
                e.target.value = ''
                if (file) assets.upload(field.asset, file)
              }}
            />
          </label>
          {a.custom && (
            <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" disabled={a.busy} title="Revert to bundled default" onClick={() => assets.reset(field.asset)}>
              <Icon name="undo" /> Reset
            </button>
          )}
        </div>
      )
    }
    default:
      return (
        <input
          id={id} className={`dk-input${field.key === 'vram_budget' ? ' dk-input--mono' : ''} st-text`} value={value}
          placeholder={field.placeholder} disabled={disabled} autoComplete={field.sensitive ? 'off' : undefined}
          onChange={(e) => set(e.target.value)}
        />
      )
  }
}

// One setting: its name and words on the left, its control on the right, and
// under the words what is worth knowing (where it was, its key, when it
// applies, whether it differs from the default and how to put that back).
export default function SettingField({ field, settings, onChange, assets, showWhere, pending }) {
  const id = `st-${field.key.replace(/[^a-z0-9_]/gi, '-')}`
  const labelId = `${id}-label`
  const needsOn = !field.needs || !!getValue(FIELD_BY_KEY[field.needs], settings)
  const needsValue = !field.needsValue || getValue(FIELD_BY_KEY[field.needsValue[0]], settings) === field.needsValue[1]
  const disabled = !needsOn || !needsValue
  const changed = isChanged(field, settings)
  const wide = field.kind === 'json' || field.kind === 'lines'
  const where = groupOf(field.group)?.label
  const was = OLD_SECTIONS[field.was]

  return (
    <div className="st-row" data-wide={wide || undefined} data-bool={field.kind === 'bool' || undefined} data-changed={changed || undefined} data-draft={pending || undefined} data-disabled={disabled || undefined} data-field={field.key}>
      <div className="st-row__main">
        <div className="st-row__title">
          <label id={labelId} htmlFor={field.kind === 'bool' ? undefined : id} className="st-row__label">{field.label}</label>
          {pending && <span className="st-tag" data-tone="draft">Draft</span>}
          {changed && <span className="st-tag" data-tone="changed">Changed</span>}
        </div>
        <p className="st-row__desc">{field.desc}</p>
        <div className="st-row__meta">
          {showWhere && <span className="st-where">{where}{was && was !== where ? <> · was {was}</> : null}</span>}
          {!field.asset && <code className="st-key">{field.key}</code>}
          <ApplyHint field={field} />
          {changed && hasDefault(field) && (
            <span className="st-default">
              default <span className="dk-mono">{displayValue(field, field.default)}</span>
              {' · '}
              <button type="button" className="st-reset" onClick={() => onChange(setValue(field, settings, field.default))}>Reset</button>
            </span>
          )}
          {disabled && <span className="st-note">Needs the setting above it</span>}
        </div>
      </div>
      <div className="st-row__control" data-kind={field.kind}>
        <Control field={field} settings={settings} onChange={onChange} id={id} labelId={labelId} disabled={disabled} assets={assets} />
      </div>
    </div>
  )
}
