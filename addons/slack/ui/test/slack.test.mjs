import test from 'node:test'
import assert from 'node:assert/strict'
import { APP_MANIFEST, SLACK_LISTS, isChannel, slackSecret, tokenProblem } from '../src/tokens.js'
import { canSave, connectorPayload, emptyConnector, listEntries, replaceList } from '../../../internal/connector-ui/src/connector.js'

test('slack: both tokens make one secret, and only when both are right', () => {
  assert.equal(slackSecret({ bot_token: ' xoxb-1 ', app_token: 'xapp-2' }), 'xoxb-1 xapp-2')
  assert.equal(slackSecret({}), '')
  assert.equal(slackSecret({ bot_token: 'xoxb-1' }), '')
  assert.equal(slackSecret({ bot_token: 'xapp-2', app_token: 'xoxb-1' }), '')
  assert.equal(tokenProblem({}), '')
  assert.equal(tokenProblem({ app_token: 'xapp-2' }), 'tokensBoth')
  assert.equal(tokenProblem({ bot_token: 'abc', app_token: 'xapp-2' }), 'botShape')
  assert.equal(tokenProblem({ bot_token: 'xoxb-1', app_token: 'abc' }), 'appShape')
})

test('slack: the secret goes in the request only when it was typed', () => {
  const form = { ...emptyConnector(), name: 'App', agent_id: 'a', bot_token: 'xoxb-1', app_token: 'xapp-2' }
  const body = connectorPayload(form, 'slack', slackSecret(form))
  assert.equal(body.kind, 'slack')
  assert.equal(body.token, 'xoxb-1 xapp-2')
  assert.equal('bot_token' in body, false)
  const update = connectorPayload({ ...emptyConnector(), id: 'b1', name: 'App', agent_id: 'a' }, 'slack', '')
  assert.equal('token' in update, false)
  assert.equal(canSave(form, false, Boolean(slackSecret(form))), true)
  assert.equal(canSave({ ...form, app_token: '' }, false, false), false)
})

test('slack: people and channels are two lists of one allowlist', () => {
  assert.equal(isChannel('C0123'), true)
  assert.equal(isChannel('G9'), true)
  assert.equal(isChannel('U0123'), false)
  assert.equal(isChannel('@ada'), false)
  const allow = ['U1', 'C1', '@ada', 'G2']
  assert.deepEqual(listEntries(allow, SLACK_LISTS, 0), ['U1', '@ada'])
  assert.deepEqual(listEntries(allow, SLACK_LISTS, 1), ['C1', 'G2'])
  assert.deepEqual(replaceList(allow, SLACK_LISTS, 1, ['C9']), ['U1', '@ada', 'C9'])
  assert.deepEqual(replaceList(allow, SLACK_LISTS, 0, []), ['C1', 'G2'])
})

test('slack: the app manifest turns on Socket Mode and asks for the scopes the add-on uses', () => {
  for (const want of ['socket_mode_enabled: true', 'app_mentions:read', 'im:history', 'chat:write', 'files:read', 'users:read', 'message.im', 'app_mention']) {
    assert.ok(APP_MANIFEST.includes(want), want)
  }
})
