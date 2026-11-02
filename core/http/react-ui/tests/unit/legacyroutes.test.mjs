import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { bareChatsRedirect, chatsRedirect, MIGRATION_ROUTES } from '../../src/utils/legacyRoutes.js'

const here = dirname(fileURLToPath(import.meta.url))
const router = readFileSync(join(here, '../../src/router.jsx'), 'utf8')

import { addonPageAddress } from '../../src/addons/addresses.js'

test('removed addon routes have no registrations and native routes remain', () => {
 for (const path of ['studio','studio/:tab','studio/:tab/:model','talk','image','video','3d','tts','sound','diarization','transform','face','voice','voice-library','voice-library/new','fine-tune','quantize']) {
  assert.ok(!router.includes(`path: '${path}'`), path)
  assert.ok(!router.includes(`path: '${path}/:model'`), path + '/:model')
 }
 assert.ok(router.includes("path: '*'"))
 for (const name of ['StudioTabRedirect','StudioRedirect','TalkRedirect','AddonRedirect','AddonsOffRedirect']) assert.ok(!router.includes(name), name)
})
test('generic addon addresses retain nested pages selected model query and hash', () => {
 assert.equal(addonPageAddress({addon:'arbitrary',page:'voice/library/new',model:'org/name:雪?&',search:'?x=1',hash:'#h'}), '/app/addons/arbitrary/voice/library/new?x=1&model=org%2Fname%3A%E9%9B%AA%3F%26#h')
})
test('chatsRedirect opens the matching page and keeps the query and the hash; the old chat page opens Home', () => {
  assert.equal(chatsRedirect('/app/chat'), '/app')
  assert.equal(chatsRedirect('/app/chat/'), '/app')
  assert.equal(chatsRedirect('/app/chat/llama', '?x=1', '#top'), '/app?x=1&model=llama#top')
  // A model id may hold "/" or ":", so it travels as ?model= and is decoded first.
  assert.equal(chatsRedirect('/app/chat/org%2Fname%3Aq4'), '/app?model=org%2Fname%3Aq4')
  assert.equal(chatsRedirect('/app/chats/agents'), '/app/agents')
  assert.equal(chatsRedirect('/app/chats/agents', '?a=b'), '/app/agents?a=b')
  assert.equal(chatsRedirect('/app/chats/triggers'), '/app/triggers')
  assert.equal(chatsRedirect('/app/agent-jobs'), '/app/triggers')
  assert.equal(chatsRedirect('/app/agent-jobs/jobs/j1'), '/app/triggers')
  assert.equal(chatsRedirect('/app/agent-jobs/tasks/new', '?x=1'), '/app/triggers?x=1')
  assert.equal(chatsRedirect('/app/chats/skills'), '/app/skills')
  assert.equal(chatsRedirect('/app/chats/skills/', '', '#a'), '/app/skills#a')
})

test('every old chat, agent, job and skill route redirects, and no built-in page is left behind', () => {
  for (const r of MIGRATION_ROUTES.filter(x => x.kind === 'chats')) {
    assert.equal(chatsRedirect(r.from), r.to, r.from)
  }
  for (const path of ["'chat'", "'chat/:model'", "'chats/agents'", "'chats/skills'", "'chats/triggers'", "'agent-jobs'", "'agent-jobs/*'"]) {
    const entry = router.split('\n').find(l => l.includes(`{ path: ${path}, element:`))
    assert.ok(entry?.includes('<ChatsRedirect />'), `no ChatsRedirect for ${path}`)
  }
  assert.ok(!/pages\/(Chats|AgentRun|AgentStatus|AgentJobDetails|ChatAgents|ChatSkills|ChatTriggers)'/.test(router))
})

test('chatsRedirect keeps a model name that is not valid percent-encoding as typed', () => {
  assert.equal(chatsRedirect('/app/chat/100%'), '/app?model=100%25')
  assert.equal(chatsRedirect('/app/chat/a%zzb'), '/app?model=a%25zzb')
})

test('/app/chats as an address opens Home with its query; a hand-off with state stays on the empty chat', () => {
  assert.equal(bareChatsRedirect('/app/chats'), '/app')
  assert.equal(bareChatsRedirect('/app/chats/'), '/app')
  assert.equal(bareChatsRedirect('/app/chats', '?model=llama', '#x'), '/app?model=llama#x')
  assert.equal(bareChatsRedirect('/app/chats', '?model=llama', '', null), '/app?model=llama')
  // Home's first message, an agent, a draft or the page's own marker.
  for (const state of [{ start: 'hi' }, { agentId: 'a1' }, { draft: 'x' }, { newChat: true }]) {
    assert.equal(bareChatsRedirect('/app/chats', '?model=llama', '', state), '', JSON.stringify(state))
  }
  // A transcript and the other chats pages are not touched.
  for (const p of ['/app/chats/c1', '/app/chats/all', '/app/chats/connectors']) assert.equal(bareChatsRedirect(p), '', p)
})
