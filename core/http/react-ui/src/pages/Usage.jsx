import { useState, useEffect, useCallback, useRef, Fragment } from 'react'
import { useOutletContext } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'
import { apiUrl } from '../utils/basePath'
import LoadingSpinner from '../components/LoadingSpinner'

const PERIODS = [
  { key: 'day', label: 'Day' },
  { key: 'week', label: 'Week' },
  { key: 'month', label: 'Month' },
  { key: 'all', label: 'All' },
]

const TOTAL_BUCKETS = { day: 24, week: 7, month: 30 }
const HOURS_PER_BUCKET = { day: 1, week: 24, month: 24, all: 730 }

function formatNumber(n) {
  if (n == null) return '0'
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M'
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K'
  return String(n)
}

function StatCard({ icon, label, value, muted }) {
  return (
    <div className="card" style={{ padding: 'var(--spacing-sm) var(--spacing-md)', flex: '1 1 0', minWidth: 120, opacity: muted ? 0.7 : 1 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 2 }}>
        <i className={icon} style={{ color: 'var(--color-text-muted)', fontSize: '0.75rem' }} />
        <span style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)', fontWeight: 500, textTransform: 'uppercase', letterSpacing: '0.03em' }}>{label}</span>
      </div>
      <div style={{ fontSize: '1.375rem', fontWeight: 700, fontFamily: 'JetBrains Mono, monospace', color: muted ? 'var(--color-text-secondary)' : 'var(--color-text-primary)' }}>
        {muted ? '~' : ''}{formatNumber(value)}
      </div>
    </div>
  )
}

function UsageBar({ value, max }) {
  const pct = max > 0 ? Math.min((value / max) * 100, 100) : 0
  return (
    <div style={{
      width: '100%', height: 6, borderRadius: 3,
      background: 'var(--color-bg-primary)',
      overflow: 'hidden',
    }}>
      <div style={{
        width: `${pct}%`, height: '100%', borderRadius: 3,
        background: 'var(--color-primary)',
        transition: 'width 0.3s ease',
      }} />
    </div>
  )
}

function aggregateByModel(buckets) {
  const map = {}
  for (const b of buckets) {
    const key = b.model || '(unknown)'
    if (!map[key]) {
      map[key] = { model: key, prompt_tokens: 0, completion_tokens: 0, total_tokens: 0, request_count: 0 }
    }
    map[key].prompt_tokens += b.prompt_tokens
    map[key].completion_tokens += b.completion_tokens
    map[key].total_tokens += b.total_tokens
    map[key].request_count += b.request_count
  }
  return Object.values(map).sort((a, b) => b.total_tokens - a.total_tokens)
}

function aggregateByUser(buckets) {
  const map = {}
  for (const b of buckets) {
    const key = b.user_id || '(unknown)'
    if (!map[key]) {
      map[key] = { user_id: key, user_name: b.user_name || key, prompt_tokens: 0, completion_tokens: 0, total_tokens: 0, request_count: 0 }
    }
    map[key].prompt_tokens += b.prompt_tokens
    map[key].completion_tokens += b.completion_tokens
    map[key].total_tokens += b.total_tokens
    map[key].request_count += b.request_count
  }
  return Object.values(map).sort((a, b) => b.total_tokens - a.total_tokens)
}

function aggregateByBucket(buckets) {
  const map = {}
  for (const b of buckets) {
    if (!b.bucket) continue
    if (!map[b.bucket]) {
      map[b.bucket] = { bucket: b.bucket, prompt_tokens: 0, completion_tokens: 0, total_tokens: 0, request_count: 0 }
    }
    map[b.bucket].prompt_tokens += b.prompt_tokens
    map[b.bucket].completion_tokens += b.completion_tokens
    map[b.bucket].total_tokens += b.total_tokens
    map[b.bucket].request_count += b.request_count
  }
  return Object.values(map).sort((a, b) => a.bucket.localeCompare(b.bucket))
}

function aggregateByBucketForUser(buckets, userId) {
  return aggregateByBucket(buckets.filter(b => b.user_id === userId))
}

function generateUserPredictions(adminUsage, userRows, period) {
  const result = {}
  for (const u of userRows) {
    const ts = aggregateByBucketForUser(adminUsage, u.user_id)
    const preds = generatePredictions(ts, period)
    result[u.user_id] = { timeSeries: ts, predictions: preds }
  }
  return result
}

function formatBucket(bucket, period) {
  if (!bucket) return ''
  if (period === 'day') {
    return bucket.split(' ')[1] || bucket
  }
  if (period === 'week' || period === 'month') {
    const d = new Date(bucket + 'T00:00:00')
    if (!isNaN(d)) return d.toLocaleDateString('en-US', { month: 'short', day: 'numeric' })
    return bucket
  }
  const [y, m] = bucket.split('-')
  if (y && m) {
    const d = new Date(Number(y), Number(m) - 1)
    if (!isNaN(d)) return d.toLocaleDateString('en-US', { month: 'short', year: 'numeric' })
  }
  return bucket
}

function formatYLabel(n) {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M'
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K'
  return String(n)
}

// --- Prediction helpers ---

function linearRegression(values) {
  const n = values.length
  if (n < 2) return null
  let sumX = 0, sumY = 0, sumXY = 0, sumX2 = 0
  for (let i = 0; i < n; i++) {
    sumX += i
    sumY += values[i]
    sumXY += i * values[i]
    sumX2 += i * i
  }
  const denom = n * sumX2 - sumX * sumX
  if (denom === 0) return { slope: 0, intercept: sumY / n }
  const slope = (n * sumXY - sumX * sumY) / denom
  const intercept = (sumY - slope * sumX) / n
  return { slope, intercept }
}

function generateFutureBucketLabels(lastBucket, count, period) {
  const labels = []
  if (period === 'day') {
    // lastBucket like "2026-03-21 14:00"
    const parts = lastBucket.split(' ')
    const datePart = parts[0] || ''
    const hourStr = (parts[1] || '00:00').split(':')[0]
    let hour = parseInt(hourStr, 10)
    for (let i = 0; i < count; i++) {
      hour++
      if (hour >= 24) hour = 0
      labels.push(`${datePart} ${String(hour).padStart(2, '0')}:00`)
    }
  } else if (period === 'week' || period === 'month') {
    // lastBucket like "2026-03-21"
    const d = new Date(lastBucket + 'T00:00:00')
    for (let i = 0; i < count; i++) {
      d.setDate(d.getDate() + 1)
      const y = d.getFullYear()
      const m = String(d.getMonth() + 1).padStart(2, '0')
      const day = String(d.getDate()).padStart(2, '0')
      labels.push(`${y}-${m}-${day}`)
    }
  } else {
    // all: lastBucket like "2026-03"
    const [y, m] = lastBucket.split('-').map(Number)
    let year = y, month = m
    for (let i = 0; i < count; i++) {
      month++
      if (month > 12) { month = 1; year++ }
      labels.push(`${year}-${String(month).padStart(2, '0')}`)
    }
  }
  return labels
}

function generatePredictions(timeSeries, period) {
  if (!timeSeries || timeSeries.length < 2) return null

  const n = timeSeries.length
  const totalBuckets = TOTAL_BUCKETS[period]
  const remaining = totalBuckets ? Math.max(totalBuckets - n, 0) : 3 // 'all' gets 3 extra months
  if (remaining === 0) return null

  const metrics = ['prompt_tokens', 'completion_tokens', 'total_tokens', 'request_count']
  const regressions = {}
  for (const m of metrics) {
    regressions[m] = linearRegression(timeSeries.map(d => d[m]))
  }

  const lastBucket = timeSeries[n - 1].bucket
  const futureLabels = generateFutureBucketLabels(lastBucket, remaining, period)

  const predictedBuckets = futureLabels.map((label, i) => {
    const idx = n + i
    const entry = { bucket: label, predicted: true }
    for (const m of metrics) {
      const reg = regressions[m]
      entry[m] = reg ? Math.max(0, Math.round(reg.intercept + reg.slope * idx)) : 0
    }
    return entry
  })

  const existingTotals = {
    prompt_tokens: timeSeries.reduce((s, d) => s + d.prompt_tokens, 0),
    completion_tokens: timeSeries.reduce((s, d) => s + d.completion_tokens, 0),
    total_tokens: timeSeries.reduce((s, d) => s + d.total_tokens, 0),
    request_count: timeSeries.reduce((s, d) => s + d.request_count, 0),
  }
  const projectedTotals = { ...existingTotals }
  for (const b of predictedBuckets) {
    for (const m of metrics) {
      projectedTotals[m] += b[m]
    }
  }

  return { predictedBuckets, projectedTotals }
}

function formatDuration(hours) {
  if (!isFinite(hours) || hours < 0) return 'N/A'
  if (hours < 1) return '< 1 hour'
  if (hours < 48) return `~${Math.round(hours)} hours`
  const days = Math.round(hours / 24)
  if (days < 60) return `~${days} days`
  return `~${Math.round(days / 30)} months`
}

function computeQuotaExhaustion(quotas, timeSeries, period) {
  if (!quotas?.length || !timeSeries?.length) return []

  const totalTokens = timeSeries.reduce((s, b) => s + b.total_tokens, 0)
  const totalRequests = timeSeries.reduce((s, b) => s + b.request_count, 0)
  const bucketCount = timeSeries.length
  const hpb = HOURS_PER_BUCKET[period] || 24
  const tokensPerHour = bucketCount > 0 ? (totalTokens / bucketCount) / hpb : 0
  const requestsPerHour = bucketCount > 0 ? (totalRequests / bucketCount) / hpb : 0

  const results = []
  for (const q of quotas) {
    const items = []

    if (q.max_total_tokens != null) {
      const remaining = q.max_total_tokens - (q.current_tokens || 0)
      const hoursLeft = tokensPerHour > 0 ? remaining / tokensPerHour : Infinity
      const resetsAt = q.resets_at ? new Date(q.resets_at) : null
      const hoursUntilReset = resetsAt ? Math.max(0, (resetsAt - Date.now()) / 3600000) : Infinity
      items.push({
        label: 'Tokens',
        current: q.current_tokens || 0,
        max: q.max_total_tokens,
        hoursLeft: Math.min(hoursLeft, hoursUntilReset),
        withinLimits: hoursLeft >= hoursUntilReset,
      })
    }

    if (q.max_requests != null) {
      const remaining = q.max_requests - (q.current_requests || 0)
      const hoursLeft = requestsPerHour > 0 ? remaining / requestsPerHour : Infinity
      const resetsAt = q.resets_at ? new Date(q.resets_at) : null
      const hoursUntilReset = resetsAt ? Math.max(0, (resetsAt - Date.now()) / 3600000) : Infinity
      items.push({
        label: 'Requests',
        current: q.current_requests || 0,
        max: q.max_requests,
        hoursLeft: Math.min(hoursLeft, hoursUntilReset),
        withinLimits: hoursLeft >= hoursUntilReset,
      })
    }

    if (items.length > 0) {
      results.push({ model: q.model || 'All models', window: q.window, items })
    }
  }
  return results
}

// --- Components ---

function PredictionCards({ predictions, quotaExhaustion, period }) {
  if (!predictions) {
    return (
      <div className="card" style={{ padding: 'var(--spacing-md)', marginBottom: 'var(--spacing-md)', borderTop: '2px solid var(--color-primary)', borderTopLeftRadius: 0, borderTopRightRadius: 0, opacity: 0.6 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: '0.8125rem', color: 'var(--color-text-muted)' }}>
          <i className="fas fa-chart-line" />
          <span>Not enough data to predict trends (need at least 2 data points)</span>
        </div>
      </div>
    )
  }

  const { projectedTotals } = predictions
  const periodLabel = period === 'all' ? '(next 3 months)' : `end of ${period}`

  return (
    <div style={{ marginBottom: 'var(--spacing-md)' }}>
      <div className="card" style={{ padding: 'var(--spacing-md)', borderTop: '2px solid var(--color-primary)', borderTopLeftRadius: 0, borderTopRightRadius: 0 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 'var(--spacing-sm)' }}>
          <i className="fas fa-chart-line" style={{ color: 'var(--color-primary)', fontSize: '0.8125rem' }} />
          <span style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--color-text-primary)' }}>
            Projected {periodLabel}
          </span>
          <span style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)', fontStyle: 'italic' }}>
            based on linear trend
          </span>
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(120px, 1fr))', gap: 'var(--spacing-sm)' }}>
          <StatCard icon="fas fa-arrow-right-arrow-left" label="Proj. Requests" value={projectedTotals.request_count} muted />
          <StatCard icon="fas fa-arrow-up" label="Proj. Prompt" value={projectedTotals.prompt_tokens} muted />
          <StatCard icon="fas fa-arrow-down" label="Proj. Completion" value={projectedTotals.completion_tokens} muted />
          <StatCard icon="fas fa-coins" label="Proj. Total" value={projectedTotals.total_tokens} muted />
        </div>
      </div>

      {quotaExhaustion.length > 0 && (
        <div className="card" style={{ padding: 'var(--spacing-md)', marginTop: 'var(--spacing-sm)' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 'var(--spacing-sm)' }}>
            <i className="fas fa-gauge-high" style={{ color: 'var(--color-text-muted)', fontSize: '0.8125rem' }} />
            <span style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--color-text-primary)' }}>Quota forecast</span>
          </div>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-sm)' }}>
            {quotaExhaustion.map((q, qi) => (
              <div key={qi}>
                <div style={{ fontSize: '0.75rem', fontWeight: 600, color: 'var(--color-text-secondary)', marginBottom: 4 }}>
                  {q.model} <span style={{ fontWeight: 400, color: 'var(--color-text-muted)' }}>({q.window} window)</span>
                </div>
                {q.items.map((item, ii) => (
                  <div key={ii} style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', marginBottom: 4 }}>
                    <span style={{ minWidth: 70, fontSize: '0.75rem', color: 'var(--color-text-muted)', fontFamily: 'JetBrains Mono, monospace' }}>
                      {item.label}
                    </span>
                    <div style={{ flex: 1, maxWidth: 200 }}>
                      <UsageBar value={item.current} max={item.max} />
                    </div>
                    <span style={{ fontSize: '0.75rem', fontFamily: 'JetBrains Mono, monospace', color: 'var(--color-text-muted)', minWidth: 100 }}>
                      {formatNumber(item.current)}/{formatNumber(item.max)}
                    </span>
                    {item.withinLimits ? (
                      <span style={{ fontSize: '0.6875rem', color: 'var(--color-success, #22c55e)' }}>
                        <i className="fas fa-check" style={{ marginRight: 4 }} />Within limits
                      </span>
                    ) : (
                      <span style={{ fontSize: '0.6875rem', color: 'var(--color-warning, #f59e0b)' }}>
                        <i className="fas fa-exclamation-triangle" style={{ marginRight: 4 }} />{formatDuration(item.hoursLeft)} left
                      </span>
                    )}
                  </div>
                ))}
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

function UsageTimeChart({ data, predictedData, period }) {
  const containerRef = useRef(null)
  const [width, setWidth] = useState(600)
  const [tooltip, setTooltip] = useState(null)

  useEffect(() => {
    if (!containerRef.current) return
    const observer = new ResizeObserver(entries => {
      for (const entry of entries) {
        setWidth(entry.contentRect.width)
      }
    })
    observer.observe(containerRef.current)
    return () => observer.disconnect()
  }, [])

  if (!data || data.length === 0) return null

  const allData = predictedData ? [...data, ...predictedData] : data
  const actualCount = data.length

  const height = 200
  const margin = { top: 16, right: 16, bottom: 40, left: 56 }
  const chartW = width - margin.left - margin.right
  const chartH = height - margin.top - margin.bottom

  const maxVal = Math.max(...allData.map(d => d.total_tokens), 1)
  const barWidth = Math.max(Math.min(chartW / allData.length - 2, 40), 4)
  const barGap = (chartW - barWidth * allData.length) / (allData.length + 1)

  // Y-axis ticks (4 ticks)
  const ticks = [0, 1, 2, 3, 4].map(i => Math.round(maxVal * i / 4))

  return (
    <div className="card" style={{ padding: 'var(--spacing-md)', marginBottom: 'var(--spacing-md)' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--spacing-sm)' }}>
        <span style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--color-text-primary)' }}>Tokens over time</span>
        <div style={{ display: 'flex', gap: 'var(--spacing-md)', fontSize: '0.6875rem', color: 'var(--color-text-muted)' }}>
          <span><span style={{ display: 'inline-block', width: 8, height: 8, borderRadius: 2, background: 'var(--color-primary)', marginRight: 4, verticalAlign: 'middle' }} />Prompt</span>
          <span><span style={{ display: 'inline-block', width: 8, height: 8, borderRadius: 2, background: 'var(--color-primary)', opacity: 0.35, marginRight: 4, verticalAlign: 'middle' }} />Completion</span>
          {predictedData && predictedData.length > 0 && (
            <span>
              <span style={{
                display: 'inline-block', width: 8, height: 8, borderRadius: 2,
                border: '1.5px dashed var(--color-primary)', background: 'transparent',
                marginRight: 4, verticalAlign: 'middle', opacity: 0.6,
              }} />
              Predicted
            </span>
          )}
        </div>
      </div>
      <div ref={containerRef} style={{ position: 'relative', width: '100%' }}>
        <svg width={width} height={height} style={{ display: 'block' }}>
          <g transform={`translate(${margin.left},${margin.top})`}>
            {/* Grid lines and Y labels */}
            {ticks.map((t, i) => {
              const y = chartH - (t / maxVal) * chartH
              return (
                <g key={i}>
                  <line x1={0} y1={y} x2={chartW} y2={y} stroke="var(--color-border)" strokeOpacity={0.5} strokeDasharray={i === 0 ? 'none' : '3,3'} />
                  <text x={-8} y={y + 4} textAnchor="end" fontSize="10" fill="var(--color-text-muted)" fontFamily="JetBrains Mono, monospace">
                    {formatYLabel(t)}
                  </text>
                </g>
              )
            })}
            {/* Actual bars */}
            {data.map((d, i) => {
              const x = barGap + i * (barWidth + barGap)
              const promptH = (d.prompt_tokens / maxVal) * chartH
              const compH = (d.completion_tokens / maxVal) * chartH
              return (
                <g key={d.bucket}
                  onMouseEnter={(e) => {
                    const rect = containerRef.current.getBoundingClientRect()
                    setTooltip({
                      x: e.clientX - rect.left,
                      y: e.clientY - rect.top,
                      data: d,
                    })
                  }}
                  onMouseMove={(e) => {
                    const rect = containerRef.current.getBoundingClientRect()
                    setTooltip(prev => prev ? {
                      ...prev,
                      x: e.clientX - rect.left,
                      y: e.clientY - rect.top,
                    } : null)
                  }}
                  onMouseLeave={() => setTooltip(null)}
                  style={{ cursor: 'default' }}
                >
                  {/* Invisible hit area */}
                  <rect x={x} y={0} width={barWidth} height={chartH} fill="transparent" />
                  {/* Prompt tokens (bottom) */}
                  <rect x={x} y={chartH - promptH - compH} width={barWidth} height={promptH} fill="var(--color-primary)" rx={2} />
                  {/* Completion tokens (top) */}
                  <rect x={x} y={chartH - compH} width={barWidth} height={compH} fill="var(--color-primary)" opacity={0.35} rx={2} />
                </g>
              )
            })}
            {/* Separator line between actual and predicted */}
            {predictedData && predictedData.length > 0 && (() => {
              const sepX = barGap + actualCount * (barWidth + barGap) - barGap / 2
              return (
                <line x1={sepX} y1={0} x2={sepX} y2={chartH}
                  stroke="var(--color-text-muted)" strokeOpacity={0.4} strokeDasharray="4,3" strokeWidth={1} />
              )
            })()}
            {/* Predicted bars */}
            {predictedData && predictedData.map((d, i) => {
              const idx = actualCount + i
              const x = barGap + idx * (barWidth + barGap)
              const promptH = (d.prompt_tokens / maxVal) * chartH
              const compH = (d.completion_tokens / maxVal) * chartH
              const totalH = promptH + compH
              return (
                <g key={`pred-${d.bucket}`}
                  onMouseEnter={(e) => {
                    const rect = containerRef.current.getBoundingClientRect()
                    setTooltip({
                      x: e.clientX - rect.left,
                      y: e.clientY - rect.top,
                      data: d,
                      predicted: true,
                    })
                  }}
                  onMouseMove={(e) => {
                    const rect = containerRef.current.getBoundingClientRect()
                    setTooltip(prev => prev ? {
                      ...prev,
                      x: e.clientX - rect.left,
                      y: e.clientY - rect.top,
                    } : null)
                  }}
                  onMouseLeave={() => setTooltip(null)}
                  style={{ cursor: 'default' }}
                >
                  {/* Invisible hit area */}
                  <rect x={x} y={0} width={barWidth} height={chartH} fill="transparent" />
                  {/* Predicted bar outline */}
                  {totalH > 0 && (
                    <rect x={x} y={chartH - totalH} width={barWidth} height={totalH}
                      fill="var(--color-primary)" fillOpacity={0.08}
                      stroke="var(--color-primary)" strokeOpacity={0.35} strokeDasharray="3,2" strokeWidth={1}
                      rx={2} />
                  )}
                  {/* Prompt fill (faded) */}
                  <rect x={x} y={chartH - promptH - compH} width={barWidth} height={promptH}
                    fill="var(--color-primary)" opacity={0.15} rx={2} />
                  {/* Completion fill (more faded) */}
                  <rect x={x} y={chartH - compH} width={barWidth} height={compH}
                    fill="var(--color-primary)" opacity={0.08} rx={2} />
                </g>
              )
            })}
            {/* X-axis labels */}
            {allData.map((d, i) => {
              const x = barGap + i * (barWidth + barGap) + barWidth / 2
              // Skip some labels if too many
              const skip = allData.length > 20 ? Math.ceil(allData.length / 12) : 1
              if (i % skip !== 0) return null
              return (
                <text key={d.bucket} x={x} y={chartH + 16} textAnchor="middle" fontSize="10"
                  fill={d.predicted ? 'var(--color-text-muted)' : 'var(--color-text-secondary)'}
                  fontFamily="JetBrains Mono, monospace"
                  fontStyle={d.predicted ? 'italic' : 'normal'}
                >
                  {formatBucket(d.bucket, period)}
                </text>
              )
            })}
          </g>
        </svg>
        {tooltip && (
          <div style={{
            position: 'absolute',
            left: tooltip.x + 12,
            top: tooltip.y - 8,
            background: 'var(--color-bg-tertiary)',
            border: '1px solid var(--color-border)',
            borderRadius: 'var(--radius-md)',
            padding: 'var(--spacing-xs) var(--spacing-sm)',
            fontSize: '0.75rem',
            fontFamily: 'JetBrains Mono, monospace',
            color: 'var(--color-text-primary)',
            pointerEvents: 'none',
            zIndex: 10,
            boxShadow: 'var(--shadow-md)',
            whiteSpace: 'nowrap',
          }}>
            <div style={{ fontWeight: 600, marginBottom: 2 }}>
              {tooltip.predicted && <span style={{ color: 'var(--color-text-muted)', fontStyle: 'italic', marginRight: 4 }}>Predicted</span>}
              {formatBucket(tooltip.data.bucket, period)}
            </div>
            <div><span style={{ color: 'var(--color-primary)' }}>Prompt:</span> {tooltip.predicted ? '~' : ''}{tooltip.data.prompt_tokens.toLocaleString()}</div>
            <div><span style={{ color: 'var(--color-text-secondary)' }}>Completion:</span> {tooltip.predicted ? '~' : ''}{tooltip.data.completion_tokens.toLocaleString()}</div>
            <div style={{ color: 'var(--color-text-muted)', borderTop: '1px solid var(--color-border)', marginTop: 2, paddingTop: 2 }}>
              {tooltip.predicted ? '~' : ''}{tooltip.data.request_count} requests
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

function ModelDistChart({ rows }) {
  if (!rows || rows.length === 0) return null

  const maxVal = Math.max(...rows.map(r => r.total_tokens), 1)
  const barH = 24
  const gap = 4
  const height = rows.length * (barH + gap) + gap

  return (
    <div className="card" style={{ padding: 'var(--spacing-md)', marginBottom: 'var(--spacing-md)' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--spacing-sm)' }}>
        <span style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--color-text-primary)' }}>Token distribution by model</span>
        <div style={{ display: 'flex', gap: 'var(--spacing-md)', fontSize: '0.6875rem', color: 'var(--color-text-muted)' }}>
          <span><span style={{ display: 'inline-block', width: 8, height: 8, borderRadius: 2, background: 'var(--color-primary)', marginRight: 4, verticalAlign: 'middle' }} />Prompt</span>
          <span><span style={{ display: 'inline-block', width: 8, height: 8, borderRadius: 2, background: 'var(--color-primary)', opacity: 0.35, marginRight: 4, verticalAlign: 'middle' }} />Completion</span>
        </div>
      </div>
      <div style={{ display: 'flex', flexDirection: 'column', gap: gap }}>
        {rows.map(row => {
          const promptPct = (row.prompt_tokens / maxVal) * 100
          const compPct = (row.completion_tokens / maxVal) * 100
          return (
            <div key={row.model} style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)' }}>
              <div style={{
                width: 120, minWidth: 120, fontSize: '0.75rem', fontFamily: 'JetBrains Mono, monospace',
                color: 'var(--color-text-secondary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
              }} title={row.model}>
                {row.model}
              </div>
              <div style={{ flex: 1, height: barH, background: 'var(--color-bg-primary)', borderRadius: 4, overflow: 'hidden', display: 'flex' }}>
                <div style={{ width: `${promptPct}%`, height: '100%', background: 'var(--color-primary)', transition: 'width 0.3s ease' }} />
                <div style={{ width: `${compPct}%`, height: '100%', background: 'var(--color-primary)', opacity: 0.35, transition: 'width 0.3s ease' }} />
              </div>
              <div style={{
                minWidth: 60, textAlign: 'right', fontSize: '0.75rem', fontFamily: 'JetBrains Mono, monospace',
                color: 'var(--color-text-muted)', fontWeight: 600,
              }}>
                {formatNumber(row.total_tokens)}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}

export default function Usage() {
  const { addToast } = useOutletContext()
  const { isAdmin, authEnabled } = useAuth()
  const [period, setPeriod] = useState('month')
  const [loading, setLoading] = useState(true)
  const [usage, setUsage] = useState([])
  const [totals, setTotals] = useState({})
  const [adminUsage, setAdminUsage] = useState([])
  const [adminTotals, setAdminTotals] = useState({})
  const [activeTab, setActiveTab] = useState('models')
  const [quotas, setQuotas] = useState([])
  const [selectedUserId, setSelectedUserId] = useState(null)

  const fetchUsage = useCallback(async () => {
    setLoading(true)
    try {
      const usagePromise = fetch(apiUrl(`/api/auth/usage?period=${period}`))
      const quotaPromise = fetch(apiUrl('/api/auth/quota'))

      const [res, quotaRes] = await Promise.all([usagePromise, quotaPromise])

      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      const data = await res.json()
      setUsage(data.usage || [])
      setTotals(data.totals || {})

      if (quotaRes.ok) {
        const quotaData = await quotaRes.json()
        setQuotas(quotaData.quotas || [])
      }

      if (isAdmin) {
        const adminRes = await fetch(apiUrl(`/api/auth/admin/usage?period=${period}`))
        if (adminRes.ok) {
          const adminData = await adminRes.json()
          setAdminUsage(adminData.usage || [])
          setAdminTotals(adminData.totals || {})
        }
      }
    } catch (err) {
      addToast(`Failed to load usage: ${err.message}`, 'error')
    } finally {
      setLoading(false)
    }
  }, [period, isAdmin, addToast])

  useEffect(() => {
    if (authEnabled) fetchUsage()
    else setLoading(false)
  }, [fetchUsage, authEnabled])

  if (!authEnabled) {
    return (
      <div className="page">
        <div className="empty-state">
          <div className="empty-state-icon"><i className="fas fa-chart-bar" /></div>
          <h2 className="empty-state-title">Usage tracking unavailable</h2>
          <p className="empty-state-text">Authentication must be enabled to track API usage.</p>
        </div>
      </div>
    )
  }

  const modelRows = aggregateByModel(isAdmin ? adminUsage : usage)
  const userRows = isAdmin ? aggregateByUser(adminUsage) : []
  const maxTokens = modelRows.reduce((max, r) => Math.max(max, r.total_tokens), 0)
  const maxUserTokens = userRows.reduce((max, r) => Math.max(max, r.total_tokens), 0)

  const displayTotals = isAdmin ? adminTotals : totals
  const displayUsage = isAdmin ? adminUsage : usage
  const timeSeries = aggregateByBucket(displayUsage)

  const predictions = generatePredictions(timeSeries, period)
  const quotaExhaustion = computeQuotaExhaustion(quotas, timeSeries, period)
  const userPredictions = isAdmin && userRows.length > 0 ? generateUserPredictions(adminUsage, userRows, period) : {}

  const monoCell = { fontFamily: 'JetBrains Mono, monospace', fontSize: '0.8125rem' }

  return (
    <div className="page">
      <div className="page-header" style={{ marginBottom: 'var(--spacing-sm)' }}>
        <h1 className="page-title">Usage</h1>
        <p className="page-subtitle">API token usage statistics</p>
      </div>

      {/* Period selector + tabs */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)', marginBottom: 'var(--spacing-md)', flexWrap: 'wrap' }}>
        {PERIODS.map(p => (
          <button
            key={p.key}
            className={`btn btn-sm ${period === p.key ? 'btn-primary' : 'btn-secondary'}`}
            onClick={() => setPeriod(p.key)}
          >
            {p.label}
          </button>
        ))}
        {isAdmin && (
          <>
            <div style={{ width: 1, height: 20, background: 'var(--color-border-subtle)', margin: '0 var(--spacing-xs)' }} />
            <button
              className={`btn btn-sm ${activeTab === 'models' ? 'btn-primary' : 'btn-secondary'}`}
              onClick={() => setActiveTab('models')}
            >
              <i className="fas fa-cube" style={{ fontSize: '0.7rem' }} /> Models
            </button>
            <button
              className={`btn btn-sm ${activeTab === 'users' ? 'btn-primary' : 'btn-secondary'}`}
              onClick={() => setActiveTab('users')}
            >
              <i className="fas fa-users" style={{ fontSize: '0.7rem' }} /> Users
            </button>
          </>
        )}
        <div style={{ flex: 1 }} />
        <button className="btn btn-secondary btn-sm" onClick={fetchUsage} disabled={loading} style={{ gap: 4 }}>
          <i className={`fas fa-rotate${loading ? ' fa-spin' : ''}`} /> Refresh
        </button>
      </div>

      {loading ? (
        <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}>
          <LoadingSpinner size="lg" />
        </div>
      ) : (
        <>
          {/* Summary cards */}
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(120px, 1fr))', gap: 'var(--spacing-sm)', marginBottom: 'var(--spacing-md)' }}>
            <StatCard icon="fas fa-arrow-right-arrow-left" label="Requests" value={displayTotals.request_count} />
            <StatCard icon="fas fa-arrow-up" label="Prompt" value={displayTotals.prompt_tokens} />
            <StatCard icon="fas fa-arrow-down" label="Completion" value={displayTotals.completion_tokens} />
            <StatCard icon="fas fa-coins" label="Total" value={displayTotals.total_tokens} />
          </div>

          {/* Predictions */}
          {timeSeries.length > 0 && (
            <PredictionCards predictions={predictions} quotaExhaustion={quotaExhaustion} period={period} />
          )}

          {/* Charts */}
          <UsageTimeChart data={timeSeries} predictedData={predictions?.predictedBuckets} period={period} />
          {activeTab === 'models' && <ModelDistChart rows={modelRows} />}

          {/* Table */}
          {activeTab === 'models' && (
            modelRows.length === 0 ? (
              <div className="empty-state">
                <div className="empty-state-icon"><i className="fas fa-chart-bar" /></div>
                <h2 className="empty-state-title">No usage data</h2>
                <p className="empty-state-text">Usage data will appear here as API requests are made.</p>
              </div>
            ) : (
              <div className="table-container">
                <table className="table">
                  <thead>
                    <tr>
                      <th>Model</th>
                      <th style={{ width: 90 }}>Requests</th>
                      <th style={{ width: 110 }}>Prompt</th>
                      <th style={{ width: 110 }}>Completion</th>
                      <th style={{ width: 110 }}>Total</th>
                      <th style={{ width: 140 }}></th>
                    </tr>
                  </thead>
                  <tbody>
                    {modelRows.map(row => (
                      <tr key={row.model}>
                        <td style={monoCell}>{row.model}</td>
                        <td style={monoCell}>{formatNumber(row.request_count)}</td>
                        <td style={monoCell}>{formatNumber(row.prompt_tokens)}</td>
                        <td style={monoCell}>{formatNumber(row.completion_tokens)}</td>
                        <td style={{ ...monoCell, fontWeight: 600 }}>{formatNumber(row.total_tokens)}</td>
                        <td><UsageBar value={row.total_tokens} max={maxTokens} /></td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )
          )}

          {activeTab === 'users' && isAdmin && (
            userRows.length === 0 ? (
              <div className="empty-state">
                <div className="empty-state-icon"><i className="fas fa-users" /></div>
                <h2 className="empty-state-title">No user usage data</h2>
                <p className="empty-state-text">Per-user usage data will appear here as users make API requests.</p>
              </div>
            ) : (
              <div className="table-container">
                <table className="table">
                  <thead>
                    <tr>
                      <th></th>
                      <th>User</th>
                      <th style={{ width: 90 }}>Requests</th>
                      <th style={{ width: 110 }}>Prompt</th>
                      <th style={{ width: 110 }}>Completion</th>
                      <th style={{ width: 110 }}>Total</th>
                      <th style={{ width: 110 }}>Proj. Total</th>
                      <th style={{ width: 140 }}></th>
                    </tr>
                  </thead>
                  <tbody>
                    {userRows.map(row => {
                      const up = userPredictions[row.user_id]
                      const isExpanded = selectedUserId === row.user_id
                      return (
                        <Fragment key={row.user_id}>
                          <tr
                            onClick={() => setSelectedUserId(isExpanded ? null : row.user_id)}
                            style={{ cursor: 'pointer' }}
                          >
                            <td style={{ width: 28, textAlign: 'center', color: 'var(--color-text-muted)', fontSize: '0.7rem' }}>
                              <i className={`fas fa-chevron-${isExpanded ? 'down' : 'right'}`} />
                            </td>
                            <td style={{ fontSize: '0.8125rem' }}>{row.user_name}</td>
                            <td style={monoCell}>{formatNumber(row.request_count)}</td>
                            <td style={monoCell}>{formatNumber(row.prompt_tokens)}</td>
                            <td style={monoCell}>{formatNumber(row.completion_tokens)}</td>
                            <td style={{ ...monoCell, fontWeight: 600 }}>{formatNumber(row.total_tokens)}</td>
                            <td style={{ ...monoCell, color: 'var(--color-text-muted)', fontStyle: 'italic' }}>
                              {up?.predictions ? `~${formatNumber(up.predictions.projectedTotals.total_tokens)}` : '-'}
                            </td>
                            <td><UsageBar value={row.total_tokens} max={maxUserTokens} /></td>
                          </tr>
                          {isExpanded && up && (
                            <tr>
                              <td colSpan={8} style={{ padding: 0, background: 'var(--color-bg-secondary)' }}>
                                <div style={{ padding: 'var(--spacing-md)' }}>
                                  {up.predictions && (
                                    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(100px, 1fr))', gap: 'var(--spacing-xs)', marginBottom: 'var(--spacing-sm)' }}>
                                      <StatCard icon="fas fa-arrow-right-arrow-left" label="Proj. Requests" value={up.predictions.projectedTotals.request_count} muted />
                                      <StatCard icon="fas fa-arrow-up" label="Proj. Prompt" value={up.predictions.projectedTotals.prompt_tokens} muted />
                                      <StatCard icon="fas fa-arrow-down" label="Proj. Completion" value={up.predictions.projectedTotals.completion_tokens} muted />
                                      <StatCard icon="fas fa-coins" label="Proj. Total" value={up.predictions.projectedTotals.total_tokens} muted />
                                    </div>
                                  )}
                                  {up.timeSeries.length > 0 ? (
                                    <UsageTimeChart data={up.timeSeries} predictedData={up.predictions?.predictedBuckets} period={period} />
                                  ) : (
                                    <div style={{ fontSize: '0.8125rem', color: 'var(--color-text-muted)', padding: 'var(--spacing-sm)' }}>
                                      No time series data for this user.
                                    </div>
                                  )}
                                </div>
                              </td>
                            </tr>
                          )}
                        </Fragment>
                      )
                    })}
                  </tbody>
                </table>
              </div>
            )
          )}
        </>
      )}
    </div>
  )
}
