// The per-node view of a backend update, as pure functions so the node unit
// tests drive them without a browser.
//
// An item of the backend read model (GET /api/backends/updates) carries
// `nodes[]` in a cluster: every node that reports the backend, with its
// version, a drift flag and the models loaded with that backend. This module
// says, for one node, whether it is current, behind or only different from
// the cluster, builds the plan of an update across nodes, and counts what the
// plan stops.
//
// The model list is best effort. The registry records the backend a model was
// loaded with, which can be empty or name a meta backend, so an empty list
// never proves that nothing would stop. The words must say that.
import { versionLabel, shortDigest } from './backendRows.js'

export const NODE_STATES = ['current', 'behind', 'differs', 'unreachable']

// A node takes an update only when it is healthy. A status that is missing is
// treated as healthy: the read model leaves it out for nodes it cannot judge.
export function nodeReachable(node) {
  const s = String((node && node.status) || '').toLowerCase()
  return s === '' || s === 'healthy'
}

// Whether a node already has the build the gallery offers.
export function nodeHasAvailable(item, node) {
  if (item.availableVersion) {
    if (node.version !== item.availableVersion) return false
    return !(item.availableDigest && node.digest && node.digest !== item.availableDigest)
  }
  if (item.availableDigest) return Boolean(node.digest) && node.digest === item.availableDigest
  return false
}

// current   has the build on offer
// behind    has another build and the cluster as a whole is behind (or the
//           backend is flagged stale), so this node needs the update
// differs   has another build than the rest of the cluster; the update
//           realigns it. It is not called "behind": it can be newer.
// unreachable  cannot take an update now (not healthy)
export function nodeStateOf(item, node) {
  if (!nodeReachable(node)) return 'unreachable'
  if (item.state !== 'stale' && nodeHasAvailable(item, node)) return 'current'
  if (item.state === 'drift') return 'differs'
  if (item.state === 'update' && node.drift && node.version !== item.installedVersion) return 'differs'
  return 'behind'
}

const labelOf = (version, digest) => versionLabel(version) || shortDigest(digest)

// The line of the plan for one node of one backend.
export function entryOf(item, node) {
  return {
    key: `${item.name}@${node.nodeId}`,
    itemId: item.id,
    backend: item.name,
    nodeId: node.nodeId,
    nodeName: node.nodeName || node.nodeId,
    from: labelOf(node.version, node.digest),
    to: labelOf(item.availableVersion, item.availableDigest),
    toVersion: item.availableVersion || '',
    toDigest: item.availableDigest || '',
    models: Array.isArray(node.models) ? node.models : [],
    state: nodeStateOf(item, node),
  }
}

// The nodes of one backend with their state and what each would stop.
// Only gallery backends have a node view.
export function nodeView(item) {
  const nodes = (item && item.kind === 'gallery' && Array.isArray(item.nodes) ? item.nodes : [])
    .map(n => entryOf(item, n))
    .sort((a, b) => a.nodeName.localeCompare(b.nodeName))
  const todo = nodes.filter(n => n.state === 'behind' || n.state === 'differs')
  return {
    nodes,
    todo,
    behind: nodes.filter(n => n.state === 'behind').length,
    differs: nodes.filter(n => n.state === 'differs').length,
  }
}

// Whether a row of the Updates page gets a "Nodes" action: a gallery backend
// that more than one node reports. A single node has no per-node view.
export function hasNodeView(row) {
  return Boolean(row && row.kind === 'gallery' && Array.isArray(row.nodes) && row.nodes.length > 1)
}

// The entries an update across nodes would run: every gallery item that has
// nodes, optionally of one backend, optionally on some nodes only, and only
// the nodes that need it. Entries are ordered by backend and node name, so a
// run is predictable.
export function planEntries(items, { backend, nodeIds, itemIds } = {}) {
  const out = []
  for (const item of items || []) {
    if (!item || item.kind !== 'gallery') continue
    if (backend && item.name !== backend) continue
    if (itemIds && !itemIds.has(item.id)) continue
    for (const e of nodeView(item).todo) {
      if (nodeIds && !nodeIds.has(e.nodeId)) continue
      out.push(e)
    }
  }
  return out.sort((a, b) => a.backend.localeCompare(b.backend) || a.nodeName.localeCompare(b.nodeName))
}

// What a plan stops. `models` counts the models the registry names;
// `unknownNodes` counts the entries that name none, which is not "none
// stop".
export function planTotals(entries) {
  let models = 0
  let unknown = 0
  const nodes = new Set()
  const backends = new Set()
  for (const e of entries) {
    nodes.add(e.nodeId)
    backends.add(e.backend)
    if (e.models.length === 0) unknown += 1
    else models += e.models.length
  }
  return { updates: entries.length, nodes: nodes.size, backends: backends.size, models, unknown }
}

// Which words the stop line and the confirm button use.
//   none     no entry names a model
//   known    every entry names its models
//   partial  some entries name models, the others name none
// For `none` and `partial` the words say what is not known.
export function stopKind(totals) {
  if (totals.updates === 0) return 'none'
  if (totals.unknown === 0) return 'known'
  return totals.models === 0 ? 'none' : 'partial'
}

// Did the check that followed a node update show the node on the new build?
// `snapshot` is the answer of POST /api/backends/updates/check. The backend
// leaving the list altogether means every node is current. A node that is no
// longer reported is not confirmed.
export function nodeConfirmed(entry, snapshot) {
  const items = snapshot && Array.isArray(snapshot.items) ? snapshot.items : null
  if (!items) return 'unconfirmed'
  const source = snapshot.sources && snapshot.sources.gallery
  if (!source || source.state === 'failed' || source.state === 'never') return 'unconfirmed'
  const item = items.find(i => i.id === entry.itemId)
  if (!item) return 'done'
  const node = (item.nodes || []).find(n => n.nodeId === entry.nodeId)
  if (!node) return 'unconfirmed'
  if (item.availableVersion || item.availableDigest) {
    return nodeHasAvailable(item, node) ? 'done' : 'unchanged'
  }
  return 'unchanged'
}

// The plan of the Nodes page's bulk update. The page picks the (backend,
// node) pairs (utils/swarm.updateTargets); this gives each pair its line, with
// the version the node reports and the models it names when the read model
// knows the node. A pair the read model does not know still gets a line, with
// no models named: that reads as "none recorded", never as "none stop".
export function entriesFromPairs({ pairs, items, nodes, upgrades }) {
  const nameOf = new Map((nodes || []).map(n => [n.id, n.name || n.id]))
  const out = []
  for (const pair of pairs || []) {
    const item = (items || []).find(i => i.kind === 'gallery' && i.name === pair.backend)
    const node = item && (item.nodes || []).find(n => n.nodeId === pair.nodeId)
    if (item && node) {
      out.push({ ...entryOf(item, node), nodeName: nameOf.get(pair.nodeId) || node.nodeName || pair.nodeId, state: 'behind' })
      continue
    }
    const u = upgrades && upgrades[pair.backend]
    out.push({
      key: `${pair.backend}@${pair.nodeId}`,
      itemId: `gallery:${pair.backend}`,
      backend: pair.backend,
      nodeId: pair.nodeId,
      nodeName: nameOf.get(pair.nodeId) || pair.nodeId,
      from: '',
      to: versionLabel(u && u.available_version),
      toVersion: (u && u.available_version) || '',
      toDigest: '',
      models: [],
      state: 'behind',
    })
  }
  return out
}

// A backend row that nodes report. An update of it reaches every worker that
// has it, and a worker stops the running models of the backend first, so it
// never starts without the confirmation of the node dialog.
export function isClusterRow(row) {
  return Boolean(row && row.kind === 'gallery' && Array.isArray(row.nodes) && row.nodes.length > 0)
}

// Splits what the Updates page was asked to update into the nodes to confirm
// and the rest. `rows` are the page rows, `selected` the ids asked for,
// `items` the backend read model. The cluster rows become the entries of one
// combined dialog; every other row (single machine, library, add-on) keeps its
// own way. A cluster row with no node left to update adds no entry and does
// not fall back to the plain update.
export function splitUpdate({ rows, selected, items }) {
  const ids = new Set()
  const clusterIds = new Set()
  for (const row of rows || []) {
    if (!selected.has(row.id)) continue
    if (isClusterRow(row)) clusterIds.add(row.id)
    else ids.add(row.id)
  }
  return { entries: planEntries(items, { itemIds: clusterIds }), restIds: ids }
}
