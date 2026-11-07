import { useTranslation } from 'react-i18next'
import { loadSeries, sparkPoints } from '../../utils/nodeLoad'

const W = 96
const H = 24

// A node's recent load as a line. It draws only what the controller sent: with
// fewer than two readings it draws nothing at all.
export default function NodeSpark({ node }) {
  const { t } = useTranslation('swarm')
  const series = loadSeries(node)
  if (series.length === 0) return null
  const now = Math.round(series[series.length - 1])
  const points = sparkPoints(series, W, H)
  return (
    <svg
      className="sw-spark"
      data-hot={now >= 85 ? '' : undefined}
      viewBox={`0 0 ${W} ${H}`}
      role="img"
      aria-label={t('row.sparkAria', { count: series.length, percent: now })}
    >
      <polyline className="sw-spark__area" points={`${points} ${W - 2},${H} 2,${H}`} />
      <polyline className="sw-spark__line" points={points} />
    </svg>
  )
}
