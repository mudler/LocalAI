// SPDX-License-Identifier: MIT
// The kit's Tabs, TabPanel and Tag: the markup of the tablist wiring, the
// keyboard rules of the model and the tag classes.
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import React, { useId, useRef } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { tabId, tabKeyTarget, tabPanelId, tabStop } from '../../packages/ui-kit/src/tabsModel.js'

const h = React.createElement
// Compile a kit file with its imports replaced by the given bindings.
function load(file, names, values, exportName) {
  const source = readFileSync(new URL(`../../packages/ui-kit/src/${file}`, import.meta.url), 'utf8')
  const compiled = ts.transpileModule(
    source.replace(/^import .*\n/gm, '').replace(/export default function/g, 'function').replace(/export function/g, 'function'),
    { compilerOptions: { jsx: ts.JsxEmit.React, target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None }, fileName: file },
  ).outputText
  return new Function('React', ...names, `${compiled}; return { ${exportName} }`)(React, ...values)
}
const Icon = ({ name, className }) => h('i', { 'data-icon': name, className })
const { Tabs, TabPanel } = load('Tabs.jsx', ['useId', 'useRef', 'Icon', 'tabId', 'tabKeyTarget', 'tabPanelId', 'tabStop'],
  [useId, useRef, Icon, tabId, tabKeyTarget, tabPanelId, tabStop], 'Tabs, TabPanel')
const { Tag } = load('Tag.jsx', ['Icon'], [Icon], 'Tag')

const items = [{ id: 'a', label: 'A' }, { id: 'b', label: 'B', disabled: true }, { id: 'c', label: 'C', count: 3 }, { id: 'd', label: 'D' }]

test('tabs: a tablist of tabs wired to their panels, one tab stop', () => {
  const out = renderToStaticMarkup(h(Tabs, { items, value: 'c', idPrefix: 'x', label: 'Views' }))
  assert.match(out, /^<div class="dk-tabs ui-tabs" role="tablist" aria-label="Views" data-variant="underline">/)
  assert.equal((out.match(/role="tab"/g) || []).length, 4)
  assert.match(out, /id="x-tab-c" class="dk-tab ui-tabs__tab" aria-selected="true" aria-controls="x-panel-c" tabindex="0"/)
  assert.equal((out.match(/tabindex="-1"/g) || []).length, 3)
  assert.match(out, /id="x-tab-b"[^>]*disabled=""/)
  assert.match(out, /<span class="dk-badge dk-badge--count ui-tabs__count">3<\/span>/)
})

test('tabs: one shared panel, a pill variant, a named count and a hidden count', () => {
  const out = renderToStaticMarkup(h(Tabs, {
    items: [{ id: 'a', label: 'A', icon: 'settings' }, { id: 'b', label: 'B', count: 2, countTone: 'warn', countLabel: '2 waiting' }, { id: 'c', label: 'C', count: 1, countHidden: true }],
    value: 'zzz', idPrefix: 'y', panelId: 'one', variant: 'pill', labelledBy: 'h1', itemTestId: 't-',
  }))
  assert.match(out, /class="dk-segmented ui-tabs" role="tablist" aria-labelledby="h1"/)
  assert.doesNotMatch(out, /aria-label="[^"]*" aria-labelledby/)
  assert.equal((out.match(/aria-controls="one"/g) || []).length, 3)
  assert.match(out, /class="dk-seg ui-tabs__tab" aria-selected="false" aria-controls="one" tabindex="0" data-tab="a" data-testid="t-a"><i data-icon="settings"/)
  assert.match(out, /dk-badge--warn" aria-label="2 waiting">2</)
  assert.match(out, /aria-hidden="true">1</)
})

test('tabs: without an idPrefix the tabs get ids and no aria-controls', () => {
  const out = renderToStaticMarkup(h(Tabs, { items, value: 'a', label: 'Views' }))
  assert.equal((out.match(/ id="tabs[^"]*-tab-[a-d]"/g) || []).length, 4)
  assert.doesNotMatch(out, /aria-controls/)
})

test('tabs: the panel is labelled by its tab', () => {
  const out = renderToStaticMarkup(h(TabPanel, { idPrefix: 'x', value: 'c', className: 'extra', tabIndex: 0 }, 'body'))
  assert.equal(out, '<div tabindex="0" role="tabpanel" id="x-panel-c" aria-labelledby="x-tab-c" class="dk-tabpanel extra">body</div>')
  assert.match(renderToStaticMarkup(h(TabPanel, { idPrefix: 'x', value: 'c', panelId: 'one' })), /id="one" aria-labelledby="x-tab-c"/)
})

test('tabs: the arrows of the axis, Home and End move; other keys do not', () => {
  assert.equal(tabKeyTarget(items, 0, 'ArrowRight'), 2)
  assert.equal(tabKeyTarget(items, 3, 'ArrowRight'), 0)
  assert.equal(tabKeyTarget(items, 0, 'ArrowLeft'), 3)
  assert.equal(tabKeyTarget(items, 2, 'Home'), 0)
  assert.equal(tabKeyTarget(items, 0, 'End'), 3)
  assert.equal(tabKeyTarget(items, 0, 'ArrowDown'), -1)
  assert.equal(tabKeyTarget(items, 0, 'ArrowDown', 'vertical'), 2)
  assert.equal(tabKeyTarget(items, 0, 'ArrowRight', 'vertical'), -1)
  assert.equal(tabKeyTarget(items, 0, 'Enter'), -1)
})

test('tabs: the tab stop and the ids', () => {
  assert.equal(tabStop(items, 'd'), 3)
  assert.equal(tabStop(items, 'b'), 0)
  assert.equal(tabId('p', 'k'), 'p-tab-k')
  assert.equal(tabPanelId('p', 'k'), 'p-panel-k')
  assert.equal(tabPanelId('p', 'k', 'shared'), 'shared')
})

test('tag: a neutral label by default, a tone, mono and an icon', () => {
  assert.equal(renderToStaticMarkup(h(Tag, null, 'alias')), '<span class="ui-tag">alias</span>')
  assert.equal(renderToStaticMarkup(h(Tag, { tone: 'warn', mono: true, icon: 'cpu', title: 'Why', testId: 'tg' }, 'Q4')),
    '<span class="ui-tag ui-tag--warn ui-tag--mono" title="Why" data-testid="tg"><i data-icon="cpu" class="ui-tag__icon"></i>Q4</span>')
})
