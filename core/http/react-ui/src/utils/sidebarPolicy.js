import { operateConsole, isConsoleItemVisible } from '../components/console/consoleConfig'

// The Operate > Cluster group, surfaced as a pinned top-of-sidebar quick-access
// group when the admin is running a cluster (NATS-distributed) or a P2P swarm.
// Items are filtered through the SAME gate as everywhere else, so e.g. in a
// p2p-only deployment Nodes/Scheduling (feature: 'distributed') drop out and
// only Swarm remains. Returns [] when the pin does not apply.
export function clusterPinItems(auth, deployment) {
  if (!auth.isAdmin) return []
  if (!deployment.distributed && !deployment.p2pEnabled) return []
  const group = operateConsole.groups.find(g => g.titleKey === 'operate.cluster')
  if (!group) return []
  return group.items.filter(item => isConsoleItemVisible(item, auth))
}

// In the cluster cells the Create group defaults collapsed so the pinned
// cluster group leads. Users can still expand it; their stored choice wins.
export function shouldCollapseCreate(auth, deployment) {
  return !!auth.isAdmin && (!!deployment.distributed || !!deployment.p2pEnabled)
}
