import { useTranslation } from 'react-i18next'
import Icon from '../Icon'

const ICON = { inflight: 'clock', stays: 'check', blocked: 'alert-circle', reload: 'refresh' }

function selectorText(selector) {
  return Object.entries(selector || {}).map(([k, v]) => `${k}=${v}`).join(', ')
}

// The lines of a drain or lost-node preview. Each line is one sentence about one
// model, so a person can read what changes without a legend.
//
// `lost` words the same facts for a node that is already gone: nothing is
// "finishing first" on it.
export default function LeaveList({ items, lost = false }) {
  const { t } = useTranslation('swarm')
  if (items.length === 0) return <p className="sw-note">{t(lost ? 'leave.emptyLost' : 'leave.empty')}</p>
  return (
    <ul className="sw-leave" data-testid="leave-list">
      {items.map(item => (
        <li key={`${item.kind}:${item.model || ''}`} className="sw-leave__item" data-kind={item.kind}>
          <Icon name={ICON[item.kind]} />
          <span>
            {item.kind === 'inflight' && t(lost ? 'leave.inflightLost' : 'leave.inflight', { count: item.count })}
            {item.kind === 'stays' && <>
              <strong className="dk-mono">{item.model}</strong> {t('leave.staysOn', { nodes: item.on.join(', ') })}
            </>}
            {item.kind === 'blocked' && <>
              <strong className="dk-mono">{item.model}</strong> {t('leave.blocked', { selector: selectorText(item.selector) })}
            </>}
            {item.kind === 'reload' && <>
              <strong className="dk-mono">{item.model}</strong> {t(item.candidates > 0 ? 'leave.reload' : 'leave.reloadNone', { count: item.candidates })}
            </>}
          </span>
        </li>
      ))}
    </ul>
  )
}
