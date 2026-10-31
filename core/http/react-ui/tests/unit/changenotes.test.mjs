import test from 'node:test'
import assert from 'node:assert/strict'
import { turnChanges, describeChanges, changedKeys, newLabels, wellKnownTag, liveTurns } from '../../src/utils/changeNotes.js'

const tool = (seq, turn, id) => ({ seq, turn, role: 'tool', text: 'write_file: x', card: { kind: 'files_change', id } })

test('change notes: a turn collects the file changes of its tool calls and its checkpoint, once each', () => {
  const messages = [
    { seq: 1, turn: 1, role: 'user', text: 'hi' },
    tool(2, 1, '10:/LocalAI/persona.json'),
    tool(3, 1, '11:/LocalAI/user.json'),
    { seq: 4, turn: 1, role: 'assistant', text: 'Saved.' },
    { seq: 5, turn: 1, role: 'checkpoint', rev: 12, text: '/pages/tasks/index.html\n/pages/tasks/tasks.json' },
    { seq: 6, turn: 2, role: 'tool', text: 'read_file: x' },
    { seq: 7, turn: 2, role: 'checkpoint', rev: 0, text: '' },
    { seq: 8, turn: 3, role: 'tool', text: 'x', card: { kind: 'trigger_proposal', id: 'p1' } },
    tool(9, 3, '13:/notes/todo.md'),
  ]
  const byTurn = turnChanges(messages)
  assert.deepEqual(byTurn.get(1), [
    { rev: 10, paths: ['/LocalAI/persona.json'] },
    { rev: 11, paths: ['/LocalAI/user.json'] },
    { rev: 12, paths: ['/pages/tasks/index.html', '/pages/tasks/tasks.json'] },
  ])
  assert.equal(byTurn.has(2), false, 'a turn that changed nothing has no note')
  assert.equal(byTurn.has(3), false, 'a change outside the well-known files and pages has no note')
})

test('change notes: the files a command saved count like a file tool\'s change', () => {
  const messages = [
    { seq: 1, turn: 1, role: 'user', text: 'build a page' },
    { seq: 2, turn: 1, role: 'tool', text: 'bash: python3 build.py', card: { kind: 'tool_provider', id: 'sandbox', files: [
      { path: '/pages/tasks/index.html', rev: 20, op: 'created' },
      { path: '/pages/tasks/tasks.json', rev: 20, op: 'created' },
      { path: '/report.docx', rev: 20, op: 'created' },
    ] } },
    { seq: 3, turn: 1, role: 'tool', text: 'bash: ls', card: { kind: 'tool_provider', id: 'sandbox' } },
  ]
  assert.deepEqual(turnChanges(messages).get(1), [{ rev: 20, paths: ['/pages/tasks/index.html', '/pages/tasks/tasks.json'] }])
})

test('change notes: what a turn says, by file', () => {
  const both = describeChanges([{ rev: 1, paths: ['/LocalAI/persona.json'] }, { rev: 2, paths: ['/LocalAI/user.json'] }])
  assert.equal(both.notes[0].key, 'profilePersona')
  assert.deepEqual(both.revs, [2, 1], 'undo goes newest first')
  assert.equal(both.notes[0].action, 'account')

  assert.equal(describeChanges([{ rev: 1, paths: ['/LocalAI/user.json'] }]).notes[0].key, 'profile')
  assert.equal(describeChanges([{ rev: 1, paths: ['/LocalAI/persona.json'] }]).notes[0].key, 'persona')
  const sc = describeChanges([{ rev: 1, paths: ['/LocalAI/shortcuts.json'] }])
  assert.equal(sc.notes[0].key, 'shortcuts')
  assert.equal(sc.notes[0].toast, 'shortcut')
  assert.equal(describeChanges([{ rev: 1, paths: ['/LocalAI/home.json'] }]).notes[0].toast, 'home')
  assert.equal(describeChanges([{ rev: 1, paths: ['/LocalAI/ui.json'] }]).notes[0].key, 'ui')

  // A page created in this turn, then changed in a later one.
  const seen = new Set()
  const made = describeChanges([{ rev: 3, paths: ['/pages/tasks/index.html', '/pages/tasks/tasks.json'] }], seen)
  assert.deepEqual(made.notes, [{ key: 'pageCreated', page: 'tasks', action: 'page', toast: 'page' }])
  const changed = describeChanges([{ rev: 4, paths: ['/pages/tasks/index.html'] }], seen)
  assert.deepEqual(changed.notes, [{ key: 'pageChanged', page: 'tasks', file: 'pages/tasks/index.html', action: 'page' }])

  assert.equal(describeChanges([{ rev: 1, paths: ['/notes.md'] }]).notes.length, 0)
})

test('change notes: changedKeys compares two versions of a file', () => {
  assert.deepEqual(changedKeys({ name: 'A', seen: { x: 1 } }, { name: 'A', seen: { x: 2 } }), ['seen'])
  assert.deepEqual(changedKeys(null, { name: 'A' }), ['name'])
  assert.deepEqual(changedKeys({ a: 1, b: 2 }, { a: 1 }), ['b'])
})

test('change notes: the labels a shortcuts change added', () => {
  const before = { shortcuts: [{ label: 'Summarise', prompt: 's' }] }
  const after = { shortcuts: [{ label: 'Summarise', prompt: 's' }, { label: 'Plan my day', prompt: 'p' }] }
  assert.deepEqual(newLabels(before, after), ['Plan my day'])
  assert.deepEqual(newLabels(null, after), ['Summarise', 'Plan my day'])
})

test('change notes: the Files pane tags the well-known files', () => {
  assert.equal(wellKnownTag('/LocalAI/user.json'), 'profile')
  assert.equal(wellKnownTag('/LocalAI/persona.json'), 'persona')
  assert.equal(wellKnownTag('/LocalAI/home.json'), 'home')
  assert.equal(wellKnownTag('/sub/user.json'), '')
  assert.equal(wellKnownTag('/pages/tasks/index.html'), 'page')
})

test('change notes: only the turns that ended in this tab are live', () => {
  const live = liveTurns(2, [{ turn: 1 }, { turn: 2 }, { turn: 3 }, { turn: 4 }])
  assert.deepEqual([...live], [3, 4])
  assert.deepEqual([...liveTurns(null, [{ turn: 1 }])], [])
})

test('change notes: a transcript from before LocalAI/ still names the settings files at the top of the home', () => {
  assert.equal(wellKnownTag('/user.json'), 'profile')
  assert.equal(wellKnownTag('/LocalAI/notes.json'), '')
  const old = describeChanges([{ rev: 1, paths: ['/persona.json'] }, { rev: 2, paths: ['/LocalAI/user.json'] }])
  assert.equal(old.notes[0].key, 'profilePersona')
})
