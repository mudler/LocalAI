// SPDX-License-Identifier: MIT
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { NOTICE_ICONS, noticeRole, noticeSeverity, noticeAction } from '../../src/components/ui/noticeModel.js'

// Compile the production Notice. Button and Callout are boundaries with their
// own markup rules; they draw plain elements here so the test reads the order
// and the actions Notice asks for.
const source = readFileSync(new URL('../../src/components/ui/Notice.jsx', import.meta.url), 'utf8')
const h = React.createElement
const scope = {
  React, NOTICE_ICONS, noticeAction, noticeRole, noticeSeverity,
  Button: ({ to, href, onClick, children, variant, ...rest }) => (to !== undefined || href !== undefined
    ? h('a', { href: to ?? href, 'data-variant': variant, ...rest }, children)
    : h('button', { type: 'button', onClick, 'data-variant': variant, ...rest }, children)),
  Callout: ({ tone, icon, title, actions, role, className, testId, children, ...rest }) => h('div', { className, role, 'data-tone': tone, 'data-icon': icon, 'data-testid': testId, ...rest },
    title ? h('strong', null, title) : null, h('div', { className: 'body' }, children), actions ? h('div', { className: 'actions' }, actions) : null),
}
const compiled = ts.transpileModule(source.replace(/^import .*\n/gm, '').replace('export default function', 'function'), {
  compilerOptions: { jsx: ts.JsxEmit.React, target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None }, fileName: 'Notice.jsx',
}).outputText
const Notice = new Function(...Object.keys(scope), `${compiled}; return Notice`)(...Object.values(scope))
const html = (props) => renderToStaticMarkup(h(Notice, props))

test('notice: what, why, who and the fix come in that order', () => {
  const out = html({ severity: 'warn', title: 'WHAT', reason: 'WHY', who: 'WHO', fix: { label: 'FIX', to: '/app/settings' } })
  const order = ['WHAT', 'WHY', 'WHO', 'FIX'].map(w => out.indexOf(w))
  assert.ok(order.every(i => i >= 0), out)
  assert.deepEqual([...order].sort((a, b) => a - b), order)
  assert.match(out, /role="status"/)
  assert.match(out, /data-tone="warn"/)
  assert.match(out, /href="\/app\/settings"/)
  assert.match(out, /data-variant="primary"/)
})

test('notice: severity sets the role and the icon', () => {
  assert.match(html({ severity: 'error', title: 'T' }), /role="alert"/)
  assert.match(html({ severity: 'info', title: 'T' }), /role="note"/)
  assert.match(html({ severity: 'info', title: 'T', icon: 'terminal' }), /data-icon="terminal"/)
  assert.match(html({ severity: 'error', title: 'T' }), /data-icon="alert-circle"/)
  assert.match(html({ title: 'T' }), /data-tone="info"/)
})

test('notice: no fix means no button, and a dead action is not drawn', () => {
  assert.doesNotMatch(html({ title: 'T', reason: 'R' }), /<button|<a /)
  assert.doesNotMatch(html({ title: 'T', fix: { label: 'Go' } }), /<button|<a /)
  const both = html({ title: 'T', fix: { label: 'Upgrade', onClick() {} }, secondary: { label: 'Hide', onClick() {} } })
  assert.match(both, /data-variant="primary"[^>]*>Upgrade/)
  assert.match(both, /data-variant="ghost"[^>]*>Hide/)
})

test('notice: the reason and who lines are left out when empty', () => {
  const out = html({ title: 'T' })
  assert.doesNotMatch(out, /ui-notice__reason|ui-notice__who/)
  assert.match(out, /ui-notice/)
})

test('notice: an add-on update state maps to a severity, up to date has none', async () => {
  const { updateNoticeSeverity } = await import('../../src/components/addons/updateState.js')
  assert.equal(updateNoticeSeverity('update-available'), 'warn')
  assert.equal(updateNoticeSeverity('incompatible'), 'warn')
  assert.equal(updateNoticeSeverity('check-failed'), 'warn')
  assert.equal(updateNoticeSeverity('integrity-error'), 'error')
  assert.equal(updateNoticeSeverity('unknown'), 'info')
  assert.equal(updateNoticeSeverity('up-to-date'), '')
})
