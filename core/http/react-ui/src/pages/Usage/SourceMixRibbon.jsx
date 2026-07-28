import { useTranslation } from 'react-i18next'

const SEGMENT_COLORS = {
  apikey: 'var(--color-primary)',
  web: 'var(--color-info, #3b82f6)',
  legacy: 'var(--color-warning, #f59e0b)',
}

// SourceMixRibbon renders one segmented horizontal bar showing the share of
// tokens by source class (apikey / web / legacy). Clicking a segment invokes
// onSelectSourceClass with the segment key so the parent can filter the view.
//
// Props:
//   bySource: { apikey?: {tokens, requests}, web?: {...}, legacy?: {...} }
//   keyCount: number of distinct API keys in the dataset (for the legend)
//   onSelectSourceClass: (cls: 'apikey'|'web'|'legacy') => void (optional)
export default function SourceMixRibbon({ bySource = {}, keyCount = 0, onSelectSourceClass }) {
  const { t } = useTranslation('admin')

  const apikey = (bySource.apikey?.tokens) || 0
  const web = (bySource.web?.tokens) || 0
  const legacy = (bySource.legacy?.tokens) || 0
  const total = apikey + web + legacy || 1

  const pct = (n) => Math.round((n / total) * 100)
  const apiPct = pct(apikey)
  const webPct = pct(web)
  const legacyPct = pct(legacy)

  const segments = [
    { key: 'apikey', label: `${apiPct}% API keys (${keyCount})`, pct: apiPct, color: SEGMENT_COLORS.apikey },
    { key: 'web', label: `${webPct}% ${t('usage.sources.webUI')}`, pct: webPct, color: SEGMENT_COLORS.web },
    { key: 'legacy', label: `${legacyPct}% ${t('usage.sources.legacy')}`, pct: legacyPct, color: SEGMENT_COLORS.legacy },
  ].filter((s) => s.pct > 0)

  return (
    <div
      role="group"
      aria-label={t('usage.sources.ribbonAria', { apikey: apiPct, web: webPct, legacy: legacyPct })}
      style={{ display: 'flex', flexDirection: 'column', gap: 'var(--spacing-xs)' }}
    >
      <div style={{ fontSize: '0.875rem', fontWeight: 600, color: 'var(--color-text-primary)' }}>
        {t('usage.sources.mixTitle')}
      </div>
      <div
        style={{
          display: 'flex',
          height: 12,
          borderRadius: 'var(--radius-sm)',
          overflow: 'hidden',
          border: '1px solid var(--color-border-subtle)',
        }}
      >
        {segments.map((s) => (
          <button
            key={s.key}
            type="button"
            onClick={() => onSelectSourceClass?.(s.key)}
            aria-label={s.label}
            style={{
              width: `${s.pct}%`,
              background: s.color,
              border: 'none',
              padding: 0,
              cursor: onSelectSourceClass ? 'pointer' : 'default',
            }}
          />
        ))}
      </div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--spacing-sm)', fontSize: '0.75rem' }}>
        {segments.map((s) => (
          <span key={s.key} style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
            <span
              style={{ width: 10, height: 10, borderRadius: 2, background: s.color, display: 'inline-block' }}
              aria-hidden
            />
            {s.label}
          </span>
        ))}
      </div>
    </div>
  )
}
