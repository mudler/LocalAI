// SPDX-License-Identifier: MIT
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import React, { useCallback, useMemo, useState } from 'react'
import { renderToString } from 'react-dom/server'
import * as slash from '../../src/utils/slashCommands.js'

// Compile the entire production component, not a selected callback or source slice.
// Leaf components and service hooks are boundaries; React executes all composer hooks.
const source = readFileSync(new URL('../../src/components/chat/ChatComposer.jsx', import.meta.url), 'utf8')
const names = [...source.matchAll(/^import (\w+) from/gm)].map(m => m[1])
const leaf = () => null
let captured
const scope = {
  ...Object.fromEntries(names.map(n => [n, leaf])), ...slash,
  React, useCallback, useMemo, useState,
  useTranslation: () => ({ t: k => k }), useAuth: () => ({ isAdmin: true, user: { id: 'a' } }),
  useModelFit: () => ({ resources: null }), useCommandsNotice: () => ({}), useUserCommands: () => [],
  hostMemory: () => null, agentFolderLine: () => '', CAP_CHAT: 'chat',
  CHAT_SLASH_ACTIONS: [], SLASH_ACTIONS: [],
  HomeComposer: props => { captured = props; return React.createElement('textarea', { defaultValue: props.message }) },
  useToolGroups: () => [], useChatGrants: () => null, SAVED_AGENTS: 'saved_agents', SUBAGENTS: 'subagents',
  CommandsChip: leaf, CommandsNotice: leaf, ToolFallbacks: leaf, ChatToolsControls: leaf,
  MemoryChip: leaf, MemoryLine: leaf, memoryOff: () => null, MEMORY_GROUP: 'memory',
}
const compiled = ts.transpileModule(source.replace(/^import .*\n/gm, '').replace('export default function', 'function'), {
  compilerOptions: { jsx: ts.JsxEmit.React, target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None }, fileName: 'ChatComposer.jsx',
}).outputText
const Composer = new Function(...Object.keys(scope), `${compiled}; return ChatComposer`)(...Object.values(scope))

test('full ChatComposer renders and about uses only selected command status', () => {
  for (const [state, expected] of [['available', 'isolated'], ['host', 'host'], ['provider-host', 'host'], ['unknown', 'unknown'], ['loading', 'unknown'], ['', 'unknown'], ['none', 'none']]) {
    const composer = {
      drafts: { text: '', files: [], setText() {}, setFiles() {} }, models: [], agents: [],
      switches: { commands: { state }, builtins: [], addons: [], servers: [], connectors: [], skills: [], fallbacks: [] },
    }
    let result
    assert.match(renderToString(React.createElement(Composer, { composer, onRunAction: (_, value) => { result = value } })), /textarea/)
    captured.onRunAction('about')
    assert.equal(result.about.commands, expected, state)
  }
})
