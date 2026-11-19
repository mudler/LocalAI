/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { nodesApi } from '../../utils/api'
import { useSplitWorkers } from '../../hooks/useSplitWorkers'
import { Button } from '../ui'
import ConfirmDialog from '../ConfirmDialog'
import SplitServerSetup from './SplitServerSetup'

// The split server switch of one node, in the node inspector of the Nodes
// page. Turning it on opens the quick setup, which says what changes on the
// node and asks for a confirm click; turning it off asks too, because it
// detaches the node's model-split workers. Hidden when this instance does not
// serve split workers (P2P mode is not Connect and share) and for agents.
export default function SplitServerSwitch({ node, nodes, onChanged, addToast }) {
  const { t } = useTranslation('swarm')
  const split = useSplitWorkers()
  const [setup, setSetup] = useState(false)
  const [clearing, setClearing] = useState(false)
  const [busy, setBusy] = useState(false)
  if (split.status !== 'ready' || node.node_type === 'agent') return null
  const attached = split.workers.filter(w => w.node_id === node.id)

  const clear = async () => {
    setBusy(true)
    try {
      const res = await nodesApi.setSplitServer(node.id, false)
      addToast?.(t('splitServer.toast.cleared', { node: node.name, count: res?.detached || 0 }), 'success')
      setClearing(false)
      await split.refetch()
      onChanged?.()
    } catch (error) {
      addToast?.(error.message, 'error')
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      {node.split_server
        ? <Button size="sm" variant="secondary" onClick={() => setClearing(true)} data-testid="split-server-clear">{t('splitServer.clear')}</Button>
        : <Button size="sm" icon="plus" onClick={() => setSetup(true)} data-testid="split-server-make">{t('splitServer.make')}</Button>}
      {setup && (
        <SplitServerSetup
          nodes={nodes} workers={split.workers} initial={node.id} addToast={addToast}
          onClose={() => setSetup(false)} onDone={() => { split.refetch(); onChanged?.() }}
        />
      )}
      <ConfirmDialog
        open={clearing}
        title={t('splitServer.clearTitle', { node: node.name })}
        message={t('splitServer.clearBody', { count: attached.length })}
        confirmLabel={t('splitServer.clearConfirm')}
        pendingLabel={t('splitServer.clearing')}
        pending={busy}
        danger
        onConfirm={clear}
        onCancel={() => setClearing(false)}
      />
    </>
  )
}
