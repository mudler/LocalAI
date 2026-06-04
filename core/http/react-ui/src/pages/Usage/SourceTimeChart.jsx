import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

const TOP_N = 7
// Distinct, accessible-ish series colors that read on both light and dark themes.
const SERIES_COLORS = [
  'var(--color-primary)',
  'var(--color-success, #10b981)',
  'var(--color-warning, #f59e0b)',
  'var(--color-info, #3b82f6)',
  'var(--color-danger, #ef4444)',
  '#a855f7',
  '#ec4899',
]
const OTHER_COLOR = 'var(--color-text-muted, #94a3b8)'

function identityFor(bucket) {
  return bucket.api_key_id || bucket.source || 'unknown'
}

// buckets: UsageBucket[] from /api/auth/usage/sources (server-sorted ASC by bucket)
// selectedKey: 'web' | 'legacy' | api_key_id | null
// totals: SourceTotals (for the "Other (count)" legend label)
export default function SourceTimeChart({ buckets = [], selectedKey, totals }) {
  const { t } = useTranslation('admin')

  // Find the top-N identities by total tokens across the period.
  const topIds = useMemo(() => {
    const sums = new Map()
    for (const b of buckets) {
      const id = identityFor(b)
      sums.set(id, (sums.get(id) || 0) + (b.total_tokens || 0))
    }
    return [...sums.entries()]
      .sort((a, b) => b[1] - a[1])
      .slice(0, TOP_N)
      .map(([id]) => id)
  }, [buckets])

  const topSet = useMemo(() => new Set(topIds), [topIds])

  // Resolve a display label for an identity (api_key_id -> snapshotted name, or source name).
  const labelByIdentity = useMemo(() => {
    const m = new Map()
    for (const b of buckets) {
      const id = identityFor(b)
      if (m.has(id)) continue
      if (b.source === 'web')    { m.set(id, t('usage.sources.webUI')); continue }
      if (b.source === 'legacy') { m.set(id, t('usage.sources.legacy')); continue }
      m.set(id, b.api_key_name || b.api_key_id || id)
    }
    return m
  }, [buckets, t])

  // Build a dense per-bucket row, splitting top-N vs Other.
  const series = useMemo(() => {
    const byBucket = new Map()
    for (const b of buckets) {
      const id = identityFor(b)
      const seriesId = topSet.has(id) ? id : '__other__'
      const row = byBucket.get(b.bucket) || { bucket: b.bucket, total: 0 }
      row[seriesId] = (row[seriesId] || 0) + (b.total_tokens || 0)
      row.total += b.total_tokens || 0
      byBucket.set(b.bucket, row)
    }
    return [...byBucket.values()]
  }, [buckets, topSet])

  const max = useMemo(
    () => series.reduce((m, r) => Math.max(m, r.total), 0) || 1,
    [series]
  )

  const seriesIds = [...topIds, '__other__']
  const colorOf = (id) =>
    id === '__other__'
      ? OTHER_COLOR
      : SERIES_COLORS[topIds.indexOf(id) % SERIES_COLORS.length]

  const labelOfId = (id) => {
    if (id === '__other__') return null // computed inline (need count)
    return labelByIdentity.get(id) || id
  }

  const otherCount = Math.max(0, (totals?.by_key?.length || 0) - TOP_N)

  // SVG geometry: 24px wide per bar (2px gap), 100px tall, viewBox stretches with bar count.
  const barWidth = 20
  const barGap = 4
  const slotWidth = barWidth + barGap
  const height = 100
  const width = Math.max(series.length * slotWidth, 200)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-xs)' }}>
      <div style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--color-text-primary)' }}>
        {t('usage.sources.topSources')}
      </div>

      <svg
        viewBox={`0 0 ${width} ${height}`}
        preserveAspectRatio="none"
        style={{ width: '100%', height: 160, display: 'block' }}
        aria-hidden
      >
        {series.map((row, i) => {
          let y = height
          return (
            <g key={row.bucket} transform={`translate(${i * slotWidth}, 0)`}>
              {seriesIds.map(id => {
                const v = row[id] || 0
                if (!v) return null
                const h = (v / max) * height
                y -= h
                const dim = selectedKey && selectedKey !== id ? 0.25 : 1
                const title = id === '__other__'
                  ? t('usage.sources.other', { count: otherCount })
                  : labelOfId(id)
                return (
                  <rect
                    key={id}
                    x={barGap / 2} y={y}
                    width={barWidth} height={h}
                    fill={colorOf(id)} opacity={dim}
                  >
                    <title>{`${row.bucket} - ${title}: ${v.toLocaleString()}`}</title>
                  </rect>
                )
              })}
            </g>
          )
        })}
      </svg>

      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--spacing-sm)', fontSize: '0.75rem' }}>
        {seriesIds.map(id => (
          <span key={id} style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
            <span style={{ width: 10, height: 10, borderRadius: 2, background: colorOf(id), display: 'inline-block' }} aria-hidden />
            {id === '__other__'
              ? t('usage.sources.other', { count: otherCount })
              : labelOfId(id)}
          </span>
        ))}
      </div>
    </div>
  )
}
