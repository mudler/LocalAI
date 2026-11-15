import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { DISCORD_LISTS, PERMISSIONS, PERMISSION_INTEGER, badEntries, discordSecret, inviteUrl, isSnowflake, tokenProblem } from '../src/ids.js'
import { canSave, connectorPayload, emptyConnector, listEntries } from '../../../internal/connector-ui/src/connector.js'

const TOKEN = 'MTAwMDAwMDAwMDAwMDAwMDAw.GabcDE.' + 'x'.repeat(27)

test('discord: the token goes in the request only when it was typed, and a bad one never does', () => {
  assert.equal(discordSecret({ token: ` ${TOKEN} ` }), TOKEN)
  assert.equal(discordSecret({}), '')
  assert.equal(tokenProblem({}), '')
  assert.equal(tokenProblem({ token: 'Bot ' + TOKEN }), 'tokenSpace', 'a pasted prefix has a space in it')
  assert.equal(tokenProblem({ token: 'short' }), 'tokenShape')
  assert.equal(discordSecret({ token: 'short' }), '')
  const form = { ...emptyConnector(), name: 'Bot', agent_id: 'a', token: TOKEN }
  const body = connectorPayload(form, 'discord', discordSecret(form))
  assert.equal(body.kind, 'discord')
  assert.equal(body.token, TOKEN)
  const update = connectorPayload({ ...emptyConnector(), id: 'b1', name: 'Bot', agent_id: 'a' }, 'discord', '')
  assert.equal('token' in update, false)
  assert.equal(canSave(form, false, Boolean(discordSecret(form))), true)
  assert.equal(canSave({ ...form, token: '' }, false, false), false)
})

test('discord: an ID is 17 to 20 digits, and names, servers typed as words and URLs are not', () => {
  assert.equal(isSnowflake('123456789012345678'), true)
  assert.equal(isSnowflake('12345678901234567'), true)
  assert.equal(isSnowflake('1234'), false)
  assert.equal(isSnowflake('@ada'), false)
  assert.equal(isSnowflake('ada#1234'), false)
  assert.equal(isSnowflake('123456789012345678 '), false)
  assert.equal(isSnowflake('https://discord.com/channels/1/2'), false)
})

test('discord: entries that are not IDs are named and block saving; the anyone mode checks none', () => {
  const ok = '123456789012345678'
  assert.deepEqual(badEntries({ allow: [ok, '@ada', 'general'] }), ['@ada', 'general'])
  assert.deepEqual(badEntries({ allow: ['@ada'], accept_any: true }), [])
  assert.deepEqual(badEntries({ allow: [ok] }), [])
  assert.deepEqual(badEntries({}), [])
})

test('discord: people and channels share one list of bare IDs', () => {
  assert.equal(DISCORD_LISTS.length, 1)
  const allow = ['123456789012345678', '223456789012345678']
  assert.deepEqual(listEntries(allow, DISCORD_LISTS, 0), allow)
  const body = connectorPayload({ ...emptyConnector(), name: 'B', agent_id: 'a', token: TOKEN, allow: [...allow, allow[0]] }, 'discord', TOKEN)
  assert.deepEqual(body.allow, allow, 'kept once')
})

test('discord: the invite link asks for the five permissions and for no more', () => {
  assert.equal(PERMISSION_INTEGER, '274878008320')
  assert.deepEqual(PERMISSIONS.map((p) => p.bit), [10, 11, 15, 16, 38])
  const id = '123456789012345678'
  assert.equal(inviteUrl(id), `https://discord.com/oauth2/authorize?client_id=${id}&scope=bot&permissions=274878008320`)
  assert.equal(inviteUrl(` ${id} `), inviteUrl(id))
  assert.equal(inviteUrl(''), '')
  assert.equal(inviteUrl('abc'), '')
  assert.equal(inviteUrl('1&permissions=8'), '', 'nothing else goes into the link')
  const administrator = 8n, manageMessages = 1n << 13n, mentionEveryone = 1n << 17n
  const bits = BigInt(PERMISSION_INTEGER)
  for (const bad of [administrator, manageMessages, mentionEveryone]) assert.equal(bits & bad, 0n)
})

test('discord: the words say what the anyone mode does and does not give', () => {
  const words = JSON.parse(readFileSync(new URL('../src/i18n/discord.en.json', import.meta.url), 'utf8'))
  for (const want of ['public channels', 'read-only', 'untrusted', 'prompt injection', 'hold no permission']) {
    assert.ok(words.anyoneWarning.includes(want), want)
  }
  assert.ok(words.answer.conversationHelp.includes('!new'))
  assert.ok(words.setup.step3.includes('Message Content Intent'))
  assert.ok(words.setup.never.includes('Administrator'))
  for (const key of ['title', 'intro', 'token', 'agent', 'save', 'test', 'testOk']) assert.ok(words[key], key)
})
