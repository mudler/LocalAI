// The load history a node list row draws: the recent VRAM use of a GPU node, or
// CPU use of one without a GPU, as percentages, oldest first. The controller
// keeps the last readings in memory and sends them as `load_history`; a worker
// that has not reported yet sends nothing. Fewer than two readings make no
// line, so nothing here ever invents a trend.
export function loadSeries(node) {
  const raw = Array.isArray(node?.load_history) ? node.load_history : []
  const series = raw.filter(value => typeof value === 'number' && Number.isFinite(value)).map(value => Math.min(100, Math.max(0, value)))
  return series.length >= 2 ? series : []
}

// Polyline points for a series in a width x height box. The vertical scale
// fits the readings but never spans less than 20 percentage points, so a node
// that barely moves draws a nearly flat line rather than a dramatic one. The
// exact latest value is in the line's accessible name.
export function sparkPoints(series, width, height, pad = 2) {
  if (series.length < 2) return ''
  const low = Math.min(...series)
  const span = Math.max(Math.max(...series) - low, 20)
  const step = (width - pad * 2) / (series.length - 1)
  return series
    .map((value, index) => `${(pad + index * step).toFixed(1)},${(height - pad - ((value - low) / span) * (height - pad * 2)).toFixed(1)}`)
    .join(' ')
}
