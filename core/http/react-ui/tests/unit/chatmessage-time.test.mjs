// SPDX-License-Identifier: MIT
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import React, { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'

// The message view keeps the time each line was written, from the transcript's
// At. Compile the production component (imports stubbed) and check what it draws.
const source = readFileSync(new URL('../../src/components/chat/ChatMessage.jsx', import.meta.url), 'utf8')
const leaf = () => null
const scope = {
  React, memo: c => c, useEffect, useLayoutEffect, useMemo, useRef, useState,
  useTranslation: () => ({ t: k => k, i18n: { language: 'en-GB' } }),
  Link: leaf, failureView: () => ({}), renderMarkdown: s => s, escapeHtml: s => s,
  messageParts: m => ({ text: m.text || '', attachments: [] }), chatFileTarget: () => null,
  spaceSafeLinks: s => s, filesApi: {}, iconHtml: () => '', InvokedChip: leaf, MessageAttachments: leaf,
  ActivityGroup: leaf, ActivityProposals: leaf, StreamingActivity: leaf, Icon: leaf, SenderLine: leaf,
  AddonSlot: leaf, AssistantAvatar: leaf, ReadAloudButton: leaf,
}
const compiled = ts.transpileModule(source.replace(/^import .*\n/gm, '').replace('export default memo(ChatMessage)', 'const ChatMessageView = ChatMessage').replace(/^export /gm, ''), {
  compilerOptions: { jsx: ts.JsxEmit.React, target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None }, fileName: 'ChatMessage.jsx',
}).outputText
const ChatMessage = new Function(...Object.keys(scope), `${compiled}; return ChatMessageView`)(...Object.values(scope))

const actions = { copy() {}, edit() {}, focusRelative() {}, fork() {}, regenerate() {} }
const render = (msg, extra = {}) => renderToStaticMarkup(React.createElement(ChatMessage, { msg, actions, model: 'm', warm: true, ...extra }))

test('a person message shows the time it was written', () => {
  const html = render({ seq: 1, role: 'user', text: 'hi', at: '2026-11-20T10:05:00Z' })
  assert.match(html, /<time[^>]*data-testid="message-time"[^>]*>/)
  assert.match(html, /dateTime="2026-11-20T10:05:00\.000Z"/)
})

test('a reply shows its time in the line above it', () => {
  const html = render({ seq: 2, role: 'assistant', text: 'hello', at: '2026-11-20T10:06:00Z' })
  assert.match(html, /class="cx-who__time"/)
  assert.match(html, /dateTime="2026-11-20T10:06:00\.000Z"/)
})

test('a message without a usable time shows no time', () => {
  for (const at of [undefined, '', '0001-01-01T00:00:00Z', 'not a date']) {
    const html = render({ seq: 3, role: 'user', text: 'hi', at })
    assert.doesNotMatch(html, /message-time/, `at=${JSON.stringify(at)}`)
  }
})
