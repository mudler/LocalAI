// The words of a setting: a translated label when there is one, else its key.
export function fieldLabel(t, item) {
  const key = `engineSettings.fields.${item.id.replace(/\./g, '_')}`
  const label = t(key, { defaultValue: '' })
  if (label) return label
  const base = item.option || item.key?.split('.').pop() || item.id
  return base.replace(/_/g, ' ').replace(/^./, c => c.toUpperCase())
}

// The words of a choice value: on, off and auto in the reader's language, a
// boolean as on or off, anything else (q8_0, 4096) as it is written.
export function choiceWords(t, v) {
  if (v === true || v === 'on') return t('engineSettings.on')
  if (v === false || v === 'off') return t('engineSettings.off')
  if (v === 'auto') return t('engineSettings.auto')
  return String(v)
}
