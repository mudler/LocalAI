import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { usageApi, apiKeysApi } from '../../utils/api'
import { useAuth } from '../../context/AuthContext'
import LoadingSpinner from '../../components/LoadingSpinner'
import SourceMixRibbon from './SourceMixRibbon'
import SourcesTable from './SourcesTable'

const EMPTY_DATA = {
  buckets: [],
  totals: { by_source: {}, by_key: [], grand_total: { tokens: 0, requests: 0 } },
  truncated: false,
}

// SourcesTab fetches and renders per-source / per-API-key usage breakdown.
// Task 10 replaces the raw JSON / list placeholders with SourceMixRibbon and
// SourcesTable. Task 11 will add the time chart and drill-in chip.
export default function SourcesTab({ period, adminUserId }) {
  const { t } = useTranslation('admin')
  const { isAdmin } = useAuth()

  const [data, setData] = useState(EMPTY_DATA)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState(null)

  const [selectedKey, setSelectedKey] = useState(null)
  const [search, setSearch] = useState('')
  const [sortKey, setSortKey] = useState('tokens')

  // Pull the current set of API key ids so the table can mark unknown keys as
  // revoked. Failure is non-fatal: the revoked badge just won't render.
  const [existingKeyIds, setExistingKeyIds] = useState(new Set())
  useEffect(() => {
    apiKeysApi
      .list()
      .then((resp) => {
        const list = Array.isArray(resp) ? resp : (resp?.keys || [])
        setExistingKeyIds(new Set(list.map((k) => k.id)))
      })
      .catch(() => { /* revoked detection is best-effort */ })
  }, [])

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

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-md)' }}>
      <div className="card" style={{ padding: 'var(--spacing-md)' }}>
        <SourceMixRibbon
          bySource={totals.by_source}
          keyCount={(totals.by_key || []).length}
          onSelectSourceClass={(cls) => setSelectedKey(cls)}
        />
      </div>

      <div className="card" style={{ padding: 'var(--spacing-md)' }}>
        <SourcesTable
          totals={totals}
          selectedKey={selectedKey}
          onSelectKey={setSelectedKey}
          search={search}
          setSearch={setSearch}
          sortKey={sortKey}
          setSortKey={setSortKey}
          existingKeyIds={existingKeyIds}
        />
      </div>

      {truncated && (
        <div style={{ fontSize: '0.75rem', color: 'var(--color-warning)' }}>
          {t('usage.sources.truncatedWarning')}
        </div>
      )}
    </div>
  )
}
