/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useOutletContext, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useNodeList } from '../hooks/useSwarm'
import AdminPage from '../components/admin/AdminPage'
import AddWorkerFlow from '../components/swarm/AddWorkerFlow'
import CommandBlock from '../components/swarm/CommandBlock'
import LoadingSpinner from '../components/LoadingSpinner'
import { Button } from '../components/ui'
import { DISTRIBUTED_COMMAND } from '../utils/swarm'
import './swarm.css'

// "Add worker": which connections the worker uses, one command to run on the new
// machine with a join token, and a line that updates when it arrives. On an
// install that is not distributed yet it starts with the step that turns that
// on; a model-split worker needs no cluster and has its own steps.
export default function AddNode() {
  const { addToast } = useOutletContext()
  const { t } = useTranslation('cluster')
  const { nodes, status, refetch } = useNodeList({ interval: 3000 })
  const [params] = useSearchParams()
  const shard = params.get('join') === 'shard'

  return (
    <AdminPage
      title={t('add.title')}
      supporting={t('add.supporting')}
      width="full"
      className="aw-page"
      data-testid="add-node-page"
      action={<Button to="/app/nodes">{t('add.cancel')}</Button>}
    >
      {status === 'loading' && <div className="sw-loading"><LoadingSpinner size="lg" /></div>}
      {status === 'single' && !shard && <EnableDistributed addToast={addToast} />}
      {status !== 'loading' && (status !== 'single' || shard) && (
        <AddWorkerFlow nodes={status === 'single' ? [] : nodes} addToast={addToast} onApproved={refetch} initialMethod={shard ? 'shard' : 'worker'} single={status === 'single'} />
      )}
    </AdminPage>
  )
}

// The step before the first one on a server that is not distributed: turn the
// distributed mode on. A worker needs a cluster to join.
function EnableDistributed({ addToast }) {
  const { t } = useTranslation('swarm')
  const { t: tc } = useTranslation('cluster')
  return (
    <section className="aw-panel" data-testid="add-distributed">
      <h2 className="aw-panel__title">{t('add.distributed.title')}</h2>
      <p className="aw-panel__note">{t('add.distributed.sub')}</p>
      <CommandBlock command={DISTRIBUTED_COMMAND} label={t('add.distributed.title')} addToast={addToast} />
      <p className="aw-hint">
        {t('add.distributed.docs')}{' '}
        <a className="dk-link" href="https://localai.io/features/distributed-mode/" target="_blank" rel="noopener noreferrer">{t('add.distributed.docsLink')}</a>
      </p>
      <p className="aw-hint">{tc('add.single.splitNote')} <Button variant="ghost" size="sm" to="/app/nodes/add?join=shard">{tc('add.split.action')}</Button></p>
    </section>
  )
}
