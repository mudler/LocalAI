// Typed field editors: one input per column type, with the "empty" switch of
// an optional column. The state of an editor is { text, isNull }; the codecs
// turn it into the wire value.
import { browserDatetime } from './lib/codecs.js'

export function createFields(ctx, ui) {
  const { h, t } = ctx
  const { Field } = ui

  function typeLabel(type) {
    return t(`type.${type}`)
  }

  function FieldEditor({ column, state, onChange, id, error, autoFocus, compact }) {
    const set = (patch) => onChange({ ...state, ...patch })
    const disabled = state.isNull
    const label = compact ? column.name : `${column.name}`
    let control
    switch (column.type) {
      case 'boolean':
        control = (
          <select id={id} className="tbl-input" value={state.text} disabled={disabled} autoFocus={autoFocus} onChange={(e) => set({ text: e.target.value })}>
            {column.required ? null : <option value="">{t('field.notSet')}</option>}
            <option value="true">{t('common.yes')}</option>
            <option value="false">{t('common.no')}</option>
          </select>
        )
        break
      case 'datetime':
        control = (
          <span className="tbl-inline">
            <input id={id} className="tbl-input" type="datetime-local" step="1" value={browserDatetime(state.text)} disabled={disabled} autoFocus={autoFocus} onChange={(e) => set({ text: e.target.value })} />
            <span className="tbl-unit">{t('field.utc')}</span>
          </span>
        )
        break
      case 'integer':
      case 'real':
        control = <input id={id} className="tbl-input tbl-input--num" type="text" inputMode={column.type === 'integer' ? 'numeric' : 'decimal'} autoComplete="off" value={state.text} disabled={disabled} autoFocus={autoFocus} onChange={(e) => set({ text: e.target.value })} />
        break
      default:
        control = (state.text.length > 120 || state.text.includes('\n'))
          ? <textarea id={id} className="tbl-input tbl-input--area" rows={4} value={state.text} disabled={disabled} autoFocus={autoFocus} onChange={(e) => set({ text: e.target.value })} />
          : <input id={id} className="tbl-input" type="text" autoComplete="off" value={state.text} disabled={disabled} autoFocus={autoFocus} onChange={(e) => set({ text: e.target.value })} />
    }
    const hint = `${typeLabel(column.type)}${column.required ? ` · ${t('field.required')}` : ''}${column.unique ? ` · ${t('field.unique')}` : ''}`
    return (
      <Field label={label} hint={hint} error={error ? t(`field.error.${error}`) : ''} id={id}>
        {control}
        {column.required ? null : (
          <label className="tbl-check">
            <input type="checkbox" checked={state.isNull} onChange={(e) => set({ isNull: e.target.checked })} />
            <span>{t('field.null')}</span>
          </label>
        )}
      </Field>
    )
  }

  return { FieldEditor, typeLabel }
}
