// The model of the policy editor. No React, so a node test drives it. The
// add-on keeps one policy per cluster key and is the authority: it checks the
// policy again when it saves and enforces it on every call. These helpers
// repeat its rules only to show a problem next to the field.

// The write tools a policy can turn on, in the order of the manifest.
export const WRITE_TOOLS = ['scale', 'rollout-restart', 'rollout-undo', 'delete-pod', 'cordon', 'uncordon', 'drain', 'apply', 'patch', 'exec']

// exec runs any command in a container and its output is not filtered.
export const POWERFUL = ['exec']
export const isPowerful = (tool) => POWERFUL.includes(tool)

export const ALL = '*'
export const MAX_NAMESPACES = 50
const LABEL = /^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$/

// The defaults of the add-on: every namespace, every write tool but exec,
// Secret values hidden.
export const DEFAULTS = Object.freeze({ namespaces: [ALL], writeTools: WRITE_TOOLS.filter((t) => !isPowerful(t)), readSecretValues: false })

// The policy of one cluster from the answer of get-policy: its own, or the defaults.
export function policyOf(view, key) {
  const own = view?.clusters && typeof view.clusters === 'object' ? view.clusters[key] : undefined
  const p = own || view?.defaults || DEFAULTS
  return {
    namespaces: Array.isArray(p.namespaces) ? [...p.namespaces] : [...DEFAULTS.namespaces],
    writeTools: Array.isArray(p.writeTools) ? [...p.writeTools] : [...DEFAULTS.writeTools],
    readSecretValues: p.readSecretValues === true,
  }
}

// The first problem of a namespace list: { key, entry } for the page's words, or null.
export function namespaceIssue(list) {
  const ns = Array.isArray(list) ? list : []
  if (ns.length === 0) return { key: 'nsEmpty', entry: '' }
  if (ns.length > MAX_NAMESPACES) return { key: 'nsTooMany', entry: '' }
  for (const n of ns) if (n !== ALL && !LABEL.test(n)) return { key: 'nsShape', entry: n }
  return null
}

// A namespace added as a chip. * stands alone: it replaces the list, and a
// name replaces it.
export function addNamespace(list, text) {
  const n = String(text || '').trim()
  if (!n) return list
  if (n === ALL) return [ALL]
  const rest = list.filter((x) => x !== ALL)
  return rest.includes(n) ? rest : [...rest, n]
}

export const removeNamespace = (list, n) => list.filter((x) => x !== n)

// The draft the form edits, from a policy.
export function draftOf(policy) {
  const p = policy || DEFAULTS
  const tools = Array.isArray(p.writeTools) ? p.writeTools : []
  return {
    namespaces: Array.isArray(p.namespaces) ? [...p.namespaces] : [],
    writeTools: WRITE_TOOLS.filter((t) => tools.includes(t)),
    readSecretValues: p.readSecretValues === true,
  }
}

// The arguments of set-policy that a draft makes.
export function argsOf(key, d) {
  return { tool: 'set-policy', key, namespaces: [...d.namespaces], writeTools: WRITE_TOOLS.filter((t) => d.writeTools.includes(t)), readSecretValues: d.readSecretValues === true }
}

export const same = (a, b) => JSON.stringify(draftOf(a)) === JSON.stringify(draftOf(b))

// The clusters an administrator can set a policy for: those the connections
// of this kind list, and those that already have one.
export function clusterKeys(connectors, clusters) {
  const out = new Set()
  for (const c of Array.isArray(connectors) ? connectors : []) {
    if (c?.kind !== 'kubernetes') continue
    for (const r of c.resources || []) if (typeof r === 'string' && r) out.add(r)
  }
  for (const k of Object.keys(clusters && typeof clusters === 'object' ? clusters : {})) out.add(k)
  return [...out].sort()
}
