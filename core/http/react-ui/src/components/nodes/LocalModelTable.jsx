/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import ActionMenu from '../ActionMenu'
import Icon from '../Icon'
import { formatBytes } from './nodeStatus'
import { uptime } from '../../utils/localHost'
import { cssVars } from '../../utils/modelLedger'

// The models this host has in memory, one row each. A row here is one backend
// process, so the columns say what that process costs: its resident memory (the
// host's RAM, not GPU memory, which LocalAI does not report per model), its
// share of CPU, and how long it has been up.

function SortHead({ column, label, sort, onSortChange, className, t }) {
  const active = sort.key === column
  const nextDirection = active && sort.direction === 'asc' ? 'desc' : 'asc'
  return (
    <th
      scope="col"
      className={className}
      aria-sort={active ? (sort.direction === 'asc' ? 'ascending' : 'descending') : undefined}
    >
      <button
        type="button"
        className="dk-table-sort"
        onClick={() => onSortChange({ key: column, direction: nextDirection })}
        aria-label={t('machine.sortBy', { column: label.toLowerCase() }) + (active ? `, ${sort.direction}ending` : '')}
      >
        {label} {active && <Icon name={`arrow-${sort.direction === 'asc' ? 'up' : 'down'}`} />}
      </button>
    </th>
  )
}

function Usage({ percent, label, title, emptyLabel }) {
  if (percent == null) return <span className="op-usage__none" aria-label={title} title={title}>{emptyLabel}</span>
  const clamped = Math.min(100, Math.max(0, percent))
  return (
    <span className="op-usage" aria-label={title} title={title}>
      <span className="dk-meter op-usage__meter" aria-hidden="true">
        <span className="dk-meter-seg" style={cssVars({ '--dk-w': `${clamped.toFixed(1)}%` })} />
      </span>
      <span className="dk-mono">{label}</span>
    </span>
  )
}

export default function LocalModelTable({ models, onViewLogs, onStop, stoppingName, sort, onSortChange, now = Date.now() }) {
  const { t } = useTranslation('operate')
  return (
    <div className="dk-table-wrap op-modeltable">
      <table className="dk-table dk-table--compact local-model-table" aria-label={t('machine.tableLabel')}>
        <thead>
          <tr>
            <SortHead column="model_name" label={t('machine.columns.model')} sort={sort} onSortChange={onSortChange} t={t} />
            <SortHead column="backend" label={t('machine.columns.backend')} sort={sort} onSortChange={onSortChange} className="dk-hide-phone" t={t} />
            <SortHead column="rss_bytes" label={t('machine.columns.memory')} sort={sort} onSortChange={onSortChange} t={t} />
            <SortHead column="cpu_percent" label={t('machine.columns.cpu')} sort={sort} onSortChange={onSortChange} className="dk-hide-phone" t={t} />
            <SortHead column="started_at" label={t('machine.columns.uptime')} sort={sort} onSortChange={onSortChange} className="dk-hide-phone" t={t} />
            <th scope="col" className="dk-table-actions"><span className="dk-sr-only">{t('machine.columns.actions')}</span></th>
          </tr>
        </thead>
        <tbody>
          {models.map(model => {
            const up = uptime(model.started_at, now)
            const stopping = stoppingName === model.model_name
            return (
              <tr key={model.model_name} data-row data-testid="local-model-row" data-busy={stopping ? 'true' : undefined}>
                <td>
                  <span className="dk-table-name dk-mono">{model.model_name}</span>
                  {model.pid != null && <span className="dk-table-sub">PID {model.pid}</span>}
                </td>
                <td className="dk-hide-phone">{model.backend || <span className="op-usage__none">{t('machine.unknown')}</span>}</td>
                <td>
                  <Usage
                    percent={model.memory_percent}
                    label={model.rss_bytes != null ? formatBytes(model.rss_bytes) : null}
                    emptyLabel={t('machine.noData')}
                    title={model.rss_bytes != null
                      ? t('machine.memoryTitle', { size: formatBytes(model.rss_bytes), percent: model.memory_percent?.toFixed(1) })
                      : t('machine.memoryNone')}
                  />
                </td>
                <td className="dk-hide-phone">
                  {/* CPU is a delta between two server readings, so a process
                      seen for the first time has none yet; that is not "no data". */}
                  <Usage
                    percent={model.cpu_percent}
                    label={model.cpu_percent != null ? `${model.cpu_percent.toFixed(1)}%` : null}
                    emptyLabel={model.pid != null ? t('machine.measuring') : t('machine.noData')}
                    title={model.cpu_percent != null
                      ? t('machine.cpuTitle', { percent: model.cpu_percent.toFixed(1) })
                      : model.pid != null ? t('machine.cpuNotYet') : t('machine.cpuNone')}
                  />
                </td>
                <td className="dk-hide-phone dk-mono">{up ?? <span className="op-usage__none">{t('machine.unknown')}</span>}</td>
                <td className="dk-table-actions">
                  <ActionMenu
                    compact
                    ariaLabel={`${model.model_name} actions`}
                    triggerLabel={`Actions for ${model.model_name}`}
                    items={[{
                      key: 'logs',
                      icon: 'terminal',
                      label: t('machine.viewLogs'),
                      onClick: () => onViewLogs(model),
                    }, {
                      divider: true,
                    }, {
                      key: 'stop',
                      icon: 'stop',
                      label: stopping ? t('machine.stopping') : t('machine.stopModel'),
                      danger: true,
                      disabled: !!stoppingName,
                      onClick: invoker => onStop(model, invoker),
                    }]}
                  />
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
