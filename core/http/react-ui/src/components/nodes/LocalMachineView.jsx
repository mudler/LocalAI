/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import HostOverview from './HostOverview'
import LocalRunningModels from './LocalRunningModels'
import { useLocalMachine } from '../../hooks/useLocalMachine'
import { hostAsNode } from '../../utils/localHost'
import { summarizeFleet } from '../../utils/nodeFleet'
import Icon from '../Icon'
import '../../pages/operate.css'

// What the Nodes route shows when distributed mode is off: the host and what is
// loaded on it. The host is treated as a fleet of one so the capacity readings
// and the running-models table match what a cluster operator sees. The setup
// for more than one machine stays behind a button, since most single-node
// installs are single-node on purpose.
export default function LocalMachineView({ addToast, scaleOut }) {
  const { t } = useTranslation('operate')
  const machine = useLocalMachine()
  const [showScaleOut, setShowScaleOut] = useState(false)
  const summary = useMemo(() => {
    const node = hostAsNode(machine.resources)
    return summarizeFleet(node ? [node] : [])
  }, [machine.resources])

  return (
    <div className="page op-page op-machine" data-testid="local-machine">
      <header className="op-machine__head">
        <div>
          <h1 className="op-status__title">{t('machine.title')}</h1>
          <p className="op-status__sub">{t('machine.subtitle')}</p>
        </div>
        <button
          type="button"
          className="dk-btn dk-btn--secondary"
          aria-expanded={showScaleOut}
          onClick={() => setShowScaleOut(value => !value)}
        >
          <Icon name="plus" /> {showScaleOut ? t('machine.hideScaleOut') : t('machine.scaleOut')}
        </button>
      </header>
      {showScaleOut && scaleOut}
      <HostOverview summary={summary} models={machine.rows} ramTotal={machine.resources?.ram?.total} resources={machine.resources} />
      <LocalRunningModels machine={machine} addToast={addToast} />
    </div>
  )
}
