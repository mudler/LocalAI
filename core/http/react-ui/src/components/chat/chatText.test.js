import assert from 'node:assert/strict'
import test from 'node:test'

import {
  editableMessageText, escapeHtml, isActivityRole, messageText, splitError, withEditedMessageText,
} from './chatText.js'

test('a message is a string or a list of blocks, and the text is the first text block', () => {
  assert.equal(messageText('hello'), 'hello')
  assert.equal(messageText([{ type: 'image_url', image_url: { url: 'x' } }, { type: 'text', text: 'look' }]), 'look')
  assert.equal(messageText(null), '')
  assert.equal(messageText([{ type: 'image_url' }]), '')
})

test('only a message with a text block can be edited, and an edit keeps the other blocks', () => {
  const withFile = {
    role: 'user',
    content: [
      { type: 'text', text: 'what does it say' },
      { type: 'text', text: '\n\n--- File: a.txt ---\nsecret\n--- End of a.txt ---' },
    ],
    files: [{ name: 'a.txt', type: 'file' }],
  }
  assert.equal(editableMessageText({ role: 'user', content: 'plain' }), 'plain')
  assert.equal(editableMessageText(withFile), 'what does it say')
  assert.equal(editableMessageText({ role: 'user', content: [{ type: 'image_url' }] }), null)
  assert.equal(editableMessageText({ role: 'user', content: null }), null)

  const edited = withEditedMessageText(withFile, 'edited')
  assert.equal(edited.content[0].text, 'edited')
  assert.equal(edited.content[1], withFile.content[1])
  assert.deepEqual(edited.files, withFile.files)
  assert.equal(withEditedMessageText({ role: 'user', content: 'a' }, 'b').content, 'b')
})

test('an error is the tail the chat hook appends, not any mention of the word', () => {
  assert.deepEqual(splitError('Error: out of memory'), { text: '', message: 'out of memory' })
  assert.deepEqual(splitError('Half an answer\n\nError: link dropped'), { text: 'Half an answer', message: 'link dropped' })
  assert.equal(splitError('It prints Error: file not found when the path is wrong.'), null)
  assert.equal(splitError('Fine answer'), null)
  assert.equal(splitError(null), null)
})

test('activity roles are the four that fold into one line', () => {
  for (const role of ['thinking', 'reasoning', 'tool_call', 'tool_result']) assert.equal(isActivityRole(role), true)
  for (const role of ['user', 'assistant', 'system']) assert.equal(isActivityRole(role), false)
})

test('escaping keeps typed text from becoming markup', () => {
  assert.equal(escapeHtml('<b>"a" & \'b\'</b>'), '&lt;b&gt;&quot;a&quot; &amp; &#39;b&#39;&lt;/b&gt;')
})
