import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { nodesApi, p2pNetApi, p2pSplitApi } from '../utils/api'
import { shortPeer } from '../utils/p2p'
import { useSplitWorkers } from './useSplitWorkers'
import { useLoad } from '../components/ui'
import { nodeChoices, relayOf, summarize, topCallout, unattached } from '../components/swarm/splitWorkersModel'

// Everything the page needs to manage the model-split workers: the workers
// (read every 5 s), the backend nodes they can attach to, the callout to show,
// and the actions: use a slow path anyway, attach to a node, revoke, the relay
// limits and the setup of a split server. The Workers roster and the panel of a
// single install both use it, so a worker behaves the same wherever it is.
//
//   nodes   the node list the page already holds. Without it the hook reads
//           the list itself (the panel of a single install has none).
//   enabled false stops the reading of the workers, for a page that does not
//           show them yet.
export function useSplitManagement({ addToast, nodes: given, enabled = true }) {
  const { t } = useTranslation('swarm')
  const split = useSplitWorkers(enabled)
  const own = useLoad(() => (given === undefined && split.distributed ? nodesApi.list() : Promise.resolve([])), [split.distributed, given === undefined])
  const nodeList = given !== undefined ? given : own.data
  const nodesReady = given !== undefined ? true : own.status === 'ready'
  const choices = nodeChoices(nodeList)
  const [busy, setBusy] = useState(null)
  const [revoking, setRevoking] = useState(null)
  const [revokeBusy, setRevokeBusy] = useState(false)
  const [limitsOf, setLimitsOf] = useState(null)
  const [setup, setSetup] = useState(false)

  const sum = summarize(split.workers)
  const idle = unattached(split.workers, split.distributed)
  const noServer = split.distributed && nodesReady && choices.length === 0
  const waiting = split.workers.filter(w => w.node_id && w.node_offline)
  const refused = split.workers.filter(w => relayOf(w)?.key === 'refused')
  const callout = topCallout({
    noServer, refused: refused.length > 0, limited: sum.limited.length > 0, cut: sum.cut.length > 0,
    offline: waiting.length > 0, unattached: idle.length > 0, slow: sum.slow.length > 0,
  })
  const nameOf = worker => worker.label || shortPeer(worker.peer_id)

  const setSlow = async (worker, allow) => {
    setBusy(worker.id)
    try {
      await p2pSplitApi.setSlowPath(worker.id, allow)
      addToast?.(t(allow ? 'split.toast.used' : 'split.toast.stopped', { name: nameOf(worker) }), 'success')
      await split.refetch()
    } catch (error) {
      addToast?.(error.message, 'error')
    } finally {
      setBusy(null)
    }
  }
  const attach = async (worker, nodeId) => {
    setBusy(worker.id)
    const node = choices.find(n => n.id === nodeId)?.name
    try {
      await p2pSplitApi.setNode(worker.id, nodeId)
      addToast?.(nodeId ? t('split.toast.attached', { name: nameOf(worker), node }) : t('split.toast.detached', { name: nameOf(worker) }), 'success')
      await split.refetch()
    } catch (error) {
      addToast?.(error.message, 'error')
    } finally {
      setBusy(null)
    }
  }
  const revoke = async () => {
    setRevokeBusy(true)
    try {
      await p2pNetApi.revokeDevice(revoking.id)
      addToast?.(t('split.toast.revoked', { name: nameOf(revoking) }), 'success')
      setRevoking(null)
      await split.refetch()
    } catch (error) {
      addToast?.(error.message, 'error')
    } finally {
      setRevokeBusy(false)
    }
  }

  return {
    split, nodeList, choices, sum, idle, noServer, waiting, refused, callout, busy, nameOf,
    setSlow, attach, revoke, revoking, askRevoke: setRevoking, cancelRevoke: () => setRevoking(null), revokeBusy,
    limitsOf, openLimits: setLimitsOf, closeLimits: () => setLimitsOf(null),
    setup, openSetup: () => setSetup(true), closeSetup: () => setSetup(false),
    reloadNodes: own.reload,
  }
}
