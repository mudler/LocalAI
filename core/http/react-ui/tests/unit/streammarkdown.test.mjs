// Tests for streamMarkdown.streamSegments
import test from 'node:test'
import assert from 'node:assert/strict'
import { streamSegments } from '../../src/utils/streamMarkdown.js'

// 1. Plain text is one prose segment
test('stream markdown: plain text is one prose segment', () => {
  const result = streamSegments('Hello\nWorld')
  assert.equal(result.length, 1)
  assert.equal(result[0].kind, 'prose')
  assert.equal(result[0].text, 'Hello\nWorld')
  assert.ok(result[0].key.startsWith('prose-'))
})

// 2. A closed fence is a code segment with its language
test('stream markdown: a closed fence is a code segment with its language', () => {
  const result = streamSegments('```js\nconsole.log(1)\n```')
  assert.equal(result.length, 1)
  assert.equal(result[0].kind, 'code')
  assert.equal(result[0].lang, 'js')
  assert.equal(result[0].code, 'console.log(1)')
  assert.equal(result[0].open, false)
})

// 3. An unterminated fence is open and keeps its text (with trailing newline)
test('stream markdown: an unterminated fence is open and keeps its text', () => {
  const result = streamSegments('```js\nconsole.log(1)\n')
  assert.equal(result.length, 1)
  assert.equal(result[0].kind, 'code')
  assert.equal(result[0].lang, 'js')
  assert.equal(result[0].code, 'console.log(1)\n')
  assert.equal(result[0].open, true)
})

// 3b. No trailing newline when input has none
test('stream markdown: no trailing newline when input has none', () => {
  const result = streamSegments('```js\nconsole.log(1)')
  assert.equal(result.length, 1)
  assert.equal(result[0].kind, 'code')
  assert.equal(result[0].lang, 'js')
  assert.equal(result[0].code, 'console.log(1)')
  assert.equal(result[0].open, true)
})

// 4. Text after a closed fence is its own prose segment
test('stream markdown: text after a closed fence is its own prose segment', () => {
  const result = streamSegments('```js\nx\n```\nHello')
  assert.equal(result.length, 2)
  assert.equal(result[0].kind, 'code')
  assert.equal(result[1].kind, 'prose')
  assert.equal(result[1].text, 'Hello')
})

// 5. Keys are start offsets so earlier segments survive growth
test('stream markdown: keys are start offsets so earlier segments survive growth', () => {
  const base = 'Hello\n```js\nx\n```'
  const extended = base + '\nWorld'
  const segs1 = streamSegments(base)
  const segs2 = streamSegments(extended)
  // Every segment in segs1 that also appears in segs2 by key must have the same kind
  const keys1 = new Set(segs1.map(s => s.key))
  for (const s of segs2) {
    if (keys1.has(s.key)) {
      const matching = segs1.find(m => m.key === s.key)
      assert.equal(matching.kind, s.kind, `key ${s.key} kind mismatch`)
    }
  }
})

// 6. Tilde fences close only with tildes
test('stream markdown: tilde fences close only with tildes', () => {
  const result = streamSegments('~~~js\nbacktick\n```\n~~~')
  assert.equal(result.length, 1)
  assert.equal(result[0].kind, 'code')
  assert.equal(result[0].code, 'backtick\n```')
  assert.equal(result[0].open, false)
})

// 7. The info string keeps only the language
test('stream markdown: the info string keeps only the language', () => {
  const result = streamSegments('```js -a -b\nx\n```')
  assert.equal(result.length, 1)
  assert.equal(result[0].lang, 'js')
})

// 8. An empty text has no segments
test('stream markdown: an empty text has no segments', () => {
  assert.equal(streamSegments('').length, 0)
  assert.equal(streamSegments('  ').length, 1)
  assert.equal(streamSegments('  ')[0].kind, 'prose')
})

// 9. A closing fence with trailing spaces closes
test('stream markdown: a closing fence with trailing spaces closes', () => {
  const result = streamSegments('```js\nx\n```  ')
  assert.equal(result.length, 1)
  assert.equal(result[0].kind, 'code')
  assert.equal(result[0].open, false)
  assert.equal(result[0].code, 'x')
})

// 10. Prose is split at a paragraph boundary
test('stream markdown: prose is split at a paragraph boundary', () => {
  const result = streamSegments('First.\n\nSecond.')
  assert.equal(result.length, 2)
  assert.equal(result[0].kind, 'prose')
  assert.equal(result[0].text, 'First.')
  assert.equal(result[1].kind, 'prose')
  assert.equal(result[1].text, 'Second.')
})

// 11. An ordered list keeps both items in one segment
test('stream markdown: an ordered list keeps both items in one segment', () => {
  const result = streamSegments('Steps:\n\n1. one\n\n2. two\n\nDone.')
  assert.equal(result.length, 2)
  assert.equal(result[0].kind, 'prose')
  assert.equal(result[0].text, 'Steps:\n\n1. one\n\n2. two')
  assert.equal(result[1].kind, 'prose')
  assert.equal(result[1].text, 'Done.')
})

// 12. A loose list item keeps its indented paragraph
test('stream markdown: a loose list item keeps its indented paragraph', () => {
  const result = streamSegments('1. one\n\n   more\n\n2. two')
  assert.equal(result.length, 1)
  assert.equal(result[0].kind, 'prose')
  assert.equal(result[0].text, '1. one\n\n   more\n\n2. two')
})

// 13. A blockquote stays one segment
test('stream markdown: a blockquote stays one segment', () => {
  const result = streamSegments('> a\n\n> b')
  assert.equal(result.length, 1)
  assert.equal(result[0].kind, 'prose')
  assert.equal(result[0].text, '> a\n\n> b')
})

// 14. A chunk with reference syntax stays whole
test('stream markdown: a chunk with reference syntax stays whole', () => {
  const result = streamSegments('[x][r]\n\n[r]: https://example.com')
  assert.equal(result.length, 1)
  assert.equal(result[0].kind, 'prose')
  assert.equal(result[0].text, '[x][r]\n\n[r]: https://example.com')
})

// 15. An earlier paragraph keeps its offset when text grows
test('stream markdown: an earlier paragraph keeps its offset when text grows', () => {
  const segs1 = streamSegments('One.\n\nTwo.')
  const segs2 = streamSegments('One.\n\nTwo.\n\nThree.')
  assert.equal(segs1[0].key, segs2[0].key)
  assert.equal(segs1[0].text, segs2[0].text)
})

// 16. Keys are the character offsets of later segments too
test('stream markdown: keys are the character offsets of later segments too', () => {
  const result = streamSegments('A.\n\nB.\n\nC.')
  assert.equal(result.length, 3)
  assert.equal(result[0].key, 'prose-0')
  assert.equal(result[1].key, 'prose-4')
  assert.equal(result[2].key, 'prose-8')
})
