// SPDX-License-Identifier: MIT
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import React, { useRef } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { radioKeyTarget, radioTabStop } from '../../packages/ui-kit/src/segmentedModel.js'

// Compile the production Segmented; only its model import is a boundary.
const source = readFileSync(new URL('../../packages/ui-kit/src/Segmented.jsx', import.meta.url), 'utf8')
const h = React.createElement
const compiled = ts.transpileModule(source.replace(/^import .*\n/gm, '').replace('export default function', 'function'), {
  compilerOptions: { jsx: ts.JsxEmit.React, target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None }, fileName: 'Segmented.jsx',
}).outputText
const Segmented = new Function('React', 'useRef', 'radioKeyTarget', 'radioTabStop', `${compiled}; return Segmented`)(React, useRef, radioKeyTarget, radioTabStop)
const html = (props) => renderToStaticMarkup(h(Segmented, props))

const opts = [{ id: 'a', label: 'A' }, { id: 'b', label: 'B', disabled: true }, { id: 'c', label: 'C' }, { id: 'd', label: 'D' }]

test('segmented: filter mode keeps the pressed-button markup', () => {
  const out = html({ options: opts, value: 'a', label: 'Show' })
  assert.equal(out, '<div class="ui-seg" role="group" aria-label="Show"><button type="button" class="ui-seg__item" aria-pressed="true" data-filter="a">A</button><button type="button" class="ui-seg__item" aria-pressed="false" disabled="" data-filter="b">B</button><button type="button" class="ui-seg__item" aria-pressed="false" data-filter="c">C</button><button type="button" class="ui-seg__item" aria-pressed="false" data-filter="d">D</button></div>')
})

test('segmented: radio mode draws a radiogroup with one tab stop', () => {
  const out = html({ mode: 'radio', options: opts, value: 'c', label: 'Pick' })
  assert.match(out, /role="radiogroup" aria-label="Pick"/)
  assert.equal((out.match(/role="radio"/g) || []).length, 4)
  assert.doesNotMatch(out, /aria-pressed/)
  assert.match(out, /aria-checked="true" tabindex="0" data-filter="c"/)
  assert.equal((out.match(/tabindex="-1"/g) || []).length, 3)
})

test('segmented: radio mode with no match puts the tab stop on the first enabled item', () => {
  const out = html({ mode: 'radio', options: [{ id: 'x', label: 'X', disabled: true }, ...opts.slice(2)], value: 'zzz', label: 'Pick' })
  assert.match(out, /aria-checked="false" tabindex="0" data-filter="c"/)
})

test('segmented: radio mode names the group by an element when labelledBy is set', () => {
  const out = html({ mode: 'radio', options: opts, value: 'a', labelledBy: 'title-1' })
  assert.match(out, /aria-labelledby="title-1"/)
  assert.doesNotMatch(out, /aria-label="/)
})

test('segmented: arrows wrap and skip disabled options, Home and End pick the ends', () => {
  assert.equal(radioKeyTarget(opts, 0, 'ArrowRight'), 2)
  assert.equal(radioKeyTarget(opts, 0, 'ArrowDown'), 2)
  assert.equal(radioKeyTarget(opts, 3, 'ArrowRight'), 0)
  assert.equal(radioKeyTarget(opts, 0, 'ArrowLeft'), 3)
  assert.equal(radioKeyTarget(opts, 2, 'ArrowUp'), 0)
  assert.equal(radioKeyTarget(opts, 2, 'Home'), 0)
  assert.equal(radioKeyTarget(opts, 0, 'End'), 3)
  assert.equal(radioKeyTarget([{ id: 'a', disabled: true }, { id: 'b' }, { id: 'c', disabled: true }], 1, 'Home'), 1)
  assert.equal(radioKeyTarget(opts, 0, 'Enter'), -1)
  assert.equal(radioKeyTarget(opts, 0, 'Tab'), -1)
  assert.equal(radioKeyTarget([{ id: 'a', disabled: true }], 0, 'ArrowRight'), -1)
})

test('segmented: the tab stop is the checked item, else the first enabled one', () => {
  assert.equal(radioTabStop(opts, 'd'), 3)
  assert.equal(radioTabStop(opts, 'none'), 0)
  assert.equal(radioTabStop(opts, 'b'), 0)
  assert.equal(radioTabStop([{ id: 'a', disabled: true }], 'a'), -1)
})
