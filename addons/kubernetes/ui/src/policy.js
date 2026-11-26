// The model of the policy editor. No React, so a node test drives it. The
// add-on keeps one policy per cluster, keyed by the server URL, and a default
// policy for every cluster with none. It is the authority: it checks the
// policy again when it saves and enforces it on every call. These helpers
// repeat its rules only to show a problem next to the field.
import { normalizeServer, splitKey } from './kube.js'

// The write tools a policy can turn on, in the order of the manifest.
export const WRITE_TOOLS = ['scale', 'rollout-restart', 'rollout-undo', 'delete-pod', 'cordon', 'uncordon', 'drain', 'apply', 'patch', 'exec']

// exec runs any command in a container and its output is not filtered.
export const POWERFUL = ['exec']
export const isPowerful = (tool) => POWERFUL.includes(tool)

export const ALL = '*'
export const MAX_NAMESPACES = 50
const LABEL = /^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$/

// The built-in default of the add-on: reads in every namespace, no write tool,
// Secret values shown. It applies to a cluster with no policy until an
// administrator sets a default policy.
export const DEFAULTS = Object.freeze({ namespaces: [ALL], writeTools: [], readSecretValues: true })

// The entry of the editor that edits the default policy.
export const DEFAULT_KEY = 'default'

// The server of a cluster key: a connection key or a server URL.
export function serverOf(key) {
  const k = String(key || '')
  if (k.includes('#')) return splitKey(k).server
  return normalizeServer(k)
}

const tidy = (p) => ({
  namespaces: Array.isArray(p?.namespaces) ? [...p.namespaces] : [...DEFAULTS.namespaces],
  writeTools: Array.isArray(p?.writeTools) ? [...p.writeTools] : [...DEFAULTS.writeTools],
  readSecretValues: p?.readSecretValues === true,
})

const ownOf = (view, key) => (view?.clusters && typeof view.clusters === 'object' ? view.clusters[key] : undefined)

// The policy of one cluster from the answer of get-policy: its own, or the
// default policy. DEFAULT_KEY gives the default policy.
export function policyOf(view, key) {
  if (key === DEFAULT_KEY) return tidy(view?.default || DEFAULTS)
  return tidy(ownOf(view, key) || view?.default || DEFAULTS)
}

// The state of one cluster for the editor: 'closed' gets a policy that
// allows no namespace (the default after a damaged file was replaced, or a
// server whose old entries disagree), so it reads and writes nothing; 'own'
// has its own policy; 'default' uses the default policy; 'off' uses a
// default that allows no write, so nothing can be changed until an
// administrator decides.
export function stateOf(view, key) {
  if (key === DEFAULT_KEY) return view?.defaultSet ? 'own' : 'off'
  if (policyOf(view, key).namespaces.length === 0) return 'closed'
  if (ownOf(view, key)) return 'own'
  return policyOf(view, key).writeTools.length === 0 ? 'off' : 'default'
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
  const target = key === DEFAULT_KEY ? { default: true } : { key }
  return { tool: 'set-policy', ...target, namespaces: [...d.namespaces], writeTools: WRITE_TOOLS.filter((t) => d.writeTools.includes(t)), readSecretValues: d.readSecretValues === true }
}

export const same = (a, b) => JSON.stringify(draftOf(a)) === JSON.stringify(draftOf(b))

// The clusters an administrator can set a policy for, by server: those the
// connections of this kind list, and those that already have one.
export function clusterKeys(connectors, clusters) {
  const out = new Set()
  for (const c of Array.isArray(connectors) ? connectors : []) {
    if (c?.kind !== 'kubernetes') continue
    for (const r of c.resources || []) {
      const s = typeof r === 'string' ? serverOf(r) : ''
      if (s) out.add(s)
    }
  }
  for (const k of Object.keys(clusters && typeof clusters === 'object' ? clusters : {})) out.add(k)
  return [...out].sort()
}

// The context names the connections use for one server, for its label.
export function contextsOf(connectors, server) {
  const out = new Set()
  for (const c of Array.isArray(connectors) ? connectors : []) {
    if (c?.kind !== 'kubernetes') continue
    for (const r of c.resources || []) {
      const k = splitKey(r)
      if (k.server && k.server === server) out.add(k.context)
    }
  }
  return [...out].sort()
}
