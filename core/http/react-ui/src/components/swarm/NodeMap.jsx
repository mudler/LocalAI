/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { cssVars } from '../../utils/modelLedger'
import { formatBytes } from '../nodes/nodeStatus'
import { memoryOf } from '../../utils/swarm'
import NodeState from './NodeState'

const CARD = 84
const GAP = 10
// A map of a thousand nodes is not a map. Past this the list is the tool.
export const MAP_LIMIT = 24

// Where requests go: this instance on the left, one card per worker on the
// right. A line is a registered worker; a dashed line is a worker that gets no
// traffic now (it is waiting, draining or not answering). It shows the same
// facts as the list and no health number the list lacks.
//
// `rows` are the loaded replicas, or null until they have been read.
export default function NodeMap({ nodes, rows }) {
  const { t } = useTranslation('swarm')
  const shown = nodes.slice(0, MAP_LIMIT)
  const height = shown.length * CARD + Math.max(0, shown.length - 1) * GAP
  const mid = height / 2
  const modelNames = node => {
    if (!rows) return null
    return [...new Set(rows.filter(r => r.node_id === node.id && (!r.state || r.state === 'loaded')).map(r => r.model_name))]
  }

  return (
    <div className="sw-map" data-testid="nodes-map" style={cssVars({ '--sw-card': `${CARD}px`, '--sw-gap': `${GAP}px` })}>
      <div className="sw-map__left">
        <div className="sw-map__instance">
          <strong>{t('map.instance')}</strong>
          <span>{t('map.instanceSub')}</span>
        </div>
        <div className="sw-map__bus" aria-hidden="true">{t('map.bus')}</div>
      </div>
      <div className="sw-map__wire"><svg className="sw-map__links" viewBox={`0 0 100 ${height}`} preserveAspectRatio="none" aria-hidden="true">
        {shown.map((node, index) => {
          const y = index * (CARD + GAP) + CARD / 2
          const routed = node.status === 'healthy'
          const down = node.status === 'unhealthy' || node.status === 'offline'
          return (
            <path
              key={node.id}
              d={`M 0 ${mid} C 55 ${mid}, 45 ${y}, 100 ${y}`}
              className="sw-map__link"
              data-routed={routed ? 'true' : 'false'}
              data-down={down ? '' : undefined}
              vectorEffect="non-scaling-stroke"
            />
          )
        })}
      </svg></div>
      <ul className="sw-map__nodes">
        {shown.map(node => {
          const memory = memoryOf(node)
          const names = modelNames(node)
          return (
            <li key={node.id}>
              <Link
                className="sw-map__node"
                to={`/app/nodes/${encodeURIComponent(node.id)}`}
                data-state={node.status}
                data-routed={node.status === 'healthy' ? 'true' : 'false'}
                data-testid="map-node"
              >
                <span className="sw-map__top">
                  <strong className="dk-mono">{node.name}</strong>
                  <span className="dk-mono sw-map__mem">
                    {memory ? (memory.kind === 'gpu' ? `${formatBytes(memory.used)} / ${formatBytes(memory.total)}` : t('map.cpuOnly')) : ''}
                  </span>
                </span>
                <NodeState node={node} />
                <span className="sw-map__models">
                  {node.node_type === 'agent'
                    ? t('map.agent')
                    : names === null ? ' '
                      : names.length === 0 ? t('map.noModels')
                        : names.join(', ')}
                </span>
              </Link>
            </li>
          )
        })}
      </ul>
      {nodes.length > shown.length && (
        <p className="sw-map__more">{t('map.more', { count: nodes.length - shown.length })}</p>
      )}
    </div>
  )
}
