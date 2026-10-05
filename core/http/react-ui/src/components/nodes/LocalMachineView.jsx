import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import PageHeader from '../PageHeader'
import HostOverview from './HostOverview'
import LocalRunningModels from './LocalRunningModels'
import { useLocalMachine } from '../../hooks/useLocalMachine'
import { hostAsNode } from '../../utils/localHost'
import { summarizeFleet } from '../../utils/nodeFleet'

// What the Nodes page shows when distributed mode is off. It used to be only
// an "enable distributed mode" card, which left a single-node install with no
// page listing what was loaded and no way to stop it short of the API. The
// host is treated as a fleet of one so the gauges and the running-models table
// match what a cluster operator sees; the distributed setup moves behind a
// button, since most single-node installs are single-node on purpose.
export default function LocalMachineView({ addToast, scaleOut }) {
  const { t } = useTranslation('admin')
  const machine = useLocalMachine()
  const [showScaleOut, setShowScaleOut] = useState(false)
  const summary = useMemo(() => {
    const node = hostAsNode(machine.resources)
    return summarizeFleet(node ? [node] : [])
  }, [machine.resources])

  return (
    <div className="page page--wide nodes-fleet-page local-machine-page" data-testid="local-machine">
      <PageHeader className="nodes-fleet-page__header" eyebrow={null} title={t('localMachine.title')} supporting={t('localMachine.subtitle')}
        actions={<button type="button" className="btn btn-secondary btn-sm" aria-expanded={showScaleOut} onClick={() => setShowScaleOut(value => !value)}>
          <i className="fas fa-network-wired" aria-hidden="true" /> {showScaleOut ? t('localMachine.hideScaleOut') : t('localMachine.scaleOut')}
        </button>} />
      {showScaleOut && scaleOut}
      <HostOverview summary={summary} models={machine.rows} ramTotal={machine.resources?.ram?.total} />
      <LocalRunningModels machine={machine} addToast={addToast} />
    </div>
  )
}
