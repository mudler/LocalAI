import { useTranslation } from 'react-i18next'
import { nodeRoute } from '../../utils/swarm'

// How the frontends reach a node: NATS, the tunnel over a websocket, or the
// tunnel over P2P with its path. It says nothing for a node that reports
// nothing, so an older worker shows no route instead of a guess.
export default function NodeRoute({ node }) {
  const { t } = useTranslation('swarm')
  const route = nodeRoute(node)
  if (!route) return null
  return (
    <span className="sw-route" data-route={route.kind} data-testid="node-route">
      <span className="dk-badge sw-route__kind">{t(`route.${route.kind}`)}</span>
      {route.path && <span className="sw-route__path">{t(`route.path.${route.path}`)}</span>}
      {route.limited && <span className="dk-badge dk-badge--warn" title={t('route.limitedHint')}>{t('route.limited')}</span>}
    </span>
  )
}
