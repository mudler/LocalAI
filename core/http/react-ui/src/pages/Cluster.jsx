import { useTranslation } from 'react-i18next'
import { useDistributedMode } from '../hooks/useDistributedMode'
import { useP2PMode } from '../hooks/useP2PMode'
import ClusterSection from '../components/ClusterSection'
import ClusterSummary from '../components/ClusterSummary'
import Nodes from './Nodes'
import P2P from './P2P'

export default function Cluster() {
  const { t } = useTranslation('admin')
  const distributed = useDistributedMode()
  const p2p = useP2PMode()

  const loading = distributed.loading || p2p.loading
  const nothingEnabled = !loading && !distributed.enabled && !p2p.enabled

  return (
    <div className="page page--wide">
      <div className="page-header">
        <h1 className="page-title"><i className="fas fa-network-wired" /> {t('cluster.title', 'Cluster')}</h1>
        <p className="page-subtitle">{t('cluster.subtitle', 'Distributed and peer-to-peer nodes serving this instance')}</p>
      </div>

      {!loading && <ClusterSummary distributedEnabled={distributed.enabled} p2pEnabled={p2p.enabled} />}

      {distributed.enabled && (
        <ClusterSection icon="fas fa-network-wired" title={t('cluster.distributed.title', 'Distributed (NATS)')} defaultOpen>
          <Nodes embedded />
        </ClusterSection>
      )}

      {p2p.enabled && (
        <ClusterSection icon="fas fa-circle-nodes" title={t('cluster.swarm.title', 'Swarm (p2p)')} defaultOpen={!distributed.enabled}>
          <P2P embedded />
        </ClusterSection>
      )}

      {nothingEnabled && (
        <div className="card" style={{ padding: 'var(--spacing-lg)', textAlign: 'center', color: 'var(--color-text-muted)' }}>
          {t('cluster.empty', 'No distributed or p2p clustering is enabled. Start LocalAI in distributed or federated/p2p mode to manage cluster nodes here.')}
        </div>
      )}
    </div>
  )
}
