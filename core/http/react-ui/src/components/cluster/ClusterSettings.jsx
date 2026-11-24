/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import { useClusterSettings } from '../../hooks/useClusterSettings'
import { SECTIONS } from '../../utils/clusterSettings'
import { EmptyState, LoadError, SkeletonRows } from '../ui'
import { BusSection, JoiningSection, NatsSection, WeightsSection } from './ClusterSections'
import './cluster.css'

const PARTS = { bus: BusSection, nats: NatsSection, weights: WeightsSection, joining: JoiningSection }

// The settings of the cluster as one form: the broadcast bus and the frontends,
// the NATS addresses, the preference between connections and how a worker is
// approved. One component, drawn in two places: the side sheet of the Workers
// page and the Cluster group of Settings. Both read and write the same cluster
// settings, so a change made in one is what the other shows.
//
//   sections   the sections to draw, in the order of SECTIONS (default all);
//              Settings search passes the ones that match
export default function ClusterSettings({ sections = SECTIONS }) {
  const { t } = useTranslation('cluster')
  const { status, data, error, reload } = useClusterSettings()

  if (status === 'loading') return <SkeletonRows />
  if (status === 'unavailable') return <EmptyState variant="unavailable" title={t('unavailable.title')} body={t('unavailable.body')} />
  if (!data) return <LoadError title={t('loadFailed')} error={{ message: error }} onRetry={reload} />
  return (
    <div className="cl" data-testid="cluster-settings">
      {SECTIONS.filter(id => sections.includes(id)).map(id => {
        const Part = PARTS[id]
        return <Part key={id} settings={data} onSaved={reload} />
      })}
    </div>
  )
}
