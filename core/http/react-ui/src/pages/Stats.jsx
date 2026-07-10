import { useState, useEffect, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useResources } from '../hooks/useResources'
import { usageApi, p2pApi } from '../utils/api'

function formatBytes(bytes) {
  if (bytes == null || bytes === 0) return '0 B'
  if (bytes >= 1073741824) return (bytes / 1073741824).toFixed(1) + ' GB'
  if (bytes >= 1048576) return (bytes / 1048576).toFixed(0) + ' MB'
  if (bytes >= 1024) return (bytes / 1024).toFixed(0) + ' KB'
  return bytes + ' B'
}

function formatNumber(n) {
  if (n == null) return '-'
  if (n >= 1000000) return (n / 1000000).toFixed(1) + 'M'
  if (n >= 1000) return (n / 1000).toFixed(1) + 'K'
  return String(n)
}

function formatPercent(used, total) {
  if (!total || total === 0) return '--'
  return ((used / total) * 100).toFixed(0) + '%'
}

function StatCard({ icon, label, value, sub, color }) {
  return (
    <div className="card" style={{ padding: 'var(--spacing-md)', flex: '1 1 0', minWidth: 140 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
        <i className={icon} style={{ color: color || 'var(--color-primary)', fontSize: '0.85rem' }} />
        <span style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)', fontWeight: 500, textTransform: 'uppercase', letterSpacing: '0.03em' }}>{label}</span>
      </div>
      <div style={{ fontSize: '1.5rem', fontWeight: 700, fontFamily: 'var(--font-mono)', color: 'var(--color-text-primary)' }}>
        {value}
      </div>
      {sub && <div style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)', marginTop: 2 }}>{sub}</div>}
    </div>
  )
}

function UsageBar({ value, max, label }) {
  const pct = max > 0 ? Math.min((value / max) * 100, 100) : 0
  const hue = pct > 85 ? 4 : pct > 60 ? 32 : 160
  return (
    <div style={{ marginBottom: 12 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: '0.6875rem', color: 'var(--color-text-muted)', marginBottom: 3 }}>
        <span>{label}</span>
        <span>{formatBytes(value)} / {formatBytes(max)}</span>
      </div>
      <div style={{ width: '100%', height: 8, borderRadius: 'var(--radius-sm)', background: 'var(--color-bg-primary)', overflow: 'hidden' }}>
        <div style={{
          width: `${pct}%`, height: '100%', borderRadius: 'var(--radius-sm)',
          background: `hsl(${hue}, 70%, 50%)`,
          transition: 'width 0.5s ease',
        }} />
      </div>
    </div>
  )
}

export default function Stats() {
  const { t } = useTranslation('stats')
  const navigate = useNavigate()
  const { resources, loading: resLoading } = useResources(10000)
  const [usage, setUsage] = useState(null)
  const [p2pStats, setP2pStats] = useState(null)
  const [usageLoading, setUsageLoading] = useState(true)

  const fetchUsage = useCallback(async () => {
    try {
      const data = await usageApi.getAdminSources('month')
      setUsage(data)
    } catch { /* auth/inactive — silently skip */ }
    finally { setUsageLoading(false) }
  }, [])

  const fetchP2P = useCallback(async () => {
    try {
      const data = await p2pApi.getStats()
      setP2pStats(data)
    } catch { /* not in distributed mode */ }
  }, [])

  useEffect(() => { fetchUsage(); fetchP2P() }, [fetchUsage, fetchP2P])

  const aggregate = resources?.aggregate
  const gpuMemory = aggregate?.type === 'gpu'

  // Usage totals
  const totalkens = usage?.buckets?.reduce((s, b) => s + (b.total_tokens || 0), 0) || 0
  const totalRequests = usage?.buckets?.reduce((s, b) => s + (b.request_count || 0), 0) || 0

  const p2pTotal = p2pStats?.online != null ? p2pStats.total_nodes : null
  const p2pOnline = p2pStats?.online ?? null

  // Individual GPU cards
  const gpus = resources?.gpus || []

  return (
    <div className="page">
      <div className="page-header">
        <h1 className="page-title">{t('title')}</h1>
        <p className="page-subtitle">{t('subtitle')}</p>
      </div>

      {/* System Resources Section */}
      <div style={{ marginBottom: 'var(--spacing-lg)' }}>
        <h2 style={{ fontSize: '0.8125rem', fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.04em', color: 'var(--color-text-muted)', marginBottom: 'var(--spacing-sm)' }}>
          {t('systemResources')}
        </h2>
        {resLoading ? (
          <div style={{ padding: 'var(--spacing-lg)', textAlign: 'center', color: 'var(--color-text-muted)' }}>
            {t('loading')}
          </div>
        ) : (
          <>
            <div style={{ display: 'flex', gap: 'var(--spacing-sm)', flexWrap: 'wrap', marginBottom: 'var(--spacing-md)' }}>
              <StatCard
                icon="fas fa-microchip"
                label={gpuMemory ? t('gpuMemory') : t('systemMemory')}
                value={formatPercent(aggregate?.total_memory - (aggregate?.free_memory || 0), aggregate?.total_memory)}
                sub={t('memoryDetail', { free: formatBytes(aggregate?.free_memory), total: formatBytes(aggregate?.total_memory) })}
                color="var(--color-success)"
              />
              <StatCard
                icon="fas fa-server"
                label={t('cpuInfo')}
                value={resources?.cpu?.model || resources?.cpu?.logical_cores ? `${resources?.cpu?.logical_cores || '?'} cores` : '--'}
                color="var(--color-info)"
              />
              <StatCard
                icon="fas fa-clock"
                label={t('uptime')}
                value={t('uptimeValue', { seconds: resources?.uptime_seconds || 0 })}
                color="var(--color-text-muted)"
              />
            </div>
            {gpus.length > 0 && (
              <div className="card" style={{ padding: 'var(--spacing-md)' }}>
                <h3 style={{ fontSize: '0.75rem', fontWeight: 600, marginBottom: 'var(--spacing-sm)' }}>{t('gpuDetails')}</h3>
                {gpus.map((gpu, i) => (
                  <UsageBar
                    key={i}
                    label={`${gpu.name || `GPU ${i}`}${gpu.driver_version ? ` (${gpu.driver_version})` : ''}`}
                    value={gpu.total_memory - gpu.free_memory}
                    max={gpu.total_memory}
                  />
                ))}
              </div>
            )}
          </>
        )}
      </div>

      {/* Usage Summary Section */}
      <div style={{ marginBottom: 'var(--spacing-lg)' }}>
        <h2 style={{ fontSize: '0.8125rem', fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.04em', color: 'var(--color-text-muted)', marginBottom: 'var(--spacing-sm)' }}>
          {t('usageOverview')}
        </h2>
        {usageLoading ? (
          <div style={{ padding: 'var(--spacing-lg)', textAlign: 'center', color: 'var(--color-text-muted)' }}>
            {t('loading')}
          </div>
        ) : (
          <div style={{ display: 'flex', gap: 'var(--spacing-sm)', flexWrap: 'wrap' }}>
            <StatCard
              icon="fas fa-bolt"
              label={t('totalTokens')}
              value={formatNumber(totalkens)}
              sub={t('thisMonth')}
              color="var(--color-primary)"
            />
            <StatCard
              icon="fas fa-paper-plane"
              label={t('totalRequests')}
              value={formatNumber(totalRequests)}
              sub={t('thisMonth')}
              color="var(--color-info)"
            />
            <StatCard
              icon="fas fa-diagram-project"
              label={t('activeModels')}
              value={usage?.buckets ? new Set(usage.buckets.map(b => b.model).filter(Boolean)).size : '-'}
              color="var(--color-warning)"
            />
          </div>
        )}
      </div>

      {/* P2P Section */}
      {p2pOnline != null && (
        <div style={{ marginBottom: 'var(--spacing-lg)' }}>
          <h2 style={{ fontSize: '0.8125rem', fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.04em', color: 'var(--color-text-muted)', marginBottom: 'var(--spacing-sm)' }}>
            {t('p2pNetwork')}
          </h2>
          <div style={{ display: 'flex', gap: 'var(--spacing-sm)', flexWrap: 'wrap' }}>
            <StatCard
              icon="fas fa-network-wired"
              label={t('nodesOnline')}
              value={`${p2pOnline} / ${p2pTotal || '?'}`}
              color="var(--color-success)"
            />
          </div>
        </div>
      )}

      {/* Quick Links */}
      <div className="card" style={{ padding: 'var(--spacing-md)' }}>
        <h3 style={{ fontSize: '0.75rem', fontWeight: 600, marginBottom: 'var(--spacing-sm)' }}>{t('quickLinks')}</h3>
        <div style={{ display: 'flex', gap: 'var(--spacing-sm)', flexWrap: 'wrap' }}>
          <button className="btn btn--secondary" onClick={() => navigate('/app/usage')}>
            <i className="fas fa-chart-bar" style={{ marginRight: 6 }} />
            {t('linkUsage')}
          </button>
          <button className="btn btn--secondary" onClick={() => navigate('/app/traces')}>
            <i className="fas fa-bug" style={{ marginRight: 6 }} />
            {t('linkTraces')}
          </button>
          {p2pOnline != null && (
            <button className="btn btn--secondary" onClick={() => navigate('/app/nodes')}>
              <i className="fas fa-cubes" style={{ marginRight: 6 }} />
              {t('linkNodes')}
            </button>
          )}
          <button className="btn btn--secondary" onClick={() => navigate('/app/backends')}>
            <i className="fas fa-cogs" style={{ marginRight: 6 }} />
            {t('linkBackends')}
          </button>
          <button className="btn btn--secondary" onClick={() => navigate('/app/settings')}>
            <i className="fas fa-sliders-h" style={{ marginRight: 6 }} />
            {t('linkSettings')}
          </button>
        </div>
      </div>
    </div>
  )
}
