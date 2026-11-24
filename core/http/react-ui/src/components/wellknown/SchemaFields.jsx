/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { checkField } from '../../utils/wellKnown'
import BackgroundField from './BackgroundField'
import LookField from './LookField'
import StringsField from './StringsField'
import { AvatarField, EmptyChatField, TemplateField } from './IdentityFields'
import { StatusChip } from '../ui'
import { accentContrast } from './contrast'

// Common language tags for the language field. The value is free text in the
// file; a tag that is not here still shows, as its own option.
const LANGUAGES = ['en', 'it', 'es', 'de', 'fr', 'pt', 'pt-BR', 'nl', 'pl', 'sv', 'da', 'fi', 'cs', 'el', 'tr', 'ru', 'uk', 'ar', 'he', 'hi', 'id', 'ja', 'ko', 'zh-CN', 'zh-TW', 'vi', 'th']

function timeZones() {
  try { return Intl.supportedValuesOf('timeZone') } catch { return ['UTC'] }
}

function languageName(tag, locale) {
  try { return new Intl.DisplayNames([locale || 'en'], { type: 'language' }).of(tag) || tag } catch { return tag }
}

// ContrastBadge says whether an accent keeps the contrast of the page in the
// mode the theme value gives: "AA 5.5:1", or why the default shows instead.
function ContrastBadge({ accent, theme }) {
  const { t } = useTranslation('workspace')
  const c = accentContrast(accent, theme)
  if (!c) return null
  const mode = t(`wellKnown.contrast.mode.${c.mode}`)
  return c.ok
    ? <StatusChip tone="ok" icon="check" testId="wk-contrast-ok">{t('wellKnown.contrast.ok', { ratio: c.ratio })}</StatusChip>
    : <StatusChip tone="error" icon="alert-circle" testId="wk-contrast-fail" title={t('wellKnown.contrast.failLong', { ratio: c.ratio, mode })}>{t('wellKnown.contrast.fail', { ratio: c.ratio, mode })}</StatusChip>
}

// SchemaFields draws the form of a well-known file from the fields of its JSON
// Schema (fieldsFromSchema). One renderer serves every file, so the form
// cannot drift from the schema that validates the file and that the assistant
// reads. Labels and help come from the schema, translated when the namespace
// has a key for them.
//
//   name        the kind (persona, user, ui ...), for the translation keys
//   fields      from fieldsFromSchema
//   values      the edited values; onChange(key, value)
//   notes       key -> a line under the field ("From your account")
//   options     key -> [{value, label}] for a select-like field (models)
export default function SchemaFields({ name, fields, values, onChange, notes = {}, options = {}, disabled = false, idPrefix }) {
  const { t, i18n } = useTranslation('workspace')
  const zones = useMemo(() => (fields.some(f => f.widget === 'timezone') ? timeZones() : []), [fields])
  const pre = idPrefix || `wk-${name}`

  return fields.map(f => {
    const id = `${pre}-${f.key}`
    const value = values?.[f.key] ?? ''
    const label = t(`wellKnown.fields.${name}.${f.key}.label`, { defaultValue: f.label })
    const help = notes[f.key] || t(`wellKnown.fields.${name}.${f.key}.help`, { defaultValue: f.help })
    const problem = checkField(f, value)
    const describedBy = `${id}-hint${problem ? ` ${id}-err` : ''}`
    const common = {
      id, disabled, 'aria-describedby': describedBy, 'aria-invalid': problem ? 'true' : undefined,
      'data-testid': `wk-field-${name}-${f.key}`,
    }
    let control
    switch (f.widget) {
      case 'textarea':
        control = <textarea {...common} className="dk-input dk-textarea wk-textarea" value={value} maxLength={f.maxLength} onChange={e => onChange(f.key, e.target.value)} />
        break
      case 'checkbox':
        control = <input {...common} type="checkbox" className="dk-check" checked={!!values?.[f.key]} onChange={e => onChange(f.key, e.target.checked)} />
        break
      case 'select':
      case 'model':
      case 'language':
      case 'timezone': {
        let opts = options[f.key]
        if (!opts && f.widget === 'select') opts = (f.options || []).map(v => ({ value: v, label: t(`wellKnown.fields.${name}.${f.key}.options.${v}`, { defaultValue: v }) }))
        if (!opts && f.widget === 'language') opts = LANGUAGES.map(v => ({ value: v, label: `${languageName(v, i18n.language)} (${v})` }))
        if (!opts && f.widget === 'timezone') opts = zones.map(v => ({ value: v, label: v.replace(/_/g, ' ') }))
        opts = opts || []
        if (value && !opts.some(o => o.value === value)) opts = [{ value, label: f.widget === 'language' ? `${languageName(value, i18n.language)} (${value})` : String(value) }, ...opts]
        control = (
          <span className="dk-select-wrap">
            <select {...common} className="dk-select" value={value} onChange={e => onChange(f.key, e.target.value)}>
              <option value="">{t('wellKnown.notSet')}</option>
              {opts.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
            </select>
          </span>
        )
        break
      }
      case 'avatar':
        control = <AvatarField id={id} name={name} value={values?.[f.key]} assistantName={values?.name} disabled={disabled} describedBy={describedBy} onChange={v => onChange(f.key, v)} />
        break
      case 'template':
        control = <TemplateField id={id} name={name} fieldKey={f.key} value={values?.[f.key]} placeholders={f.placeholders} maxLength={f.maxLength} disabled={disabled} describedBy={describedBy} onChange={v => onChange(f.key, v)} />
        break
      case 'emptyChat':
        control = <EmptyChatField id={id} name={name} value={values?.[f.key]} properties={f.properties} disabled={disabled} describedBy={describedBy} onChange={v => onChange(f.key, v)} />
        break
      case 'look':
        control = <LookField id={id} name={name} value={values?.[f.key]} disabled={disabled} describedBy={describedBy} onChange={v => onChange(f.key, v)} />
        break
      case 'strings':
        control = <StringsField id={id} name={name} value={values?.[f.key]} disabled={disabled} describedBy={describedBy} onChange={v => onChange(f.key, v)} />
        break
      case 'background':
        control = <BackgroundField id={id} name={name} bgKey={f.key} value={values?.[f.key]} disabled={disabled} describedBy={describedBy} onChange={v => onChange(f.key, v)} />
        break
      case 'color':
        control = (
          <span className="wk-color">
            <input type="color" className="wk-color__swatch" aria-label={label} value={/^#[0-9a-fA-F]{6}$/.test(value) ? value : '#4a5aa0'} disabled={disabled} onChange={e => onChange(f.key, e.target.value)} />
            <input {...common} type="text" className="dk-input dk-mono" value={value} maxLength={7} placeholder="#4a5aa0" onChange={e => onChange(f.key, e.target.value)} />
            {f.contrast && <ContrastBadge accent={value} theme={values?.theme} />}
          </span>
        )
        break
      default:
        control = <input {...common} type="text" className="dk-input" value={value} maxLength={f.maxLength} onChange={e => onChange(f.key, e.target.value)} />
    }
    // The widgets that draw their own errors (a placeholder, a path) say
    // more than the line under the field.
    const ownErrors = ['template', 'avatar', 'emptyChat', 'strings'].includes(f.widget)
    return (
      <div key={f.key} className={`dk-field wk-field${f.widget === 'checkbox' ? ' wk-field--check' : ''}`}>
        <label className="dk-label" htmlFor={id}>{label}</label>
        {control}
        <p className="dk-hint" id={`${id}-hint`}>{help}</p>
        {problem && !ownErrors && <p className="dk-field-error" id={`${id}-err`}>{t(`wellKnown.check.${problem}`, { max: f.maxLength })}</p>}
      </div>
    )
  })
}
