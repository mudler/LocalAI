import assert from 'node:assert/strict'
import test from 'node:test'

import { escapeHtml, isTyping } from './chatText.js'

test('escaping keeps typed text from becoming markup', () => {
  assert.equal(escapeHtml('<b>"a" & \'b\'</b>'), '&lt;b&gt;&quot;a&quot; &amp; &#39;b&#39;&lt;/b&gt;')
})

test('a text field counts as typing, other elements do not', () => {
  assert.equal(isTyping({ tagName: 'TEXTAREA' }), true)
  assert.equal(isTyping({ tagName: 'INPUT' }), true)
  assert.equal(isTyping({ tagName: 'DIV', isContentEditable: true }), true)
  assert.equal(isTyping({ tagName: 'BUTTON' }), false)
  assert.equal(isTyping(null), false)
})
