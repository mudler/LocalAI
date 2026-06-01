import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import StatCard from './StatCard'
import { nodesApi, p2pApi } from '../utils/api'

// ClusterSummary shows merged totals across both transports. Self-contained
// (own lightweight fetch) so the page composes without lifting state out of the
// two large section components.
export default function ClusterSummary({ distributedEnabled, p2pEnabled }) {
  const { t } = useTranslation('admin')
  const [nats, setNats] = useState({ nodes: 0, inFlight: 0 })
  const [swarm, setSwarm] = useState({ online: 0, total: 0 })

  useEffect(() => {
    let active = true
    async function load() {
      if (distributedEnabled) {
        try {
          const list = await nodesApi.list()
          const nodes = Array.isArray(list) ? list : (list?.nodes ?? [])
          if (active) setNats({ nodes: nodes.length, inFlight: nodes.reduce((a, n) => a + (n.in_flight_count || 0), 0) })
        } catch { /* leave zeros */ }
      }
      if (p2pEnabled) {
        try {
          const stats = await p2pApi.getStats()
          const online = (stats?.federated?.online || 0) + (stats?.llama_cpp_workers?.online || 0) + (stats?.mlx_workers?.online || 0)
          const total = (stats?.federated?.total || 0) + (stats?.llama_cpp_workers?.total || 0) + (stats?.mlx_workers?.total || 0)
          if (active) setSwarm({ online, total })
        } catch { /* leave zeros */ }
      }
    }
    load()
    return () => { active = false }
  }, [distributedEnabled, p2pEnabled])

  return (
    <div className="stat-grid" style={{ marginBottom: 'var(--spacing-lg)' }}>
      {distributedEnabled && <StatCard icon="fas fa-network-wired" label={t('cluster.summary.nodes', 'Distributed nodes')} value={nats.nodes} />}
      {distributedEnabled && <StatCard icon="fas fa-bolt" label={t('cluster.summary.inFlight', 'In-flight requests')} value={nats.inFlight} />}
      {p2pEnabled && <StatCard icon="fas fa-circle-nodes" label={t('cluster.summary.peers', 'Swarm peers online')} value={`${swarm.online}/${swarm.total}`} />}
    </div>
  )
}
