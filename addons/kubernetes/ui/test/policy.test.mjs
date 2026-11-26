import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { addNamespace, argsOf, clusterKeys, contextsOf, DEFAULT_KEY, DEFAULTS, draftOf, isPowerful, namespaceIssue, policyOf, removeNamespace, same, serverOf, stateOf, WRITE_TOOLS } from '../src/policy.js'

const manifest = JSON.parse(readFileSync(new URL('../../manifest.json', import.meta.url), 'utf8'))

test('kubernetes policy: the defaults match the add-on', () => {
  assert.deepEqual(WRITE_TOOLS, ['scale', 'rollout-restart', 'rollout-undo', 'delete-pod', 'cordon', 'uncordon', 'drain', 'apply', 'patch', 'exec'])
  const writes = manifest.capabilities.toolDefinitions.filter((d) => d.kind === 'write').map((d) => d.name)
  assert.deepEqual(writes, WRITE_TOOLS)
  assert.deepEqual(DEFAULTS, { namespaces: ['*'], writeTools: [], readSecretValues: true })
  // A cluster with no policy of its own uses the default policy.
  const server = 'https://cluster.example:6443'
  const open = { namespaces: ['*'], writeTools: ['scale'], readSecretValues: false }
  const view = { clusters: { [server]: { namespaces: ['team-a'], writeTools: ['scale'], readSecretValues: true } }, default: DEFAULTS, defaultSet: false }
  assert.deepEqual(policyOf(view, server).namespaces, ['team-a'])
  assert.deepEqual(policyOf(view, 'https://other.example:6443'), DEFAULTS)
  assert.deepEqual(policyOf(null, 'x'), DEFAULTS)
  assert.equal(stateOf(view, server), 'own')
  assert.equal(stateOf(view, 'https://other.example:6443'), 'off', 'not configured: writes off')
  assert.equal(stateOf(view, DEFAULT_KEY), 'off')
  const set = { ...view, default: open, defaultSet: true }
  assert.deepEqual(policyOf(set, 'https://other.example:6443'), open)
  assert.equal(stateOf(set, 'https://other.example:6443'), 'default')
  assert.equal(stateOf(set, DEFAULT_KEY), 'own')
  assert.deepEqual(policyOf(set, DEFAULT_KEY), open)
  // After a damaged file was replaced, the default allows no namespace: the
  // clusters under it read and write nothing.
  const closed = { clusters: {}, default: { namespaces: [], writeTools: [], readSecretValues: false }, defaultSet: true }
  assert.equal(stateOf(closed, 'https://other.example:6443'), 'closed')
  assert.equal(stateOf(closed, DEFAULT_KEY), 'own')
  const kept = { clusters: { [server]: { namespaces: [], writeTools: [], readSecretValues: false } }, default: DEFAULTS, defaultSet: false }
  assert.equal(stateOf(kept, server), 'closed', 'a cluster the add-on keeps closed')
})

test('kubernetes policy: every state has its words in every locale', async () => {
  const { readdirSync } = await import('node:fs')
  const dir = new URL('../src/i18n/', import.meta.url)
  for (const f of readdirSync(dir)) {
    const words = JSON.parse(readFileSync(new URL(f, dir), 'utf8'))
    for (const k of ['ownPolicy', 'defaultPolicy', 'notConfigured', 'noNamespace']) assert.ok(words.policy?.[k], `${f} policy.${k}`)
  }
})

test('kubernetes policy: exec is marked powerful', () => {
  assert.equal(isPowerful('exec'), true)
  for (const t of WRITE_TOOLS.filter((x) => x !== 'exec')) assert.equal(isPowerful(t), false, t)
})

test('kubernetes policy: refuses an empty namespace list and bad namespace names', () => {
  assert.deepEqual(namespaceIssue([]), { key: 'nsEmpty', entry: '' })
  assert.deepEqual(namespaceIssue(['team-a', 'Team_B']), { key: 'nsShape', entry: 'Team_B' })
  assert.deepEqual(namespaceIssue(['-a']), { key: 'nsShape', entry: '-a' })
  assert.deepEqual(namespaceIssue(['a'.repeat(64)]).key, 'nsShape')
  assert.deepEqual(namespaceIssue(Array.from({ length: 51 }, (_, n) => `ns-${n}`)), { key: 'nsTooMany', entry: '' })
  assert.equal(namespaceIssue(['*']), null)
  assert.equal(namespaceIssue(['team-a', 'kube-system']), null)
})

test('kubernetes policy: chips add and remove namespaces; * stands alone', () => {
  assert.deepEqual(addNamespace(['*'], 'team-a'), ['team-a'])
  assert.deepEqual(addNamespace(['team-a'], ' team-b '), ['team-a', 'team-b'])
  assert.deepEqual(addNamespace(['team-a'], 'team-a'), ['team-a'])
  assert.deepEqual(addNamespace(['team-a'], '*'), ['*'])
  assert.deepEqual(addNamespace(['team-a'], ''), ['team-a'])
  assert.deepEqual(removeNamespace(['team-a', 'team-b'], 'team-a'), ['team-b'])
})

test('kubernetes policy: the draft round-trips to the arguments of set-policy', () => {
  const d = draftOf({ namespaces: ['team-a'], writeTools: ['exec', 'scale'], readSecretValues: true })
  assert.deepEqual(argsOf('https://k.example', d), { tool: 'set-policy', key: 'https://k.example', namespaces: ['team-a'], writeTools: ['scale', 'exec'], readSecretValues: true })
  assert.deepEqual(argsOf(DEFAULT_KEY, d), { tool: 'set-policy', default: true, namespaces: ['team-a'], writeTools: ['scale', 'exec'], readSecretValues: true })
  assert.equal(same(d, draftOf({ namespaces: ['team-a'], writeTools: ['scale', 'exec'], readSecretValues: true })), true)
  assert.equal(same(d, draftOf(DEFAULTS)), false)
})

test('kubernetes policy: the clusters to edit are servers, from the connections and the saved policies', () => {
  const connectors = [
    { kind: 'kubernetes', resources: ['https://cluster.example:6443#context=team-a'] },
    { kind: 'kubernetes', resources: ['https://Cluster.Example:6443#context=ops'] },
    { kind: 'kubernetes', resources: ['https://other.example:443#context=dev'] },
    { kind: 'ssh', resources: ['a@b'] },
    { kind: 'kubernetes', resources: [] },
  ]
  const clusters = { 'https://lab.example:6443': DEFAULTS, 'https://cluster.example:6443': DEFAULTS }
  assert.deepEqual(clusterKeys(connectors, clusters), ['https://cluster.example:6443', 'https://lab.example:6443', 'https://other.example'])
  assert.deepEqual(contextsOf(connectors, 'https://cluster.example:6443'), ['ops', 'team-a'])
  assert.deepEqual(clusterKeys(undefined, undefined), [])
})

test('kubernetes policy: a server is written as the add-on writes it', () => {
  assert.equal(serverOf('https://Cluster.Example.:443/#context=x'), 'https://cluster.example')
  assert.equal(serverOf('https://cluster.example:443'), 'https://cluster.example')
  assert.equal(serverOf('https://cluster.example:6443'), 'https://cluster.example:6443')
  assert.equal(serverOf('https://[2001:DB8::1]:443'), 'https://[2001:db8::1]')
  assert.equal(serverOf('http://cluster.example'), '')
})
