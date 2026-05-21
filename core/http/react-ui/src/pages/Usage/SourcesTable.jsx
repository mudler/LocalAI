import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

const SORT_FNS = {
  tokens: (a, b) => (b.tokens || 0) - (a.tokens || 0),
  requests: (a, b) => (b.requests || 0) - (a.requests || 0),
  last_used: (a, b) => new Date(b.last_used || 0).getTime() - new Date(a.last_used || 0).getTime(),
  name: (a, b) => (a.name || '').localeCompare(b.name || ''),
  user: (a, b) => (a.userName || '').localeCompare(b.userName || ''),
}

function formatTokens(n) {
  if (!n) return '0'
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M'
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'k'
  return String(n)
}

function formatRelative(iso) {
  if (!iso) return '-'
  const t = new Date(iso).getTime()
  if (Number.isNaN(t) || t <= 0) return '-'
  const diff = Date.now() - t
  if (diff < 60_000) return 'just now'
  if (diff < 3_600_000) return Math.round(diff / 60_000) + 'm ago'
  if (diff < 86_400_000) return Math.round(diff / 3_600_000) + 'h ago'
  return Math.round(diff / 86_400_000) + 'd ago'
}

// SourcesTable is the searchable, sortable list of key totals plus pseudo-rows
// for the web UI and legacy (unkeyed) source classes. Clicking a row selects
// it; the parent decides what to do with the selection (the drill-in panel
// will be wired in Task 11).
//
// Props:
//   totals: SourceTotals payload (from /api/auth/usage/sources)
//   selectedKey: currently-selected row id (api_key_id | 'web' | 'legacy' | null)
//   onSelectKey: (id|null) => void
//   search / setSearch: free-text filter state lifted to the parent
//   sortKey / setSortKey: sort column state lifted to the parent
//   existingKeyIds: Set<string> of current (non-revoked) api key ids, or null
//     when the parent hasn't yet learned which keys exist. Null suppresses the
//     revoked badge entirely so live keys aren't dimmed during the fetch or
//     after a failure.
//   showUserColumn: render the User column. Admin views set this true so the
//     reader can attribute each key (and each Web UI row) to its owner.
export default function SourcesTable({
  totals,
  selectedKey,
  onSelectKey,
  search,
  setSearch,
  sortKey,
  setSortKey,
  existingKeyIds = null,
  showUserColumn = false,
}) {
  const { t } = useTranslation('admin')

  const rows = useMemo(() => {
    const named = (totals?.by_key || []).map((k) => ({
      kind: 'apikey',
      id: k.api_key_id,
      name: k.api_key_name || k.api_key_id,
      userID: k.user_id || '',
      userName: k.user_name || '',
      prefix: '',
      tokens: k.tokens,
      requests: k.requests,
      last_used: k.last_used,
      revoked: existingKeyIds != null && !existingKeyIds.has(k.api_key_id),
    }))

    // Pseudo-rows for sources that don't have a named key identity.
    // In admin view (showUserColumn=true), prefer the per-user breakdown
    // from totals.by_user_source so each user's Web UI / legacy traffic
    // gets its own row. Otherwise fall back to the global by_source aggregate.
    let unkeyed = []
    if (showUserColumn && Array.isArray(totals?.by_user_source) && totals.by_user_source.length > 0) {
      unkeyed = totals.by_user_source.map((r) => ({
        kind: r.source,
        id: r.source + ':' + (r.user_id || ''),
        name: r.source === 'legacy' ? t('usage.sources.legacy') : t('usage.sources.webUI'),
        userID: r.user_id || '',
        userName: r.user_name || '',
        prefix: '-',
        tokens: r.tokens,
        requests: r.requests,
      }))
    } else {
      if (totals?.by_source?.web) {
        unkeyed.push({
          kind: 'web',
          id: 'web',
          name: t('usage.sources.webUI'),
          userID: '',
          userName: '',
          prefix: '-',
          tokens: totals.by_source.web.tokens,
          requests: totals.by_source.web.requests,
        })
      }
      if (totals?.by_source?.legacy) {
        unkeyed.push({
          kind: 'legacy',
          id: 'legacy',
          name: t('usage.sources.legacy'),
          userID: '',
          userName: '',
          prefix: '-',
          tokens: totals.by_source.legacy.tokens,
          requests: totals.by_source.legacy.requests,
        })
      }
    }

    return [...named, ...unkeyed]
  }, [totals, existingKeyIds, showUserColumn, t])

  const filtered = useMemo(() => {
    const q = (search || '').trim().toLowerCase()
    const list = q
      ? rows.filter((r) =>
          (r.name || '').toLowerCase().includes(q) ||
          (r.prefix || '').toLowerCase().includes(q) ||
          (r.userName || '').toLowerCase().includes(q) ||
          (r.userID || '').toLowerCase().includes(q)
        )
      : rows
    return [...list].sort(SORT_FNS[sortKey] || SORT_FNS.tokens)
  }, [rows, search, sortKey])

  const iconFor = (kind) =>
    kind === 'apikey' ? 'fas fa-key' : kind === 'web' ? 'fas fa-globe' : 'fas fa-gear'

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-sm)' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--spacing-sm)', flexWrap: 'wrap' }}>
        <input
          type="search"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder={t('usage.sources.searchPlaceholder')}
          aria-label={t('usage.sources.searchPlaceholder')}
          style={{
            flex: '1 1 12rem',
            minWidth: 160,
            padding: 'var(--spacing-xs) var(--spacing-sm)',
            border: '1px solid var(--color-border-subtle)',
            borderRadius: 'var(--radius-sm)',
            background: 'var(--color-bg-primary)',
            color: 'var(--color-text-primary)',
          }}
        />
        <label style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: '0.75rem' }}>
          {t('usage.sources.sortBy')}:
          <select
            value={sortKey}
            onChange={(e) => setSortKey(e.target.value)}
            style={{
              padding: 'calc(var(--spacing-xs) / 2) var(--spacing-xs)',
              border: '1px solid var(--color-border-subtle)',
              borderRadius: 'var(--radius-sm)',
              background: 'var(--color-bg-primary)',
              color: 'var(--color-text-primary)',
            }}
          >
            <option value="tokens">{t('usage.sources.sortTokens')}</option>
            <option value="requests">{t('usage.sources.sortRequests')}</option>
            <option value="last_used">{t('usage.sources.sortLastUsed')}</option>
            <option value="name">{t('usage.sources.sortName')}</option>
            {showUserColumn && <option value="user">{t('usage.sources.sortUser')}</option>}
          </select>
        </label>
      </div>

      <div className="table-container">
        <table className="table">
          <thead>
            <tr>
              <th>{t('usage.sources.sortName')}</th>
              {showUserColumn && <th style={{ width: 180 }}>{t('usage.sources.sortUser')}</th>}
              <th style={{ width: 110 }}>Prefix</th>
              <th style={{ width: 100, textAlign: 'right' }}>{t('usage.sources.sortRequests')}</th>
              <th style={{ width: 100, textAlign: 'right' }}>{t('usage.sources.sortTokens')}</th>
              <th style={{ width: 120, textAlign: 'right' }}>{t('usage.sources.sortLastUsed')}</th>
            </tr>
          </thead>
          <tbody>
            {filtered.map((r) => {
              const isSel = selectedKey === r.id
              return (
                <tr
                  key={r.id}
                  onClick={() => onSelectKey?.(isSel ? null : r.id)}
                  style={{
                    cursor: 'pointer',
                    background: isSel ? 'var(--color-bg-secondary)' : undefined,
                    opacity: r.revoked ? 0.5 : 1,
                  }}
                >
                  <td>
                    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
                      <i
                        className={iconFor(r.kind)}
                        style={{ color: 'var(--color-text-muted)', fontSize: '0.8125rem' }}
                      />
                      <span>{r.name}</span>
                      {r.revoked && (
                        <span
                          style={{
                            fontSize: '0.6875rem',
                            textTransform: 'uppercase',
                            color: 'var(--color-text-muted)',
                          }}
                        >
                          ({t('usage.sources.revoked')})
                        </span>
                      )}
                    </span>
                  </td>
                  {showUserColumn && (
                    <td style={{ color: 'var(--color-text-secondary)', fontSize: '0.8125rem' }}>
                      {r.userName || r.userID || '-'}
                    </td>
                  )}
                  <td style={{ color: 'var(--color-text-muted)', fontSize: '0.75rem' }}>{r.prefix || '-'}</td>
                  <td style={{ textAlign: 'right', fontFamily: 'var(--font-mono)' }}>
                    {Number(r.requests || 0).toLocaleString()}
                  </td>
                  <td style={{ textAlign: 'right', fontFamily: 'var(--font-mono)' }}>
                    {formatTokens(r.tokens || 0)}
                  </td>
                  <td style={{ textAlign: 'right', fontSize: '0.75rem', color: 'var(--color-text-muted)' }}>
                    {formatRelative(r.last_used)}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </div>
  )
}
