import { useState, useEffect } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { usageApi } from '../../utils/api'

// Compact admin-only usage glance: today's total tokens, optionally against a
// quota cap, linking to the full /app/usage page. Self-contained data fetch so
// a usage-API failure cannot break the navbar - it just renders nothing.
function sumTotalTokens(res) {
  const buckets = res?.buckets || res?.usage || (Array.isArray(res) ? res : [])
  if (!Array.isArray(buckets) || buckets.length === 0) return null
  return buckets.reduce((s, b) => s + (b.total_tokens || 0), 0)
}

export default function TokenUsageMeter() {
  const { t } = useTranslation('nav')
  const navigate = useNavigate()
  const [tokens, setTokens] = useState(null)
  const [cap, setCap] = useState(null)

  useEffect(() => {
    let cancelled = false
    usageApi.getAdminUsage('day')
      .then(res => { if (!cancelled) setTokens(sumTotalTokens(res)) })
      .catch(() => { if (!cancelled) setTokens(null) })
    usageApi.getMyQuotas()
      .then(q => { if (!cancelled) setCap(q?.token_limit || q?.tokens?.limit || null) })
      .catch(() => { if (!cancelled) setCap(null) })
    return () => { cancelled = true }
  }, [])

  if (tokens === null) return null

  const pct = cap ? Math.min(100, Math.round((tokens / cap) * 100)) : null

  return (
    <button
      type="button"
      className="top-navbar__meter"
      onClick={() => navigate('/app/usage')}
      title={t('topbar.usageDetail')}
    >
      <span className="top-navbar__meter-label">
        {t('topbar.tokensToday')}: {Intl.NumberFormat().format(tokens)}
        {cap ? ` / ${Intl.NumberFormat().format(cap)}` : ''}
      </span>
      {pct !== null && (
        <span className="top-navbar__meter-bar"><i style={{ width: `${pct}%` }} /></span>
      )}
    </button>
  )
}
