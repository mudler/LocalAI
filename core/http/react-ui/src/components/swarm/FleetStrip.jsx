/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import { SummaryStrip } from '../admin/AdminPage'

// The fleet in one slim strip under the header: how many workers are online,
// how many models are loaded and on how many workers, the requests in flight,
// and how many workers need a look. `summary` is rosterSummary().
export default function FleetStrip({ summary }) {
  const { t } = useTranslation('cluster')
  const detail = summary.attention === 0
    ? t('strip.allClear')
    : [summary.offline > 0 && t('strip.offline', { count: summary.offline }), summary.degraded > 0 && t('strip.degraded', { count: summary.degraded })].filter(Boolean).join(', ')
  return (
    <SummaryStrip
      label={t('strip.label')}
      items={[
        { key: 'online', label: t('strip.online'), value: summary.online, unit: t('strip.ofTotal', { total: summary.total }) },
        { key: 'models', label: t('strip.models'), value: summary.loaded, unit: t('strip.onWorkers', { count: summary.hosts }) },
        { key: 'flight', label: t('strip.inFlight'), value: summary.inFlight, unit: t('strip.requests', { count: summary.inFlight }) },
        { key: 'attention', label: t('strip.attention'), value: summary.attention, unit: detail, tone: summary.attention > 0 ? (summary.offline > 0 ? 'error' : 'warn') : undefined },
      ]}
    />
  )
}
