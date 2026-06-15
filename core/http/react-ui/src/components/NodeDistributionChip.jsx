import { useRef, useState } from 'react'
import Popover from './Popover'

// NodeDistributionChip shows where something is installed/loaded across a
// cluster. Used by both Manage → Backends (per-row Nodes column, data =
// gallery NodeBackendRef with version/digest) and by the Models tab (data =
// LoadedOn with state/status). Supports arbitrary cluster size — small
// clusters render node-name chips inline, larger clusters collapse to a
// summary chip and reveal the full per-node table in a popover on click.
//
// Field names are intentionally forgiving: both {node_name, node_status} and
// {NodeName, NodeStatus} are supported so the component works whether it's
// reading directly off the JSON or off a hydrated class.
//
// Props:
//   nodes:             array of node refs (see shape below).
//   compactThreshold:  max nodes to render inline before collapsing (default 3).
//   context:           'backends' (default) shows version/digest; 'models'
//                      shows state.
//   emptyLabel:        what to render when nodes is empty (default "—").
export default function NodeDistributionChip({
  nodes,
  compactThreshold = 3,
  context = 'backends',
  emptyLabel = '—',
}) {
  const triggerRef = useRef(null)
  const [open, setOpen] = useState(false)

  const list = Array.isArray(nodes) ? nodes : []
  if (list.length === 0) {
    return <span className="cell-muted">{emptyLabel}</span>
  }

  const getName = n => n.node_name ?? n.NodeName ?? ''
  const getStatus = n => n.node_status ?? n.NodeStatus ?? ''
  const getState = n => n.state ?? n.State ?? ''
  const getVersion = n => n.version ?? n.Version ?? ''
  const getDigest = n => n.digest ?? n.Digest ?? ''

  // Inline mode: render every node as its own chip. Good for small clusters
  // where seeing the names directly is more useful than a summary.
  if (list.length <= compactThreshold) {
    return (
      <div className="badge-row">
        {list.map(n => {
          const status = getStatus(n)
          const variant = status === 'healthy' ? 'badge-success'
            : status === 'draining' ? 'badge-info'
            : 'badge-warning'
          const title = context === 'models'
            ? `${getName(n)} — ${getState(n)} (${status})`
            : `${getName(n)} — ${status}${getVersion(n) ? ` · v${getVersion(n)}` : ''}`
          return (
            <span key={n.node_id ?? n.NodeID ?? getName(n)} className={`badge ${variant}`} title={title}>
              <i className="fas fa-server" /> {getName(n)}
            </span>
          )
        })}
      </div>
    )
  }

  // Summary mode for anything bigger. Count unhealthy/offline explicitly so
  // the chip tells an operator at-a-glance whether to click in. "Drift" for
  // backends = more than one (version, digest) tuple across healthy nodes.
  const total = list.length
  const offline = list.filter(n => {
    const s = getStatus(n)
    return s !== 'healthy' && s !== 'draining'
  }).length
  const drift = context === 'backends' ? countDrift(list) : 0
  const severity = offline > 0 || drift > 0 ? 'badge-warning' : 'badge-info'

  return (
    <>
      <button
        ref={triggerRef}
        type="button"
        className={`badge ${severity} chip-trigger`}
        aria-expanded={open}
        aria-haspopup="dialog"
        onClick={e => { e.stopPropagation(); setOpen(v => !v) }}
      >
        <i className="fas fa-server" />
        {' '}on {total} node{total === 1 ? '' : 's'}
        {offline > 0 ? ` · ${offline} offline` : ''}
        {drift > 0 ? ` · ${drift} drift` : ''}
      </button>
      <Popover
        anchor={triggerRef}
        open={open}
        onClose={() => setOpen(false)}
        ariaLabel={context === 'models' ? 'Model distribution' : 'Backend distribution'}
      >
        <div className="popover__header">
          <strong>Installed on {total} node{total === 1 ? '' : 's'}</strong>
          {offline > 0 && <span className="badge badge-warning">{offline} offline</span>}
          {drift > 0 && <span className="badge badge-warning">{drift} drift</span>}
        </div>
        <div className="popover__scroll">
          <table className="table popover__table">
            <thead>
              <tr>
                <th>Node</th>
                <th>Status</th>
                {context === 'models' ? <th>State</th> : <>
                  <th>Version</th>
                  <th>Digest</th>
                </>}
              </tr>
            </thead>
            <tbody>
              {list.map(n => (
                <tr key={n.node_id ?? n.NodeID ?? getName(n)}>
                  <td className="cell-mono">{getName(n)}</td>
                  <td>
                    <span className={`badge ${getStatus(n) === 'healthy' ? 'badge-success' : 'badge-warning'}`}>
                      {getStatus(n)}
                    </span>
                  </td>
                  {context === 'models' ? (
                    <td className="cell-mono">{getState(n) || '—'}</td>
                  ) : (
                    <>
                      <td className="cell-mono">{getVersion(n) ? `v${getVersion(n)}` : '—'}</td>
                      <td className="cell-mono cell-truncate" title={getDigest(n)}>
                        {getDigest(n) ? shortenDigest(getDigest(n)) : '—'}
                      </td>
                    </>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Popover>
    </>
  )
}

// countDrift counts nodes whose (version, digest) disagrees with the cluster
// majority. Mirrors the backend summarizeNodeDrift logic so the UI number
// matches what CheckUpgradesAgainst emits in UpgradeInfo.NodeDrift.
function countDrift(nodes) {
  if (nodes.length <= 1) return 0
  const counts = new Map()
  for (const n of nodes) {
    const key = `${n.version ?? n.Version ?? ''}|${n.digest ?? n.Digest ?? ''}`
    counts.set(key, (counts.get(key) || 0) + 1)
  }
  if (counts.size === 1) return 0 // unanimous
  let topKey = ''
  let topCount = 0
  for (const [k, v] of counts.entries()) {
    if (v > topCount) { topKey = k; topCount = v }
  }
  return nodes.length - topCount
}

// shortenDigest trims a full OCI digest to the common 12-char form used in
// docker/oci tooling. Falls back to the raw value if it doesn't match.
function shortenDigest(digest) {
  const m = /^(sha\d+:)?([a-f0-9]+)$/i.exec(digest)
  if (!m) return digest
  const hex = m[2]
  return (m[1] ?? '') + hex.slice(0, 12)
}
