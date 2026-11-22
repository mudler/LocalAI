/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import { busState } from '../../utils/clusterSettings'
import { StatusChip } from '../ui'

// The bus of this frontend and how many frontends share the cluster, as one
// compact pill beside the page title: "NATS connected · 2 frontends". It says
// nothing until the cluster settings are read, and nothing on a frontend that
// has none.
export default function ConnectivityPill({ settings }) {
  const { t } = useTranslation('cluster')
  if (!settings) return null
  const bus = busState(settings.bus)
  const count = (settings.replicas || []).length
  return (
    <StatusChip tone={bus.tone} dot testId="connectivity-pill">
      {t(`bus.state.${bus.key}`)}{count > 0 && ` · ${t('pill.frontends', { count })}`}
    </StatusChip>
  )
}
