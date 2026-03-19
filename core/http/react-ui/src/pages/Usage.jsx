import { useState, useEffect, useCallback, useRef } from 'react'
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

function formatNumber(n) {
  if (n == null) return '0'
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M'
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K'
  return String(n)
}

function StatCard({ icon, label, value }) {
  return (
    <div className="card" style={{ padding: 'var(--spacing-sm) var(--spacing-md)', flex: '1 1 0', minWidth: 120 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 2 }}>
        <i className={icon} style={{ color: 'var(--color-text-muted)', fontSize: '0.75rem' }} />
        <span style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)', fontWeight: 500, textTransform: 'uppercase', letterSpacing: '0.03em' }}>{label}</span>
      </div>
      <div style={{ fontSize: '1.375rem', fontWeight: 700, fontFamily: 'JetBrains Mono, monospace', color: 'var(--color-text-primary)' }}>
        {formatNumber(value)}
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

function UsageTimeChart({ data, period }) {
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

  const height = 200
  const margin = { top: 16, right: 16, bottom: 40, left: 56 }
  const chartW = width - margin.left - margin.right
  const chartH = height - margin.top - margin.bottom

  const maxVal = Math.max(...data.map(d => d.total_tokens), 1)
  const barWidth = Math.max(Math.min(chartW / data.length - 2, 40), 4)
  const barGap = (chartW - barWidth * data.length) / (data.length + 1)

  // Y-axis ticks (4 ticks)
  const ticks = [0, 1, 2, 3, 4].map(i => Math.round(maxVal * i / 4))

  return (
    <div className="card" style={{ padding: 'var(--spacing-md)', marginBottom: 'var(--spacing-md)' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 'var(--spacing-sm)' }}>
        <span style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--color-text-primary)' }}>Tokens over time</span>
        <div style={{ display: 'flex', gap: 'var(--spacing-md)', fontSize: '0.6875rem', color: 'var(--color-text-muted)' }}>
          <span><span style={{ display: 'inline-block', width: 8, height: 8, borderRadius: 2, background: 'var(--color-primary)', marginRight: 4, verticalAlign: 'middle' }} />Prompt</span>
          <span><span style={{ display: 'inline-block', width: 8, height: 8, borderRadius: 2, background: 'var(--color-primary)', opacity: 0.35, marginRight: 4, verticalAlign: 'middle' }} />Completion</span>
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
            {/* Bars */}
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
            {/* X-axis labels */}
            {data.map((d, i) => {
              const x = barGap + i * (barWidth + barGap) + barWidth / 2
              // Skip some labels if too many
              const skip = data.length > 20 ? Math.ceil(data.length / 12) : 1
              if (i % skip !== 0) return null
              return (
                <text key={d.bucket} x={x} y={chartH + 16} textAnchor="middle" fontSize="10" fill="var(--color-text-secondary)" fontFamily="JetBrains Mono, monospace">
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
            <div style={{ fontWeight: 600, marginBottom: 2 }}>{formatBucket(tooltip.data.bucket, period)}</div>
            <div><span style={{ color: 'var(--color-primary)' }}>Prompt:</span> {tooltip.data.prompt_tokens.toLocaleString()}</div>
            <div><span style={{ color: 'var(--color-text-secondary)' }}>Completion:</span> {tooltip.data.completion_tokens.toLocaleString()}</div>
            <div style={{ color: 'var(--color-text-muted)', borderTop: '1px solid var(--color-border)', marginTop: 2, paddingTop: 2 }}>
              {tooltip.data.request_count} requests
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

  const fetchUsage = useCallback(async () => {
    setLoading(true)
    try {
      const res = await fetch(apiUrl(`/api/auth/usage?period=${period}`))
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      const data = await res.json()
      setUsage(data.usage || [])
      setTotals(data.totals || {})

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

          {/* Charts */}
          <UsageTimeChart data={timeSeries} period={period} />
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
                      <th>User</th>
                      <th style={{ width: 90 }}>Requests</th>
                      <th style={{ width: 110 }}>Prompt</th>
                      <th style={{ width: 110 }}>Completion</th>
                      <th style={{ width: 110 }}>Total</th>
                      <th style={{ width: 140 }}></th>
                    </tr>
                  </thead>
                  <tbody>
                    {userRows.map(row => (
                      <tr key={row.user_id}>
                        <td style={{ fontSize: '0.8125rem' }}>{row.user_name}</td>
                        <td style={monoCell}>{formatNumber(row.request_count)}</td>
                        <td style={monoCell}>{formatNumber(row.prompt_tokens)}</td>
                        <td style={monoCell}>{formatNumber(row.completion_tokens)}</td>
                        <td style={{ ...monoCell, fontWeight: 600 }}>{formatNumber(row.total_tokens)}</td>
                        <td><UsageBar value={row.total_tokens} max={maxUserTokens} /></td>
                      </tr>
                    ))}
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
