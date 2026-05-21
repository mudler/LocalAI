import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { usageApi, apiKeysApi } from '../../utils/api'
import { useAuth } from '../../context/AuthContext'
import LoadingSpinner from '../../components/LoadingSpinner'
import SourceMixRibbon from './SourceMixRibbon'
import SourcesTable from './SourcesTable'
import SourceTimeChart from './SourceTimeChart'

const EMPTY_DATA = {
  buckets: [],
  totals: { by_source: {}, by_key: [], grand_total: { tokens: 0, requests: 0 } },
  truncated: false,
}

// Resolve a human label for the currently selected key (web/legacy class or api_key_id).
function labelForSelected(totals, selectedKey, t) {
  if (!selectedKey) return ''
  if (selectedKey === 'web')    return t('usage.sources.webUI')
  if (selectedKey === 'legacy') return t('usage.sources.legacy')
  const row = (totals?.by_key || []).find(k => k.api_key_id === selectedKey)
  return row ? (row.api_key_name || selectedKey) : selectedKey
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
  // revoked. null = "don't know yet" so the table won't dim live keys during
  // the fetch or after a failure.
  const [existingKeyIds, setExistingKeyIds] = useState(null)
  useEffect(() => {
    apiKeysApi
      .list()
      .then((resp) => {
        const list = Array.isArray(resp) ? resp : (resp?.keys || [])
        setExistingKeyIds(new Set(list.map((k) => k.id)))
      })
      .catch(() => { /* leave existingKeyIds null so revoked detection is skipped */ })
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
  const buckets = data.buckets || EMPTY_DATA.buckets
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

      {selectedKey && (
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-xs)' }}>
          <span
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              gap: 'var(--spacing-xs)',
              padding: 'calc(var(--spacing-xs) / 2) var(--spacing-sm)',
              background: 'var(--color-bg-secondary)',
              color: 'var(--color-text-primary)',
              fontSize: '0.75rem',
              borderRadius: 'var(--radius-sm)',
              border: '1px solid var(--color-border-subtle)',
            }}
          >
            <i className="fas fa-filter" style={{ fontSize: '0.6875rem', color: 'var(--color-text-muted)' }} aria-hidden />
            {t('usage.sources.filteredTo', { name: labelForSelected(totals, selectedKey, t) })}
            <button
              type="button"
              onClick={() => setSelectedKey(null)}
              aria-label={t('usage.sources.clearFilter')}
              style={{
                appearance: 'none',
                background: 'transparent',
                border: 'none',
                color: 'var(--color-text-muted)',
                cursor: 'pointer',
                padding: 0,
                fontSize: '0.875rem',
                lineHeight: 1,
              }}
            >
              <i className="fas fa-xmark" />
            </button>
          </span>
        </div>
      )}

      <div className="card" style={{ padding: 'var(--spacing-md)' }}>
        <SourceTimeChart buckets={buckets} selectedKey={selectedKey} totals={totals} />
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
          showUserColumn={isAdmin}
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
