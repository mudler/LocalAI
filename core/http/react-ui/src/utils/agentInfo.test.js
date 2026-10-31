import assert from 'node:assert/strict'
import test from 'node:test'

import { agentInfo } from './agentInfo.js'
import { diffConfig, exportAgent, importAgent, maskSecrets, summarise } from './agentConfigTools.js'

test('agent info reads add-ons, MCP servers, collections and skills from the definition only', () => {
  const info = agentInfo({
    model: 'm', addons: ['office'], mcp_servers: ['filesystem', 'connector:c1'],
    collections: ['docs'], skills: ['citations'], instructions: 'You   are\ncareful.',
  })
  assert.deepEqual(info.tools, ['office'])
  assert.deepEqual(info.mcp, ['filesystem'])
  assert.deepEqual(info.connectors, ['c1'])
  assert.deepEqual(info.memory, ['docs'])
  assert.deepEqual(info.skills, ['citations'])
  assert.equal(info.instructions, 'You are careful.')
})

test('an agent with nothing granted reports none', () => {
  const info = agentInfo({ model: 'm' })
  assert.deepEqual(info.memory, [])
  assert.deepEqual(info.skills, [])
  assert.deepEqual(agentInfo(null).tools, [])
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
    { name: 'a', model: 'm1', description: '', addons: [] },
    { name: 'a', model: 'm2', description: undefined, addons: ['office'], hud: false },
  )
  assert.deepEqual(d.map(x => x.key), ['model', 'addons'])
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

test('JSON text is the same value however it is spaced', () => {
  const d = diffConfig({ mcp_stdio_servers: '{"mcpServers":{"a":{"command":"x"}}}' }, { mcp_stdio_servers: '{\n  "mcpServers": {\n    "a": { "command": "x" }\n  }\n}' })
  assert.equal(d.length, 0)
})

test('an export holds the definition and no id, dates or secrets, and imports back unchanged', () => {
  const saved = { id: 'a1', created: 'x', updated: 'y', name: 'Reviewer', description: 'd', instructions: 'Be brief.', model: 'm', mcp_servers: ['crm'], addons: ['office'], collections: ['docs'], skills: ['review'] }
  const file = exportAgent(saved)
  assert.equal(file.id, undefined)
  assert.equal(file.created, undefined)
  const back = importAgent(JSON.parse(JSON.stringify(file)))
  assert.equal(back.legacy, false)
  assert.deepEqual(back.dropped, [])
  assert.deepEqual(back.agent, { name: 'Reviewer', description: 'd', instructions: 'Be brief.', model: 'm', mcp_servers: ['crm'], addons: ['office'], collections: ['docs'], skills: ['review'] })
})

test('the export of the old agents imports what maps and names what was dropped', () => {
  const back = importAgent({
    name: 'helper', description: 'Helps', model: 'm', system_prompt: 'Be kind.', selected_skills: ['web'],
    enable_kb: true, actions: [{ name: 'search' }], connectors: [{ type: 'slack', config: '{}' }],
    mcp_servers: [{ url: 'https://tools.example.org', token: 's' }], periodic_runs: '', standalone_job: false,
  })
  assert.equal(back.legacy, true)
  assert.equal(back.agent.instructions, 'Be kind.')
  assert.deepEqual(back.agent.skills, ['web'])
  assert.deepEqual(back.agent.mcp_servers, [])
  assert.deepEqual(back.dropped.sort(), ['actions', 'connectors', 'enable_kb', 'mcp_servers'])
})

test('a file that is not an agent is refused', () => {
  assert.equal(importAgent(null), null)
  assert.equal(importAgent([]), null)
  assert.equal(importAgent({ kind: 'something.else', name: 'x' }), null)
  assert.equal(importAgent({ foo: 1 }), null)
})
