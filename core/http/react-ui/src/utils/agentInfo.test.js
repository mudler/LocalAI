import assert from 'node:assert/strict'
import test from 'node:test'

import { agentInfo } from './agentInfo.js'
import { diffConfig, maskSecrets, summarise } from './agentConfigTools.js'

test('agent info reads tools, mcp, memory and skills from the config only', () => {
  const info = agentInfo({
    model: 'm', actions: [{ name: 'web_search' }, { name: 'read_page' }],
    mcp_stdio_servers: JSON.stringify({ mcpServers: { filesystem: { command: 'x' } } }),
    mcp_servers: [{ url: 'https://tools.example.org/mcp', token: 's' }],
    enable_kb: true, enable_skills: true, selected_skills: ['citations'],
    system_prompt: 'You   are\ncareful.',
  })
  assert.deepEqual(info.tools, ['web_search', 'read_page'])
  assert.deepEqual(info.mcp, ['filesystem', 'tools.example.org'])
  assert.deepEqual(info.memory, ['knowledge'])
  assert.deepEqual(info.skills, ['citations'])
  assert.equal(info.instructions, 'You are careful.')
})

test('an agent with no memory and no skills reports none', () => {
  const info = agentInfo({ model: 'm' })
  assert.deepEqual(info.memory, [])
  assert.deepEqual(info.skills, [])
  assert.deepEqual(agentInfo(null).tools, [])
})

test('all skills is its own word', () => {
  const info = agentInfo({ enable_skills: true })
  assert.deepEqual(info.skills, ['all'])
  assert.equal(info.skillsAll, true)
})

test('secrets are hidden at any depth, including inside JSON strings', () => {
  const masked = maskSecrets({
    api_key: 'abc', name: 'x',
    mcp_servers: [{ url: 'https://a', token: 'tok' }],
    connectors: [{ type: 'slack', config: '{"botToken":"xoxb-1","channel":"c"}' }],
  })
  assert.equal(masked.api_key, '••••••')
  assert.equal(masked.name, 'x')
  assert.equal(masked.mcp_servers[0].token, '••••••')
  assert.equal(JSON.parse(masked.connectors[0].config).botToken, '••••••')
  assert.equal(JSON.parse(masked.connectors[0].config).channel, 'c')
})

test('the diff lists changed fields and treats empty and missing alike', () => {
  const d = diffConfig(
    { name: 'a', model: 'm1', description: '', actions: [] },
    { name: 'a', model: 'm2', description: undefined, actions: [{ name: 'x' }], hud: false },
  )
  assert.deepEqual(d.map(x => x.key), ['model', 'actions'])
})

test('a section summary names what is set', () => {
  const fields = [
    { name: 'name', type: 'text', label: 'Name' },
    { name: 'hud', type: 'checkbox', label: 'Show HUD' },
    { name: 'key', type: 'password', label: 'Key' },
    { name: 'description', type: 'textarea', label: 'Description' },
  ]
  assert.equal(summarise(fields, { name: 'a', hud: true, key: 'x', description: 'Does things' }), 'a, Show HUD, Does things')
  assert.equal(summarise(fields, {}), '')
})

import { workingLine } from './agentInfo.js'

test('an observable without a completion is work in flight', () => {
  assert.equal(workingLine([]), null)
  assert.equal(workingLine([{ id: 1, name: 'x', completion: {} }]), null)
  assert.equal(workingLine([{ id: 1, name: 'web_search', creation: { function_definition: { name: 'web_search' } } }]), 'web_search')
  assert.equal(
    workingLine([{ id: 2, name: 'job', creation: { chat_completion_request: { messages: [{ content: 'Read the  page' }] } } }]),
    'Read the page',
  )
})

test('JSON text is the same value however it is spaced', () => {
  const d = diffConfig({ mcp_stdio_servers: '{"mcpServers":{"a":{"command":"x"}}}' }, { mcp_stdio_servers: '{\n  "mcpServers": {\n    "a": { "command": "x" }\n  }\n}' })
  assert.equal(d.length, 0)
})
