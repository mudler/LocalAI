// The notice a page frame shows takes its colours from the host theme.
import test from 'node:test'
import assert from 'node:assert/strict'
import { buildPageSrcdoc } from '../../src/utils/pagesBridge.js'

const info = (doc) => JSON.parse(/var info = (\{.*\})\n/.exec(doc)[1])

test('page notice: the theme colours reach the frame, and only plain hex colours do', () => {
  const doc = buildPageSrcdoc('<p>x</p>', { name: 'n', banner: 'b', colors: { bg: '#f8e6c4', fg: '#7a4a00', accent: 'red;}</style>' } })
  const colors = info(doc).colors
  assert.deepEqual(colors, { bg: '#f8e6c4', fg: '#7a4a00' })
})

test('page notice: no colours means system colours, not literals', () => {
  const doc = buildPageSrcdoc('<p>x</p>', { name: 'n', banner: 'b' })
  assert.deepEqual(info(doc).colors, {})
  assert.match(doc, /'Canvas'/)
  assert.doesNotMatch(doc, /#[0-9a-fA-F]{6}\b/)
})
