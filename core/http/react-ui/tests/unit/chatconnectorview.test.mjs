import test from 'node:test'
import assert from 'node:assert/strict'
import { emptyLive } from '../../src/utils/chatLive.js'
import { threadRows } from '../../src/utils/chatThread.js'
import {
  standingOf, senderName, peopleOf, threadLine, threadView, noteCode, noteTone, foldNotes, placeView, isConnectorChat,
} from '../../src/utils/connectorChat.js'

const t = (k) => ({ 'connector.unknownSender': 'Unknown sender' }[k] || k)

test('connector chat: the standing of a sender is one of five, and an unknown word reads as the least trusted', () => {
  assert.equal(standingOf({ standing: 'owner' }), 'owner')
  assert.equal(standingOf({ standing: 'channel' }), 'channel')
  assert.equal(standingOf({ standing: 'admin-of-everything' }), 'anyone')
  assert.equal(standingOf(null), 'anyone')
})

test('connector chat: a sender shows a name, then a handle, and an id only in small print beside a label', () => {
  assert.deepEqual(senderName({ id: 'U1', name: 'Ada Lovelace', username: 'ada' }, t), { name: 'Ada Lovelace', small: '@ada', resolved: true })
  assert.deepEqual(senderName({ id: 'U1', username: 'ada' }, t), { name: '@ada', small: '', resolved: true })
  const bare = senderName({ id: 'U1' }, t)
  assert.equal(bare.name, 'Unknown sender', 'the id is never the only thing shown')
  assert.equal(bare.small, 'U1')
  assert.equal(bare.resolved, false)
  assert.equal(senderName(undefined, t).name, 'Unknown sender')
})

test('connector chat: the people of a chat are listed once, in order, with their latest standing', () => {
  const people = peopleOf([
    { role: 'user', sender: { id: 'U1', name: 'Ada', standing: 'listed' } },
    { role: 'assistant', text: 'ok' },
    { role: 'user', sender: { id: 'U2', name: 'Bo', standing: 'anyone' } },
    { role: 'user', sender: { id: 'U1', name: 'Ada', standing: 'owner' } },
    { role: 'user', text: 'a line stored before senders existed' },
  ])
  assert.deepEqual(people.map((p) => [p.key, p.standing, p.count]), [['U1', 'owner', 2], ['U2', 'anyone', 1]])
})

test('connector chat: a thread line splits at the first colon, and a line without an author keeps its text', () => {
  assert.deepEqual(threadLine('Ada: the deploy failed: again'), { author: 'Ada', text: 'the deploy failed: again' })
  assert.deepEqual(threadLine('The person asking (Cy): retry?'), { author: 'The person asking (Cy)', text: 'retry?' })
  assert.deepEqual(threadLine('no author here'), { author: '', text: 'no author here' })
})

test('connector chat: the thread block counts its lines and says when older ones were left out', () => {
  const v = threadView({ thread: { lines: ['Ada: one', 'Assistant (you): two'], cut: true } })
  assert.equal(v.count, 2)
  assert.equal(v.cut, true)
  assert.equal(v.lines[1].author, 'Assistant (you)')
  assert.equal(threadView({}).count, 0, 'a chat stored before the block existed has none')
})

test('connector chat: the thread block and the host notes are rows, in the order they were stored, and old chats are unchanged', () => {
  const rows = threadRows([
    { seq: 1, role: 'thread', thread: { lines: ['Ada: hi'] } },
    { seq: 2, role: 'user', text: 'help', sender: { id: 'U1' } },
    { seq: 3, role: 'hostnote', host_note: { code: 'held_back' }, text: 'x' },
    { seq: 4, role: 'assistant', text: 'sure' },
  ], emptyLive())
  assert.deepEqual(rows.map((r) => r.kind), ['thread', 'msg', 'hostnote', 'msg'])
  const old = threadRows([{ seq: 1, role: 'user', text: 'hi' }, { seq: 2, role: 'assistant', text: 'yo' }], emptyLive())
  assert.deepEqual(old.map((r) => r.kind), ['msg', 'msg'])
})

test('connector chat: a double reply is one answer row, whatever the platform did with the message', () => {
  // The server stores one assistant line per answer; a split or edited message
  // on the platform leaves nothing in the transcript to show twice.
  const rows = threadRows([
    { seq: 1, role: 'user', text: 'long question', sender: { id: 'U1' } },
    { seq: 2, role: 'reasoning', text: 'think' },
    { seq: 3, role: 'assistant', text: 'one long answer that the platform cut in two messages' },
  ], emptyLive())
  assert.equal(rows.filter((r) => r.kind === 'msg' && r.msg.role === 'assistant').length, 1)
})

test('connector chat: equal host notes in a row fold into one with a count', () => {
  const note = (seq, code, text = '') => ({ kind: 'hostnote', key: `hn-${seq}`, msg: { seq, role: 'hostnote', host_note: { code }, text } })
  const out = foldNotes([note(1, 'held_back', 'a'), note(2, 'held_back', 'a'), { kind: 'msg', key: 'm' }, note(4, 'held_back', 'a'), note(5, 'rate', '')])
  assert.deepEqual(out.map((r) => [r.kind, r.count]), [['hostnote', 2], ['msg', undefined], ['hostnote', 1], ['hostnote', 1]])
})

test('connector chat: a host note has words for the codes the server writes and a plain fallback', () => {
  assert.equal(noteCode('too_long'), 'too_long')
  assert.equal(noteCode('from-a-newer-server'), 'other')
  assert.equal(noteTone('paired'), 'ok')
  assert.equal(noteTone('held_back'), 'info')
  assert.equal(noteTone('no_upload'), 'warn')
})

test('connector chat: the place keeps only an https link and reads direct and thread', () => {
  assert.deepEqual(placeView({ direct: true }), { direct: true, name: '', thread: false, link: '' })
  assert.equal(placeView({ link: 'https://chat.example.test/a/b?c=1' }).link, 'https://chat.example.test/a/b?c=1')
  assert.equal(placeView({ link: 'javascript:alert(1)' }).link, '')
  assert.equal(placeView({ link: 'http://chat.example.test/' }).link, '')
  assert.equal(placeView({ link: 'https://x.test/a b' }).link, '')
  assert.equal(placeView(null).name, '')
  assert.equal(isConnectorChat({ origin: 'connector' }), true)
  assert.equal(isConnectorChat({ origin: 'trigger' }), false)
})
