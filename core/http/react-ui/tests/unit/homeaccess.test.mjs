// What Home shows to someone who is not an administrator: the strip must not
// say "No models loaded" when the server never said which models are loaded
// (GET /system is for administrators), and a picked saved agent must not hide
// "Manage setups". The production components are compiled and rendered; leaf
// components are stubs.
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import React, { useState } from 'react'
import { renderToString } from 'react-dom/server'
import { hostMemory, memoryFigure, fillStyle, percent } from '../../src/components/home/memory.js'

function load(path, exportName, scope) {
  const source = readFileSync(new URL(`../../src/${path}`, import.meta.url), 'utf8')
    .replace(/^import .*\n/gm, '')
    .replace(/^export default function /m, 'function ')
    .replace(/^export function /gm, 'function ')
  const compiled = ts.transpileModule(source, {
    compilerOptions: { jsx: ts.JsxEmit.React, target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None },
    fileName: path,
  }).outputText
  return new Function(...Object.keys(scope), `${compiled}; return ${exportName}`)(...Object.values(scope))
}

const t = (k) => k
const useTranslation = () => ({ t })
const Icon = () => null
let buttons = []
const Button = (props) => {
  buttons.push(props)
  return React.createElement('button', { 'data-testid': props['data-testid'], disabled: props.disabled }, props.children)
}

const Strip = load('components/home/HomeMemoryStrip.jsx', 'HomeMemoryStrip', {
  React, useTranslation, hostMemory, memoryFigure, fillStyle, percent, Icon, Button,
})
const strip = (props) => renderToString(React.createElement(Strip, {
  open: true, onOpenChange() {}, resources: null, cluster: null, stagingOp: null, failedOp: null,
  onStop() {}, onStopAll() {}, ...props,
}))

test('home strip: unknown loaded models (no administrator, /system refused) claim nothing', () => {
  const html = strip({ models: null })
  assert.doesNotMatch(html, /strip\.noneLoaded/)
  assert.doesNotMatch(html, /statusLine\.noModelsLoaded/)
  assert.doesNotMatch(html, /home-stat-loaded-none/)
})

test('home strip: an administrator with nothing loaded still reads "No models loaded"', () => {
  const html = strip({ models: [] })
  assert.match(html, /home-stat-loaded-none/)
  assert.match(html, /strip\.noneLoaded/)
})

test('home strip: loaded models are counted and listed', () => {
  const html = strip({ models: [{ id: 'qwen3-8b', backend: 'llama-cpp' }] })
  assert.match(html, /home-stat-loaded/)
  assert.match(html, /qwen3-8b/)
  assert.doesNotMatch(html, /strip\.noneLoaded/)
})

const { SetupReason } = load('components/chat/SetupStatusLine.jsx', '{ SetupStatusLine, SetupReason }', {
  React, useState, useTranslation, Icon, Button, Callout: () => null, MAX_NAME: 60,
})

test('setup reason: with a saved agent picked, Manage setups stays reachable', () => {
  buttons = []
  let managed = 0
  const html = renderToString(React.createElement(SetupReason, {
    id: 'setup-chip-reason', text: 'setup.agentReason', onManage: () => { managed += 1 },
  }))
  assert.match(html, /setup\.agentReason/)
  const manage = buttons.find((b) => b['data-testid'] === 'setup-reason-manage')
  assert.ok(manage, 'the reason line offers Manage setups')
  assert.equal(manage.children, 'setup.manage')
  assert.ok(!manage.disabled)
  manage.onClick()
  assert.equal(managed, 1)
})

test('setup reason: without setups to manage there is no button', () => {
  buttons = []
  const html = renderToString(React.createElement(SetupReason, { id: 'setup-chip-reason', text: 'setup.agentReason' }))
  assert.match(html, /setup\.agentReason/)
  assert.equal(buttons.length, 0)
})

// The chip itself stays off while the agent decides the chat: a setup can be
// neither applied nor saved from it.
const SwitchPopover = ({ disabled, testId, label }) =>
  React.createElement('button', { 'data-testid': `${testId}-button`, disabled }, label)
const SetupChip = load('components/chat/SetupChip.jsx', 'SetupChip', {
  React, useTranslation, Icon, Button, SwitchHead: () => null, SwitchPopover, setupSummary: () => '',
})

test('setup chip: a picked saved agent keeps applying and saving off', () => {
  const props = { list: [{ id: 'a', name: 'A' }], activeId: 'a', edited: false, reasonId: 'r', onPick() {}, onSaveAs() {}, onManage() {} }
  assert.match(renderToString(React.createElement(SetupChip, { ...props, inactiveReason: 'setup.agentReason' })), /disabled=""/)
  assert.doesNotMatch(renderToString(React.createElement(SetupChip, props)), /disabled=""/)
})
