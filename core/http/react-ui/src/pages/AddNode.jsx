/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link, useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useNodeList } from '../hooks/useSwarm'
import AddNodeFlow from '../components/swarm/AddNodeFlow'
import LoadingSpinner from '../components/LoadingSpinner'
import Icon from '../components/Icon'
import './swarm.css'

// "Add a node": one command to run on the new machine, and a line that updates
// when it arrives. On an install that is not distributed yet it starts with the
// step that turns that on.
export default function AddNode() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation('swarm')
  const { nodes, status, refetch } = useNodeList({ interval: 3000 })
  const single = status === 'single'
  const [params] = useSearchParams()

  return (
    <div className="page page--wide sw-page sw-page--narrow" data-testid="add-node-page">
      <Link className="sw-back" to="/app/nodes"><Icon name="arrow-left" /> {t('add.back')}</Link>
      <header className="sw-head">
        <h1 className="sw-title">{t('add.title')}</h1>
        <p className="sw-note">{t('add.sub')}</p>
      </header>
      {status === 'loading'
        ? <div className="sw-loading"><LoadingSpinner size="lg" /></div>
        : <AddNodeFlow nodes={single ? null : nodes} single={single} addToast={addToast} onApproved={refetch} initialMethod={params.get('join') === 'peer' ? 'peer' : params.get('join') === 'shard' ? 'shard' : 'worker'} />}
    </div>
  )
}
