// SPDX-License-Identifier: MIT
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import * as React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { transformWithOxc } from 'vite'

test('config password input preserves the real value on rerender and edits', async () => {
  // Exercise the renderer's actual default-input JSX without loading unrelated
  // editors (CodeMirror and model selectors require a browser).
  const source = readFileSync(new URL('../../src/components/ConfigFieldRenderer.jsx', import.meta.url), 'utf8')
  const input = source.slice(source.lastIndexOf('<input'), source.lastIndexOf('/>') + 2)
  const { code } = await transformWithOxc(`return (${input})`, 'field.jsx', { jsx: { runtime: 'classic' } })
  const renderInput = new Function('React', 'component', 'value', 'handleChange', 'field', code)
  const secret = 'fake-secret<&"'
  const values = { 'proxy.api_key': secret, name: 'before' }
  const render = () => renderInput(React, 'password', values['proxy.api_key'], value => {
    values['proxy.api_key'] = value
  }, {})

  const first = render()
  assert.equal(first.props.type, 'password')
  assert.equal(first.props.autoComplete, 'new-password')
  assert.equal(first.props.value, secret)
  assert.match(renderToStaticMarkup(first), /type="password"/)
  values.name = 'after'
  assert.equal(render().props.value, secret)
  assert.equal(JSON.parse(JSON.stringify(values))['proxy.api_key'], secret)
  render().props.onChange({ target: { value: 'replacement-secret' } })
  assert.equal(render().props.value, 'replacement-secret')
  render().props.onChange({ target: { value: '' } })
  assert.equal(render().props.value, '')
  assert.equal(renderInput(React, 'password', undefined, () => {}, {}).props.value, '')
  assert.equal(renderInput(React, 'input', 'plain', () => {}, {}).props.type, 'text')
})
