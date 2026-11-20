import test from 'node:test'
import assert from 'node:assert/strict'
import {
  SHARED_PREFIX, composerShared, decodeSharedRef, isSharedRef, pulledSharedEntries, sharedAccess, sharedChoices,
  sharedPath, sharedUnavailable, sharedUsable, splitPulled, memorySnippet,
} from '../../src/utils/sharedProjects.js'
import { pulledProjects } from '../../src/utils/projects.js'
import { proposalProject, proposalProposer, proposalVoidedForProposer, proposalsForProject } from '../../src/utils/changeProposal.js'

const SHARED = [
  { ref: 's:12', name: 'Brand guide', description: 'Voice', status: 'active', mode: 'read', owner_name: 'Dana Reyes' },
  { ref: 's:14', name: 'Launch checklist', status: 'active', mode: 'write', owner_name: 'Sam Okafor' },
  { ref: 's:15', name: 'Podcast episode 14', status: 'archived', mode: 'read', owner_name: 'Dana Reyes' },
  { ref: 's:16', name: 'Old plan', status: 'broken', mode: 'read', owner_name: 'Sam Okafor', error: 'cannot read' },
]

test('shared refs: s: marks a shared project, own ids never match', () => {
  assert.equal(SHARED_PREFIX, 's:')
  assert.equal(isSharedRef('s:12'), true)
  assert.equal(isSharedRef('p1'), false)
  assert.equal(isSharedRef(''), false)
  assert.equal(isSharedRef(undefined), false)
  assert.equal(isSharedRef(12), false)
})

test('shared refs: the page path encodes the colon and decodes back', () => {
  assert.equal(sharedPath('s:12'), '/app/projects/shared/s%3A12')
  assert.equal(decodeSharedRef('s:12'), 's:12')
  assert.equal(decodeSharedRef('s%3A12'), 's:12')
  assert.equal(decodeSharedRef(decodeURIComponent('s%3A12')), 's:12')
  for (const bad of ['', 'p1', 's:', 's:abc', 's:1/2', 's:12 ', '%E0%A4%A', undefined, null]) assert.equal(decodeSharedRef(bad), '', String(bad))
})

test('shared access: read and write labels, anything else reads', () => {
  assert.equal(sharedAccess('write'), 'write')
  assert.equal(sharedAccess('read'), 'read')
  assert.equal(sharedAccess(''), 'read')
  assert.equal(sharedAccess('admin'), 'read')
})

test('shared state: only an active project is usable', () => {
  assert.deepEqual(SHARED.map(sharedUsable), [true, true, false, false])
  assert.deepEqual(SHARED.map(sharedUnavailable), [false, false, true, true])
})

test('shared choices: active ones not yet pulled, matching a search', () => {
  assert.deepEqual(sharedChoices(SHARED, { pulled_projects: [] }).map(p => p.ref), ['s:12', 's:14'])
  assert.deepEqual(sharedChoices(SHARED, { pulled_projects: ['s:12', 'p1'] }).map(p => p.ref), ['s:14'])
  assert.deepEqual(sharedChoices(null, null), [])
  assert.deepEqual(composerShared(SHARED, 'LAUNCH').map(p => p.ref), ['s:14'])
  assert.deepEqual(composerShared(SHARED, '').map(p => p.ref), ['s:12', 's:14'])
})

test('pulled entries: own ids use the own list and shared refs use the shared list', () => {
  const own = [{ id: 'p1', name: 'Home lab', status: 'active' }]
  const chat = { pulled_projects: ['p1', 's:12', 's:15', 's:99', 'gone'] }
  const out = pulledProjects(chat, own, SHARED)
  assert.deepEqual(out.map(p => p.id), ['p1', 's:12', 's:15', 's:99'])
  assert.deepEqual(out[0], { id: 'p1', name: 'Home lab', state: 'mounted' })
  assert.deepEqual(out[1], { id: 's:12', name: 'Brand guide', state: 'mounted', shared: true, owner: 'Dana Reyes', mode: 'read' })
  assert.equal(out[2].state, 'archived')
  // A ref the list does not name is unlisted, not removed: the list can be short for a moment. Never dropped, never a crash.
  assert.deepEqual(out[3], { id: 's:99', name: '', state: 'unlisted', shared: true, owner: '', mode: 'read' })
})

test('pulled entries: before the shared list loads, shared refs are not claimed unlisted or removed', () => {
  const chat = { pulled_projects: ['s:12', 'p1'] }
  assert.deepEqual(pulledProjects(chat, [{ id: 'p1', name: 'A', status: 'active' }], null).map(p => p.id), ['p1'])
  assert.deepEqual(pulledSharedEntries(['s:12'], null), [])
  assert.deepEqual(pulledProjects(chat, null, SHARED).map(p => p.id), ['s:12'])
})

test('pulled entries: the own state mapping is unchanged without a shared list', () => {
  assert.deepEqual(pulledProjects({ pulled_projects: ['a'] }, [{ id: 'a', name: 'X', status: 'active' }]), [{ id: 'a', name: 'X', state: 'mounted' }])
})

test('splitPulled separates own ids and shared refs', () => {
  assert.deepEqual(splitPulled(['p1', 's:3', 'p2']), { own: ['p1', 'p2'], shared: ['s:3'] })
  assert.deepEqual(splitPulled(undefined), { own: [], shared: [] })
})

const proposal = (extra = {}) => ({ id: 'x', kind: 'memory', scope: 'project', target: 'Projects/client-site-redesign/memory/brand-voice.md', summary: 'Brand voice', status: 'pending', ...extra })

test('proposals: a proposer is a display name set only for another person', () => {
  assert.equal(proposalProposer(proposal()), '')
  assert.equal(proposalProposer(proposal({ proposer: 'Dana Reyes' })), 'Dana Reyes')
  assert.equal(proposalProposer(null), '')
})

test('proposals: the project name comes from the label, else from the slug of the target', () => {
  assert.equal(proposalProject(proposal({ project: 'Client site redesign' })), 'Client site redesign')
  assert.equal(proposalProject(proposal()), 'Client site redesign')
  assert.equal(proposalProject(proposal({ target: '/Projects/home-lab-notes/memory/a.md' })), 'Home lab notes')
  assert.equal(proposalProject(proposal({ target: 'Memory/index.md' })), '')
})

test('proposals: a voided proposal from another person is a stale one with a proposer', () => {
  assert.equal(proposalVoidedForProposer(proposal({ proposer: 'Dana', status: 'stale' })), true)
  assert.equal(proposalVoidedForProposer(proposal({ status: 'stale' })), false)
  assert.equal(proposalVoidedForProposer(proposal({ proposer: 'Dana', status: 'pending' })), false)
})

test('shared memory: the snippet is plain text without images, link targets or headings', () => {
  const index = '# Brand guide\n\nVoice: **plain**, short.\n\n![t](https://x.test/p.png)\n\n- See [the rules](https://x.test/r) now\n'
  assert.equal(memorySnippet(index), 'Voice: plain, short. See the rules now')
  assert.equal(memorySnippet(''), '')
  assert.equal(memorySnippet('a\nb\nc\nd\ne', 2), 'a b')
})

test('proposals: the ones waiting for one project are those that write its memory', () => {
  const list = [
    proposal({ id: 'a' }),
    proposal({ id: 'b', target: '/Projects/client-site-redesign/memory/x.md' }),
    proposal({ id: 'c', target: 'Projects/other/memory/x.md' }),
    proposal({ id: 'd', target: 'Projects/client-site-redesign-2/memory/x.md' }),
    proposal({ id: 'e', status: 'applied' }),
    proposal({ id: 'f', target: 'Memory/index.md' }),
  ]
  assert.deepEqual(proposalsForProject(list, 'client-site-redesign').map(p => p.id), ['a', 'b'])
  assert.deepEqual(proposalsForProject(null, 'x'), [])
  assert.deepEqual(proposalsForProject(list, ''), [])
})

test('shared memory: the snippet drops raw HTML, and the text of style and script', () => {
  const index = '<img src="https://evil.example/x.png">\n<style>body{background:url(https://evil.example/b)}</style>\n<p style="x">Kept text</p>\n'
  assert.equal(memorySnippet(index), 'Kept text')
})
