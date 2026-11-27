// The words of a setting: a translated label when there is one, else its key.
export function fieldLabel(t, item) {
  // A section that draws a setting in its own words (the Drafting modes).
  if (item.label) return item.label
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

// A text cut after its first sentence: [lead, rest], rest '' when there is
// one sentence. A sentence ends at ". ", "! " or "? " (so "0.6B" does not
// cut), but not after an abbreviation such as "e.g.", or at "。".
const ABBREV = /(?:^|\s)(?:e\.g|i\.e|etc|vs|approx)\.$/i
export function firstSentence(text) {
  const s = String(text ?? '').trim()
  const re = /[.!?]\s+(?=\S)/g
  let m
  while ((m = re.exec(s))) {
    const lead = s.slice(0, m.index + 1)
    if (!ABBREV.test(lead)) return [lead, s.slice(re.lastIndex).trim()]
  }
  const cjk = s.match(/^(.*?[。！？])(.+)$/su)
  return cjk ? [cjk[1], cjk[2].trim()] : [s, '']
}
