// What the proposal card of a Kubernetes write shows, from the plain data the
// host hands it. No React, so a node test drives it. The facts come from the
// add-on's preview: the diff of a dry run, the pods a drain evicts, the argv
// of an exec. They are shown as they are, never shortened or rewritten.
import { splitKey } from './kube.js'

const str = (v) => (typeof v === 'string' ? v : '')
const int = (v) => (Number.isInteger(v) ? v : null)
const text = (v) => (typeof v === 'string' ? v : Number.isFinite(v) ? String(v) : '')
const strings = (v) => (Array.isArray(v) ? v.filter((x) => typeof x === 'string') : [])

// The lines of a unified diff, each with its kind: head (--- and +++), hunk
// (@@), add, del or same.
export function diffLines(diff) {
  const lines = str(diff).split('\n')
  if (lines.length && lines[lines.length - 1] === '') lines.pop()
  return lines.map((l) => {
    if (l.startsWith('+++') || l.startsWith('---')) return { kind: 'head', text: l }
    if (l.startsWith('@@')) return { kind: 'hunk', text: l }
    if (l.startsWith('+')) return { kind: 'add', text: l }
    if (l.startsWith('-')) return { kind: 'del', text: l }
    return { kind: 'same', text: l }
  })
}

export function proposalView(tool, args, sample) {
  const a = args && typeof args === 'object' ? args : {}
  const s = sample && typeof sample === 'object' ? sample : {}
  const cluster = str(s.cluster) || str(a.cluster)
  const { server, context } = splitKey(cluster)
  const skipped = (Array.isArray(s.skipped) ? s.skipped : [])
    .filter((x) => x && typeof x.pod === 'string')
    .map((x) => ({ pod: x.pod, reason: str(x.reason) }))
  return {
    tool: str(tool),
    cluster,
    server,
    context,
    kind: str(s.kind) || str(a.kind),
    namespace: str(s.namespace) || str(a.namespace),
    name: str(s.name) || str(a.name) || str(a.pod),
    node: str(s.node) || str(a.node),
    from: text(s.from),
    to: text(s.to),
    change: str(s.change),
    lines: diffLines(s.diff),
    diffCut: s.diffCut === true,
    patchType: str(s.patchType) || str(a.patchType),
    pods: strings(s.pods),
    skipped,
    command: Array.isArray(s.command) ? strings(s.command) : strings(a.command),
    container: str(s.container) || str(a.container),
    timeoutSeconds: int(s.timeoutSeconds) ?? int(a.timeoutSeconds),
    gracePeriodSeconds: int(s.gracePeriodSeconds) ?? int(a.gracePeriodSeconds),
    cordoned: typeof s.cordoned === 'boolean' ? s.cordoned : null,
  }
}

// What the card names as the target: kind namespace/name, or the node.
export function targetOf(v) {
  if (v.node && (v.kind === 'Node' || !v.name)) return v.node
  const path = v.namespace ? `${v.namespace}/${v.name}` : v.name
  return v.kind ? `${v.kind} ${path}` : path
}
