import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { addNamespace, argsOf, clusterKeys, DEFAULTS, draftOf, isPowerful, namespaceIssue, policyOf, removeNamespace, same, WRITE_TOOLS } from '../src/policy.js'

const manifest = JSON.parse(readFileSync(new URL('../../manifest.json', import.meta.url), 'utf8'))

test('kubernetes policy: the defaults match the add-on', () => {
  assert.deepEqual(WRITE_TOOLS, ['scale', 'rollout-restart', 'rollout-undo', 'delete-pod', 'cordon', 'uncordon', 'drain', 'apply', 'patch', 'exec'])
  const writes = manifest.capabilities.toolDefinitions.filter((d) => d.kind === 'write').map((d) => d.name)
  assert.deepEqual(writes, WRITE_TOOLS)
  assert.deepEqual(DEFAULTS, { namespaces: ['*'], writeTools: WRITE_TOOLS.filter((t) => t !== 'exec'), readSecretValues: false })
  // A cluster with no policy of its own uses the defaults the add-on sent.
  const view = { clusters: { 'https://cluster.example:6443#context=team-a': { namespaces: ['team-a'], writeTools: ['scale'], readSecretValues: true } }, defaults: DEFAULTS }
  assert.deepEqual(policyOf(view, 'https://cluster.example:6443#context=team-a').namespaces, ['team-a'])
  assert.deepEqual(policyOf(view, 'https://cluster.example:6443#context=other'), DEFAULTS)
  assert.deepEqual(policyOf(null, 'x'), DEFAULTS)
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
  assert.deepEqual(argsOf('k#context=a', d), { tool: 'set-policy', key: 'k#context=a', namespaces: ['team-a'], writeTools: ['scale', 'exec'], readSecretValues: true })
  assert.equal(same(d, draftOf({ namespaces: ['team-a'], writeTools: ['scale', 'exec'], readSecretValues: true })), true)
  assert.equal(same(d, draftOf(DEFAULTS)), false)
})

test('kubernetes policy: the clusters to edit come from the connections and the saved policies', () => {
  const connectors = [
    { kind: 'kubernetes', resources: ['https://cluster.example:6443#context=team-a'] },
    { kind: 'ssh', resources: ['a@b'] },
    { kind: 'kubernetes', resources: [] },
  ]
  const clusters = { 'https://127.0.0.1:6443#context=lab': DEFAULTS, 'https://cluster.example:6443#context=team-a': DEFAULTS }
  assert.deepEqual(clusterKeys(connectors, clusters), ['https://127.0.0.1:6443#context=lab', 'https://cluster.example:6443#context=team-a'])
  assert.deepEqual(clusterKeys(undefined, undefined), [])
})
