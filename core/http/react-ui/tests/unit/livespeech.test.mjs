import test from 'node:test'
import assert from 'node:assert/strict'
import { speechText, chunkSpeech, lastAssistantText } from '../../src/utils/liveSpeech.js'

test('speechText drops fenced code and keeps inline code text', () => {
  const md = 'Run `ls -la` now.\n\n```sh\nrm -rf /\n```\n\nDone.'
  assert.equal(speechText(md), 'Run ls -la now. Done.')
})

test('speechText keeps link labels and drops images', () => {
  const md = 'See [the docs](https://example.com/a) and ![diagram](x.png) for **more** and _less_.'
  assert.equal(speechText(md), 'See the docs and for more and less.')
})

test('speechText flattens a table and a list', () => {
  const md = '# Title\n\n> quoted\n\n- one\n- two\n1. three\n\n| a | b |\n|---|---|\n| c | d |\n\n<b>bold</b>'
  const out = speechText(md)
  assert.ok(!/[|#>*<]/.test(out), out)
  assert.ok(!/---/.test(out), out)
  for (const w of ['Title', 'quoted', 'one', 'two', 'three', 'a', 'b', 'c', 'd', 'bold']) {
    assert.ok(out.split(/\s+/).includes(w), `${w} in ${out}`)
  }
  assert.ok(!/\s{2,}/.test(out))
})

test('chunkSpeech packs sentences up to max', () => {
  assert.deepEqual(chunkSpeech('One. Two! Three? Four.', 11), ['One. Two!', 'Three?', 'Four.'])
  assert.deepEqual(chunkSpeech('One. Two.', 300), ['One. Two.'])
})

test('chunkSpeech cuts an overlong sentence at a space', () => {
  const out = chunkSpeech('aaa bbb ccc ddd eee', 8)
  assert.deepEqual(out, ['aaa bbb', 'ccc ddd', 'eee'])
  for (const c of out) assert.ok(c.length <= 8)
})

test('chunkSpeech never returns empty chunks', () => {
  assert.deepEqual(chunkSpeech(''), [])
  assert.deepEqual(chunkSpeech('  \n\n  '), [])
  for (const c of chunkSpeech('Hi...\n\n\nThere!!  ?', 5)) assert.ok(c.trim().length > 0)
})

test('chunkSpeech handles text without punctuation and non-Latin text', () => {
  const long = 'word '.repeat(100).trim()
  const out = chunkSpeech(long, 50)
  assert.ok(out.length > 1)
  for (const c of out) assert.ok(c.length <= 50 && c.length > 0)
  assert.equal(out.join(' '), long)
  assert.deepEqual(chunkSpeech('你好。今天天气很好。我们走吧。', 8), ['你好。', '今天天气很好。', '我们走吧。'])
  assert.deepEqual(chunkSpeech('你好。今天。', 300), ['你好。今天。'])
  assert.deepEqual(chunkSpeech('你好。今天天气很好。', 7), ['你好。', '今天天气很好。'])
})

test('lastAssistantText ignores tool and reasoning lines and earlier turns', () => {
  const msgs = [
    { seq: 1, role: 'user', text: 'hi' },
    { seq: 2, role: 'assistant', text: 'old answer' },
    { seq: 3, role: 'user', text: 'do it' },
    { seq: 4, role: 'reasoning', text: 'thinking' },
    { seq: 5, role: 'assistant', text: 'First part.' },
    { seq: 6, role: 'tool', text: 'ls', name: 'bash' },
    { seq: 7, role: 'assistant', text: 'Second part.' },
  ]
  assert.equal(lastAssistantText(msgs, 3), 'First part.\nSecond part.')
  assert.equal(lastAssistantText(msgs, 0), 'First part.\nSecond part.')
  assert.equal(lastAssistantText(msgs, 7), '')
})

test('lastAssistantText returns empty for a tool-only turn', () => {
  const msgs = [
    { seq: 1, role: 'user', text: 'go' },
    { seq: 2, role: 'tool', text: 'x' },
    { seq: 3, role: 'reasoning', text: 'y' },
  ]
  assert.equal(lastAssistantText(msgs, 0), '')
  assert.equal(lastAssistantText([], 0), '')
  assert.equal(lastAssistantText(undefined, 0), '')
})

test('speechText removes bare URLs', () => {
  assert.equal(speechText('Open https://example.com/a?b=1 or http://x.org now.'), 'Open or now.')
})

test('speechText drops an unclosed fence to the end and keeps text after a closed one', () => {
  assert.equal(speechText('Before.\n```js\nlet a = 1'), 'Before.')
  assert.equal(speechText('A.\n```\ncode\n```\nB.'), 'A. B.')
})

test('chunkSpeech does not split on a dot followed by a non-space', () => {
  assert.deepEqual(chunkSpeech('It is 3.5 at example.com today. Next.', 300), ['It is 3.5 at example.com today. Next.'])
  assert.deepEqual(chunkSpeech('It is 3.5 at example.com today. Next.', 31), ['It is 3.5 at example.com today.', 'Next.'])
})

test('chunkSpeech does not cut a surrogate pair', () => {
  const out = chunkSpeech('😀😀😀😀😀😀', 5)
  assert.equal(out.join(''), '😀😀😀😀😀😀')
  for (const c of out) assert.ok(!/[\ud800-\udbff]$/.test(c) && !/^[\udc00-\udfff]/.test(c), c)
})
