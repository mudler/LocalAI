// The rules of the kit Slider, pure so a node test drives them. A range is
// { min, max, step, integer, exclusiveMin }: `exclusiveMin` is set when the
// setting must be above `min`, so the slider starts one step up.

// Decimal places of a step: 0.05 is 2, 1 is 0.
const decimals = (step) => (String(step).split('.')[1] || '').length

// The value a slider holds for `raw` (a string of the form, '' when the model's
// own value applies): the number kept inside the slider's ends and on its
// step, or null when nothing is set.
export function sliderValue(range, raw) {
  if (String(raw ?? '').trim() === '') return null
  const n = Number(raw)
  if (!Number.isFinite(n)) return null
  const low = range.exclusiveMin ? range.step : range.min
  const kept = Math.min(range.max, Math.max(low, n))
  const snapped = Math.round((kept - low) / range.step) * range.step + low
  return Number(Math.min(range.max, snapped).toFixed(decimals(range.step)))
}

// The string a slider writes to the form for a position.
export function sliderText(range, position) {
  const n = Number(position)
  if (!Number.isFinite(n)) return ''
  return String(Number(n.toFixed(decimals(range.step))))
}

// Where the filled part of a slider ends, in percent of its track.
export function sliderFill(range, position) {
  if (position === null || position === undefined) return 0
  const low = range.exclusiveMin ? range.step : range.min
  return Math.round(((position - low) / (range.max - low)) * 1000) / 10
}
