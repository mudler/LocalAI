import { useTranslation } from 'react-i18next'
import { formatBytes } from '../nodes/nodeStatus'
import { memoryOf } from '../../utils/swarm'
import { cssVars } from '../../utils/modelLedger'

// One node's memory as a bar and two lines of words. A node that reports no
// memory says so; a bar for it would be an invention.
export default function MemoryMeter({ node }) {
  const { t } = useTranslation('swarm')
  const memory = memoryOf(node)
  if (!memory) return <span className="sw-unknown">{t('table.noReading')}</span>
  const percent = Math.round(memory.usagePercent)
  const level = percent >= 90 ? ' dk-meter-seg--error' : percent >= 75 ? ' dk-meter-seg--warn' : ''
  const ram = capacityLine(node)
  return (
    <span className="sw-mem">
      <span className="sw-mem__top">
        <span
          className="dk-meter sw-mem__bar"
          role="img"
          aria-label={t('table.memoryAria', { used: formatBytes(memory.used), total: formatBytes(memory.total), percent })}
        >
          <span className={`dk-meter-seg${level}`} style={cssVars({ '--dk-w': `${percent}%` })} />
        </span>
        <span className="dk-mono sw-mem__fig">{formatBytes(memory.used)} / {formatBytes(memory.total)}</span>
      </span>
      <span className="sw-mem__sub">
        {memory.kind === 'gpu'
          ? [node.gpu_vendor || t('table.gpu'), ram].filter(Boolean).join(' · ')
          : t('table.cpuOnly')}
      </span>
    </span>
  )

  function capacityLine(n) {
    const total = Number(n.total_ram)
    const free = Number(n.available_ram)
    if (!(total > 0) || !Number.isFinite(free)) return ''
    return t('table.ramLine', { used: formatBytes(Math.max(0, total - free)), total: formatBytes(total) })
  }
}
