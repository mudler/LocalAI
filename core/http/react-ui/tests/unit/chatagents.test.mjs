import test from 'node:test'
import assert from 'node:assert/strict'
import { addEntry, agentPayload, emptyAgent, grantSummary, removeEntry } from '../../src/utils/chatAgents.js'

test('chat agents: an entry is trimmed and added once', () => {
  assert.deepEqual(addEntry([], ' office '), ['office'])
  assert.deepEqual(addEntry(['office'], 'office'), ['office'])
  assert.deepEqual(addEntry(['office'], '   '), ['office'])
  assert.deepEqual(removeEntry(['a', 'b'], 'a'), ['b'])
})

test('chat agents: the request body is trimmed, and its lists have no blanks or repeats', () => {
  const body = agentPayload({
    ...emptyAgent(), name: '  Reviewer ', description: ' d ', instructions: 'Be brief.\n', model: ' m ',
    addons: ['office', ' office', ''], skills: ['review'], mcp_servers: undefined,
  })
  assert.equal(body.name, 'Reviewer')
  assert.equal(body.description, 'd')
  assert.equal(body.instructions, 'Be brief.\n')
  assert.equal(body.model, 'm')
  assert.deepEqual(body.addons, ['office'])
  assert.deepEqual(body.mcp_servers, [])
  assert.deepEqual(body.collections, [])
})

test('chat agents: the list line says what an agent grants, and nothing for a bare agent', () => {
  assert.equal(grantSummary({ addons: ['a'], skills: ['x', 'y'] }), '1 add-on, 2 skills')
  assert.equal(grantSummary({ mcp_servers: ['crm'], collections: ['docs'] }), '1 MCP server, 1 collection')
  assert.equal(grantSummary(emptyAgent()), '')
})

test('chat agents: the network of unattended runs is off unless chosen, and only a known preset is sent', () => {
  assert.equal(emptyAgent().network, 'off')
  assert.equal(agentPayload({ ...emptyAgent(), name: 'a', network: 'registries' }).network, 'registries')
  assert.equal(agentPayload({ ...emptyAgent(), name: 'a', network: 'everything' }).network, 'off')
  assert.equal(agentPayload({ name: 'a' }).network, 'off')
})
