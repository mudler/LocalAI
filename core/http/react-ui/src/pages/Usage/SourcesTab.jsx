import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { usageApi } from '../../utils/api'
import { useAuth } from '../../context/AuthContext'
import LoadingSpinner from '../../components/LoadingSpinner'

const EMPTY_DATA = {
  buckets: [],
  totals: { by_source: {}, by_key: [], grand_total: { tokens: 0, requests: 0 } },
  truncated: false,
}

// SourcesTab fetches and renders per-source / per-API-key usage breakdown.
// Task 9 ships a minimal skeleton (raw totals + key list) so the data path is
// exercised end to end. Tasks 10 and 11 replace the placeholders with the
// SourceMixRibbon, SourceTimeChart and SourcesTable visualisations.
export default function SourcesTab({ period, adminUserId }) {
  const { t } = useTranslation('admin')
  const { isAdmin } = useAuth()

  const [data, setData] = useState(EMPTY_DATA)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)

  // State held now even though Tasks 10/11 will use it visually.
  const [selectedKey, setSelectedKey] = useState(null)
  // eslint-disable-next-line no-unused-vars
  const [search, setSearch] = useState('')
  // eslint-disable-next-line no-unused-vars
  const [sortKey, setSortKey] = useState('tokens')

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    const p = isAdmin
      ? usageApi.getAdminSources(period, adminUserId)
      : usageApi.getMySources(period)
    p
      .then((d) => { if (!cancelled) setData(d || EMPTY_DATA) })
      .catch((e) => { if (!cancelled) setError(e) })
      .finally(() => { if (!cancelled) setLoading(false) })
    return () => { cancelled = true }
  }, [isAdmin, period, adminUserId])

  const totals = data.totals || EMPTY_DATA.totals
  const grandT = totals.grand_total || { tokens: 0, requests: 0 }
  const truncated = data.truncated || false

  const isEmpty = !loading && (grandT.tokens || 0) === 0 && (grandT.requests || 0) === 0

  if (loading) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', padding: 'var(--spacing-xl)' }}>
        <LoadingSpinner size="lg" />
      </div>
    )
  }

  if (error) {
    return (
      <div className="empty-state">
        <div className="empty-state-icon"><i className="fas fa-triangle-exclamation" /></div>
        <h2 className="empty-state-title">Failed to load</h2>
        <p className="empty-state-text">{String(error.message || error)}</p>
      </div>
    )
  }

  if (isEmpty) {
    return (
      <div className="empty-state">
        <div className="empty-state-icon"><i className="fas fa-key" /></div>
        <h2 className="empty-state-title">{t('usage.sources.noTrafficShort')}</h2>
        <p className="empty-state-text">{t('usage.sources.noKeysYet')}</p>
      </div>
    )
  }

  // Skeleton placeholders: Tasks 10 and 11 replace these with SourceMixRibbon,
  // SourceTimeChart, and SourcesTable.
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)' }}>
      <div className="card" style={{ padding: 'var(--spacing-md)' }}>
        <div style={{
          fontSize: '0.6875rem',
          color: 'var(--color-text-muted)',
          fontWeight: 500,
          textTransform: 'uppercase',
          letterSpacing: '0.03em',
          marginBottom: 'var(--spacing-xs)',
        }}>{t('usage.sources.mixTitle')}</div>
        <pre style={{
          fontSize: '0.75rem',
          background: 'var(--color-bg-secondary)',
          padding: 'var(--spacing-sm)',
          borderRadius: 'var(--radius-sm)',
          overflow: 'auto',
          margin: 0,
          fontFamily: 'var(--font-mono)',
        }}>{JSON.stringify(totals.by_source, null, 2)}</pre>
      </div>

      <div className="card" style={{ padding: 'var(--spacing-md)' }}>
        <div style={{
          fontSize: '0.6875rem',
          color: 'var(--color-text-muted)',
          fontWeight: 500,
          textTransform: 'uppercase',
          letterSpacing: '0.03em',
          marginBottom: 'var(--spacing-xs)',
        }}>{t('usage.sources.topSources')}</div>
        <ul style={{ listStyle: 'none', padding: 0, margin: 0 }}>
          {(totals.by_key || []).map((k) => {
            const isSelected = selectedKey === k.api_key_id
            return (
              <li
                key={k.api_key_id}
                onClick={() => setSelectedKey(isSelected ? null : k.api_key_id)}
                style={{
                  padding: 'var(--spacing-xs) var(--spacing-sm)',
                  cursor: 'pointer',
                  fontWeight: isSelected ? 600 : 400,
                  fontSize: '0.8125rem',
                  borderRadius: 'var(--radius-sm)',
                  background: isSelected ? 'var(--color-bg-secondary)' : 'transparent',
                  display: 'flex',
                  alignItems: 'center',
                  gap: 6,
                }}
              >
                <i className="fas fa-key" style={{ color: 'var(--color-text-muted)', fontSize: '0.75rem' }} />
                <span style={{ fontFamily: 'var(--font-mono)' }}>{k.api_key_name || k.api_key_id}</span>
                <span style={{ color: 'var(--color-text-muted)', marginLeft: 'auto', fontFamily: 'var(--font-mono)' }}>
                  {Number(k.tokens || 0).toLocaleString()}
                </span>
              </li>
            )
          })}
        </ul>
      </div>

      {truncated && (
        <div style={{ fontSize: '0.75rem', color: 'var(--color-warning)' }}>
          {t('usage.sources.truncatedWarning')}
        </div>
      )}
    </div>
  )
}
