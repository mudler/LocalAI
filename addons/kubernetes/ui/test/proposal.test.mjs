import test from 'node:test'
import assert from 'node:assert/strict'
import { diffLines, proposalView } from '../src/proposal.js'

test('kubernetes proposal: shows the diff lines with their sign', () => {
  const diff = '--- live\n+++ dry-run\n@@ -1,3 +1,3 @@\n spec:\n-  replicas: 2\n+  replicas: 3\n'
  assert.deepEqual(diffLines(diff), [
    { kind: 'head', text: '--- live' },
    { kind: 'head', text: '+++ dry-run' },
    { kind: 'hunk', text: '@@ -1,3 +1,3 @@' },
    { kind: 'same', text: ' spec:' },
    { kind: 'del', text: '-  replicas: 2' },
    { kind: 'add', text: '+  replicas: 3' },
  ])
  const v = proposalView('apply', { manifest: 'x' }, { tool: 'apply', cluster: 'https://cluster.example:6443#context=team-a', kind: 'Deployment', namespace: 'team-a', name: 'web', change: 'update', diff, diffCut: true })
  assert.equal(v.diffCut, true)
  assert.equal(v.change, 'update')
  assert.equal(v.lines.length, 6)
  assert.equal(v.context, 'team-a')
  assert.equal(v.server, 'https://cluster.example:6443')
  assert.deepEqual(diffLines(''), [])
})

test('kubernetes proposal: shows the exec argv exactly', () => {
  const argv = ['sh', '-c', 'echo "a  b" && ls /', '', '--flag=x y']
  const v = proposalView('exec', { namespace: 'team-a', pod: 'web-0', command: ['ignored'] }, { tool: 'exec', kind: 'Pod', namespace: 'team-a', name: 'web-0', container: 'app', command: argv, timeoutSeconds: 30 })
  assert.deepEqual(v.command, argv)
  assert.equal(v.container, 'app')
  assert.equal(v.timeoutSeconds, 30)
  // Without a preview the stored arguments still show it.
  assert.deepEqual(proposalView('exec', { pod: 'web-0', command: ['id'] }, null).command, ['id'])
  assert.equal(proposalView('exec', { pod: 'web-0' }, null).name, 'web-0')
})

test('kubernetes proposal: shows the drain pods and the skipped pods', () => {
  const v = proposalView('drain', { node: 'node-1' }, { tool: 'drain', kind: 'Node', node: 'node-1', cordoned: false, pods: ['team-a/web-0', 'team-a/web-1'], skipped: [{ pod: 'kube-system/proxy-x', reason: 'DaemonSet' }, { bad: 1 }], timeoutSeconds: 120 })
  assert.deepEqual(v.pods, ['team-a/web-0', 'team-a/web-1'])
  assert.deepEqual(v.skipped, [{ pod: 'kube-system/proxy-x', reason: 'DaemonSet' }])
  assert.equal(v.node, 'node-1')
  assert.equal(v.cordoned, false)
})

test('kubernetes proposal: scale and rollback show from and to', () => {
  const s = proposalView('scale', { replicas: 3 }, { tool: 'scale', kind: 'Deployment', namespace: 'team-a', name: 'web', from: 2, to: 3 })
  assert.equal(s.from, '2')
  assert.equal(s.to, '3')
  const u = proposalView('rollout-undo', {}, { from: 5, to: 4 })
  assert.deepEqual([u.from, u.to], ['5', '4'])
  assert.equal(proposalView('scale', {}, {}).from, '')
})
