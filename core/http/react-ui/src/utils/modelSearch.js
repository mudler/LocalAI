// Helpers shared by the online sources of the Explore tab.

// 1234 -> "1.2k", 3400000 -> "3.4M". Small numbers stay as they are.
export function compactCount(n) {
  if (!Number.isFinite(n) || n <= 0) return ''
  if (n < 1000) return String(Math.round(n))
  const units = [[1e9, 'B'], [1e6, 'M'], [1e3, 'k']]
  for (const [size, suffix] of units) {
    if (n >= size) return `${parseFloat((n / size).toFixed(1))}${suffix}`
  }
  return String(n)
}

// ---- Sources that come from add-ons ----------------------------------------
// An add-on source declares typed filters. The host draws them; these helpers
// hold the rules, so the panel only renders what they return. Nothing here
// builds markup from add-on text: labels are plain strings.

// The kind of control of a filter, or '' for a type the host does not know.
export function filterControl(filter) {
  switch (filter?.type) {
    case 'enum': return (filter.values || []).length <= 4 ? 'segmented' : 'select'
    case 'bool': return 'switch'
    case 'range': return 'range'
    case 'text': return 'text'
    default: return ''
  }
}

// The text of a source or filter in a language: the translation of the
// manifest when it has one, else English. `src.i18n` is the manifest's
// translations by language tag.
export function sourceText(src, lang) {
  const tr = pickLang(src?.i18n, lang)
  return tr?.title || src?.title || src?.id || ''
}

export function filterText(src, filter, lang) {
  const tr = pickLang(src?.i18n, lang)?.filters?.[filter.id]
  return {
    label: tr?.label || filter.label || filter.id,
    help: tr?.help || filter.help || '',
    value: (v) => tr?.values?.[v] || v,
  }
}

function pickLang(map, lang) {
  if (!map || !lang) return null
  if (map[lang]) return map[lang]
  const base = String(lang).split('-')[0]
  const hit = Object.keys(map).find(k => k === base || k.startsWith(`${base}-`))
  return hit ? map[hit] : null
}

// The value a filter shows: what the person set, else the default the source
// declared, else an empty value of the type.
export function filterValue(filter, values) {
  const set = values?.[filter.id]
  if (set !== undefined) return set
  if (filter.default !== undefined && filter.default !== null) return filter.default
  switch (filter.type) {
    case 'bool': return false
    case 'range': return filter.min ?? 0
    default: return ''
  }
}

// The request parameters of the filters a person changed, as
// filters.<source>.<id>. A value left at the default is not sent, so the
// source applies its own default.
export function filterParams(sourceId, filters, values) {
  const out = {}
  for (const f of filters || []) {
    const v = values?.[f.id]
    if (v === undefined || v === '') continue
    out[`filters.${sourceId}.${f.id}`] = String(v)
  }
  return out
}

// The range of a range filter as the kit slider reads it.
export function sliderRange(filter) {
  const step = filter.step > 0 ? filter.step : 1
  return { min: filter.min ?? 0, max: filter.max ?? 100, step, integer: Number.isInteger(step) && Number.isInteger(filter.min ?? 0) }
}
