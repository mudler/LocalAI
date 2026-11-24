// SPDX-License-Identifier: MIT
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import React, { Fragment, forwardRef, useCallback, useEffect, useId, useImperativeHandle, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import * as model from '../../src/components/ui/listPickerModel.js'

const {
  FILTER_FROM, filterRows, groupHeading, initialActive, isRowSelected, moveActive,
  rowMatches, showFilterBox, toggleValue,
} = model

const rows = [
  { id: 'llama-cpp', label: 'llama-cpp', group: 'Text', keywords: ['gguf'] },
  { id: 'vllm', label: 'vLLM', group: 'Text', secondary: 'CUDA' },
  { id: 'whisper', label: 'whisper', group: 'Audio', description: 'Speech to text' },
]

test('a query matches the label, secondary text, description, id and keywords', () => {
  assert.deepEqual(filterRows(rows, '').map(r => r.id), ['llama-cpp', 'vllm', 'whisper'])
  assert.deepEqual(filterRows(rows, 'VLL').map(r => r.id), ['vllm'])
  assert.deepEqual(filterRows(rows, 'cuda').map(r => r.id), ['vllm'])
  assert.deepEqual(filterRows(rows, 'speech').map(r => r.id), ['whisper'])
  assert.deepEqual(filterRows(rows, 'gguf').map(r => r.id), ['llama-cpp'])
  assert.deepEqual(filterRows(rows, '  whis ').map(r => r.id), ['whisper'])
  assert.deepEqual(filterRows(rows, 'nothing'), [])
  // A label that is not text (a node) is skipped, not thrown on.
  assert.equal(rowMatches({ id: 'x', label: { type: 'span' } }, 'x'), true)
})

test('the filter box shows from FILTER_FROM rows unless forced', () => {
  assert.equal(FILTER_FROM, 8)
  assert.equal(showFilterBox(undefined, 7), false)
  assert.equal(showFilterBox(undefined, 8), true)
  assert.equal(showFilterBox(true, 1), true)
  assert.equal(showFilterBox(false, 100), false)
  assert.equal(showFilterBox(undefined, 3, 3), true)
})

test('the arrows wrap, Home and End move only with an empty filter', () => {
  assert.equal(moveActive('ArrowDown', 0, 3), 1)
  assert.equal(moveActive('ArrowDown', 2, 3), 0)
  assert.equal(moveActive('ArrowUp', 0, 3), 2)
  assert.equal(moveActive('ArrowUp', 2, 3), 1)
  assert.equal(moveActive('Home', 2, 3), 0)
  assert.equal(moveActive('End', 0, 3), 2)
  assert.equal(moveActive('Home', 2, 3, true), null)
  assert.equal(moveActive('End', 0, 3, true), null)
  assert.equal(moveActive('ArrowDown', 0, 0), 0)
  assert.equal(moveActive('End', 0, 0), 0)
  assert.equal(moveActive('Enter', 1, 3), null)
  assert.equal(moveActive('a', 1, 3), null)
})

test('the chosen row is active when a single choice opens', () => {
  assert.equal(initialActive(rows, 'whisper', false), 2)
  assert.equal(initialActive(rows, 'gone', false), 0)
  assert.equal(initialActive(rows, ['whisper'], true), 0)
  const none = { id: '', none: true }
  assert.equal(initialActive([none, ...rows], '', false), 0)
  // An explicit selected flag wins over the value.
  assert.equal(initialActive([{ id: 'a', selected: false }, { id: 'b', selected: true }], 'a', false), 1)
})

test('selection of single, multiple and the none row', () => {
  assert.equal(isRowSelected(rows[0], 'llama-cpp', false), true)
  assert.equal(isRowSelected(rows[0], 'vllm', false), false)
  assert.equal(isRowSelected(rows[0], ['llama-cpp'], true), true)
  assert.equal(isRowSelected(rows[0], 'llama-cpp', true), false)
  const none = { id: '', none: true }
  assert.equal(isRowSelected(none, '', false), true)
  assert.equal(isRowSelected(none, undefined, false), true)
  assert.equal(isRowSelected(none, 'vllm', false), false)
  assert.equal(isRowSelected(none, [], true), true)
  assert.equal(isRowSelected(none, ['vllm'], true), false)
})

test('a group heading starts each block of rows', () => {
  assert.equal(groupHeading(rows, 0), 'Text')
  assert.equal(groupHeading(rows, 1), null)
  assert.equal(groupHeading(rows, 2), 'Audio')
  assert.equal(groupHeading(rows, 2, { Audio: 'Speech' }), 'Speech')
  assert.equal(groupHeading([{ id: 'a' }], 0), null)
  // A row after a row with no group starts a block again.
  assert.equal(groupHeading([{ id: 'n' }, { id: 'a', group: 'g' }], 1), 'g')
})

test('toggleValue adds and removes one id', () => {
  assert.deepEqual(toggleValue([], 'a'), ['a'])
  assert.deepEqual(toggleValue(['a', 'b'], 'a'), ['b'])
  assert.deepEqual(toggleValue(undefined, 'a'), ['a'])
})

// Compile the production ListPicker; its imports are boundaries.
const source = readFileSync(new URL('../../src/components/ui/ListPicker.jsx', import.meta.url), 'utf8')
const h = React.createElement
const compiled = ts.transpileModule(source.replace(/^\/\/ eslint.*\n/gm, '').replace(/^import [\s\S]*?from .*\n/gm, '').replace(/^import .*\n/gm, '').replace('export default ListPicker', ''), {
  compilerOptions: { jsx: ts.JsxEmit.React, target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None }, fileName: 'ListPicker.jsx',
}).outputText
const Icon = ({ name, className }) => h('i', { 'data-icon': name, className })
const scope = {
  React, Fragment, forwardRef, useCallback, useEffect, useId, useImperativeHandle, useLayoutEffect, useMemo, useRef, useState,
  useTranslation: () => ({ t: k => `t:${k}` }), Icon, ...model,
}
const ListPicker = new Function(...Object.keys(scope), `${compiled}; return ListPicker`)(...Object.values(scope))
const html = (props) => renderToStaticMarkup(h(ListPicker, props))

test('the closed trigger shows the chosen label, the placeholder or the count', () => {
  const chosen = html({ items: rows, value: 'vllm', onChange() {}, ariaLabel: 'Backend', testId: 'pick' })
  assert.match(chosen, /aria-haspopup="listbox"/)
  assert.match(chosen, /aria-expanded="false"/)
  assert.match(chosen, /aria-label="Backend"/)
  assert.match(chosen, /aria-describedby="[^"]+-text"/)
  assert.match(chosen, /data-testid="pick"/)
  assert.match(chosen, /class="home-chip__text">vLLM</)
  assert.match(chosen, /class="home-chip"/)

  const empty = html({ items: rows, value: '', onChange() {}, placeholder: 'Choose' })
  assert.match(empty, /home-chip__text--placeholder">Choose</)

  const field = html({ items: rows, value: '', variant: 'field', onChange() {} })
  assert.match(field, /class="list-picker list-picker--field"/)
  assert.match(field, /class="home-chip home-chip--field"/)

  const multi = html({ items: rows, value: ['vllm', 'whisper'], multiple: true, onChange() {}, testId: 'm' })
  assert.match(multi, /vLLM, whisper/)
  assert.match(multi, /data-testid="m-count">2</)
  assert.doesNotMatch(html({ items: rows, value: ['vllm'], multiple: true, showCount: false, onChange() {} }), /home-chip__count/)

  const mono = html({ items: [{ id: 'a', label: 'a-model', mono: true }], value: 'a', onChange() {} })
  assert.match(mono, /home-chip__text--mono/)

  const clear = html({ items: rows, value: 'vllm', clearable: true, onChange() {} })
  assert.match(clear, /aria-label="t:picker.clear"/)
  assert.doesNotMatch(html({ items: rows, value: '', clearable: true, onChange() {} }), /picker.clear/)
})

test('the trigger can be replaced and given attributes', () => {
  const out = html({
    items: rows, value: 'vllm', onChange() {},
    triggerContent: h('span', { className: 'own' }, 'mine'),
    triggerProps: { 'data-testid': 'own-chip', title: 'Pick' }, triggerClassName: 'extra',
  })
  assert.match(out, /class="own">mine</)
  assert.match(out, /data-testid="own-chip"/)
  assert.match(out, /title="Pick"/)
  assert.match(out, /class="home-chip extra"/)
  assert.match(out, /data-icon="chevron-down"/)
})

test('placeMenu keeps a menu inside the room its trigger has', () => {
  const { placeMenu } = model
  // Fits from the trigger's left edge: no change.
  assert.deepEqual(placeMenu({ rootLeft: 100, rootRight: 300, menuWidth: 400, boundsLeft: 0, boundsRight: 1000 }), { left: 0, width: null })
  // Too wide to the right: it hangs from the trigger's right edge.
  assert.deepEqual(placeMenu({ rootLeft: 700, rootRight: 900, menuWidth: 400, boundsLeft: 0, boundsRight: 1000 }), { left: -200, width: null })
  // A side sheet from 960 to 1440: a 560 px menu would leave it on the left
  // after the flip, so it is narrowed to the sheet and kept inside.
  assert.deepEqual(placeMenu({ rootLeft: 985, rootRight: 1415, menuWidth: 560, boundsLeft: 960, boundsRight: 1440 }), { left: -17, width: 464 })
  // Wider than the window: narrowed, and never left of the margin.
  assert.deepEqual(placeMenu({ rootLeft: 20, rootRight: 100, menuWidth: 400, boundsLeft: 0, boundsRight: 300 }), { left: -12, width: 284 })
})
