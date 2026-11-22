/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import { StatusChip } from '../ui'

// What a connection says after its name: how it is doing now. A P2P connection
// says its path and round trip while it works. A route that fails says why, in
// the words the worker gave.
function detailOf(connection, t) {
  if (connection.kind === 'p2p' && connection.state === 'live') {
    const path = connection.pathKey ? t(`conn.path.${connection.pathKey}`, { relay: connection.relay }) : t('conn.live')
    return connection.rtt ? `${path} · ${t('conn.rtt', { ms: connection.rtt })}` : path
  }
  if (connection.state === 'live') return t('conn.live')
  if (connection.state === 'reconnecting') return t('conn.reconnecting')
  return connection.reason || t('conn.down')
}

// How a worker reaches the frontends, one chip for each connection it holds:
// NATS, WebSocket or P2P, with its state. A failing connection is a warning
// chip with its reason, a P2P tunnel that a relay limits is marked Limited.
// A worker that reports nothing says so, instead of a guess.
export default function ConnectionChips({ connections, label }) {
  const { t } = useTranslation('cluster')
  if (!connections || connections.length === 0) {
    return <span className="wk-conns"><StatusChip tone="idle" testId="conn-chip">{t('conn.notReported')}</StatusChip></span>
  }
  return (
    <span className="wk-conns" role="group" aria-label={label || t('conn.label')} data-testid="connections">
      {connections.map(connection => {
        const detail = detailOf(connection, t)
        const tone = connection.state === 'down' && connection.tone === 'error' ? 'warn' : connection.tone
        return (
          <span key={connection.key} className="wk-conn">
            <StatusChip tone={tone} dot testId="conn-chip" title={connection.reason || undefined} className={`wk-chip wk-chip--${connection.kind}`}>
              <b>{t(`conn.name.${connection.kind}`)}</b>{' '}<span className="wk-chip__detail" data-state={connection.state}>{detail}</span>
            </StatusChip>
            {connection.limited && <StatusChip tone="warn" testId="conn-limited" title={t('conn.limitedHint')}>{t('conn.limited')}</StatusChip>}
          </span>
        )
      })}
    </span>
  )
}
