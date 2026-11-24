import test from 'node:test'
import assert from 'node:assert/strict'
import {
  POLL_MS, activeProfile, disabledReason, draftView, errorKey, everyone, needsPolling, normalizePolicy, parseQuota, parseUsers, percentOf,
  draftProfileToSave, policyToSend, profileMode, pulling, samePolicy, setPinned, sourceOf, toggleRole, withAccess, withDraftChoice,
} from '../../src/utils/chatSandboxProfile.js'
import { createHostSettingsClient } from '../../src/utils/hostSettingsClient.js'
import { createAddonsApi } from '../../src/utils/addonsApi.js'
import { HOST_CONTROL_IDS } from '../../src/addons/hostControls.js'
import { agentPayload, emptyAgent } from '../../src/utils/chatAgents.js'

const view = (over = {}) => ({
  loaded: true, applies: true, profile: 'base', default: 'base', source: 'default',
  profiles: [
    { id: 'base', title: 'Base', allowed: true, state: { phase: 'ready' } },
    { id: 'media', title: 'Media', allowed: true, state: { phase: 'idle' } },
    { id: 'video', title: 'Video', allowed: false, state: { phase: 'absent' } },
  ], ...over,
})

test('sandbox profile: the row shows only for a loaded, isolated provider with profiles', () => {
  assert.equal(profileMode(view()), 'row')
  assert.equal(profileMode(view({ applies: false })), 'hidden')
  assert.equal(profileMode(view({ loaded: false })), 'hidden')
  assert.equal(profileMode(view({ profiles: [] })), 'hidden')
  assert.equal(profileMode(null), 'hidden')
})

test('sandbox profile: the active profile is the named one, else the default', () => {
  assert.equal(activeProfile(view({ profile: 'media' })).id, 'media')
  assert.equal(activeProfile(view({ profile: 'gone' })).id, 'base')
})

test('sandbox profile: a pulling image asks for a poll every two seconds', () => {
  assert.equal(POLL_MS, 2000)
  assert.equal(needsPolling(view()), false)
  const v = view({ profiles: [{ id: 'base', title: 'Base', state: { phase: 'pulling', percent: 41.6 } }] })
  assert.equal(needsPolling(v), true)
  assert.equal(pulling(v).length, 1)
  assert.equal(percentOf(v.profiles[0]), 42)
  assert.equal(percentOf({ state: { percent: 400 } }), 100)
  assert.equal(percentOf({}), 0)
})

test('sandbox profile: a profile is disabled when not allowed or without an image', () => {
  assert.equal(disabledReason(view().profiles[0]), '')
  assert.equal(disabledReason({ allowed: false }), 'notAllowed')
  assert.equal(disabledReason({ allowed: true, state: { phase: 'absent' } }), 'absent')
})

test('sandbox profile: the server error type picks the message', () => {
  assert.equal(errorKey({ body: { error: { type: 'sandbox_profile_not_allowed' } } }), 'notAllowedError')
  assert.equal(errorKey({ body: { error: { type: 'sandbox_profile_unknown' } } }), 'unknownError')
  assert.equal(errorKey(new Error('x')), '')
  assert.equal(sourceOf({ source: 'agent' }), 'agent')
  assert.equal(sourceOf({ source: 'odd' }), 'default')
})

test('sandbox profile: a new chat shows the default from the list', () => {
  const v = draftView({ addon: 'sandbox', default: 'media', profiles: [{ id: 'base', title: 'Base' }, { id: 'media', title: 'Media' }] })
  assert.equal(v.draft, true)
  assert.equal(activeProfile(v).id, 'media')
  assert.equal(v.applies, true)
  assert.equal(draftView({ profiles: [] }).applies, false)
})

test('sandbox profile policy: everyone may use a profile until it is restricted', () => {
  let p = normalizePolicy(null)
  assert.deepEqual(p, { default: '', access: {}, memory_mib: 0, disk_mib: 0, pin_previous: [] })
  assert.equal(everyone(p, 'media'), true)
  p = toggleRole(p, 'media', 'admin')
  assert.equal(everyone(p, 'media'), false)
  assert.deepEqual(p.access.media, { roles: ['admin'], users: [] })
  p = withAccess(p, 'media', { users: parseUsers('alice, bob  alice') })
  assert.deepEqual(p.access.media.users, ['alice', 'bob'])
  p = toggleRole(p, 'media', 'admin')
  p = withAccess(p, 'media', { users: [] })
  assert.equal('media' in p.access, false)
  assert.equal(everyone(p, 'media'), true)
})

test('sandbox profile policy: pins, quotas and what is sent', () => {
  let p = setPinned(normalizePolicy({}), 'media', true)
  assert.deepEqual(p.pin_previous, ['media'])
  p = setPinned(p, 'media', false)
  assert.deepEqual(p.pin_previous, [])
  assert.equal(parseQuota(''), 0)
  assert.equal(parseQuota('4096'), 4096)
  assert.ok(Number.isNaN(parseQuota('-1')))
  assert.ok(Number.isNaN(parseQuota('1.5')))
  assert.ok(Number.isNaN(parseQuota('abc')))
  const q = { default: 'media', access: { video: { roles: [], users: [] }, media: { roles: ['admin'], users: [] } }, memory_mib: 1024, disk_mib: 0, pin_previous: ['media'] }
  assert.deepEqual(policyToSend(q), { default: 'media', access: { media: { roles: ['admin'], users: [] } }, memory_mib: 1024, disk_mib: 0, pin_previous: ['media'] })
  assert.equal(samePolicy(q, policyToSend(q)), false)
  assert.equal(samePolicy({}, normalizePolicy({})), true)
})

const defaults = { chats_egress_ceiling: '', chats_egress_default: '', chats_egress_allow_private: false, chats_egress_ports: '', chats_egress_extra_registries: '', chats_egress_allowed_hosts: '', chats_tool_providers: {}, auto_upgrade_addons: false, connector_files_ceiling_bytes: 0 }
const reply = (sandbox) => new Response(JSON.stringify({ payload: { data: { schema_version: 1, revision: 1, policy: { ...defaults, sandbox_profiles: sandbox } } } }), { status: 200 })
const readWith = (sandbox) => createHostSettingsClient({ api: createAddonsApi({ fetch: async () => reply(sandbox) }) }).read()

test('owner client: reads a valid sandbox_profiles policy and refuses a broken one', async () => {
  const ok = { default: 'media', access: { media: { roles: ['admin'], users: ['alice'] } }, memory_mib: 4096, disk_mib: 0, pin_previous: ['media'] }
  assert.deepEqual((await readWith(ok)).policy.sandbox_profiles, ok)
  for (const bad of [undefined, {}, { ...ok, memory_mib: -1 }, { ...ok, access: { media: { roles: 'admin', users: [] } } }, { ...ok, extra: 1 }, { ...ok, pin_previous: [''] }]) {
    await assert.rejects(readWith(bad), { kind: 'unavailable' })
  }
})

test('host controls: the sandbox-profiles control is registered', () => {
  assert.ok(HOST_CONTROL_IDS.includes('sandbox-profiles'))
})

test('saved agents: the sandbox profile is sent once chosen, empty for the default', () => {
  assert.equal('sandbox_profile' in agentPayload({ ...emptyAgent(), name: 'a' }), false)
  assert.equal(agentPayload({ ...emptyAgent(), name: 'a', sandbox_profile: '' }).sandbox_profile, '')
  assert.equal(agentPayload({ ...emptyAgent(), name: 'a', sandbox_profile: ' media ' }).sandbox_profile, 'media')
})

test('sandbox profile: a new chat starts with the profile the person picked', () => {
  const v = view({ draft: true, selected: '', source: 'default' })
  const picked = withDraftChoice(v, 'media')
  assert.equal(activeProfile(picked).id, 'media')
  assert.equal(picked.selected, 'media')
  assert.equal(picked.source, 'chat')
  // A profile the administrator does not allow, one with no image, or an unknown one is ignored.
  assert.equal(withDraftChoice(v, 'video'), v)
  assert.equal(withDraftChoice(v, 'gone'), v)
  assert.equal(withDraftChoice(v, ''), v)
})

test('sandbox profile: the draft saves a profile only when it differs from the default', () => {
  const v = view({ draft: true })
  assert.equal(draftProfileToSave(v, 'media'), 'media')
  assert.equal(draftProfileToSave(v, 'base'), '')
  assert.equal(draftProfileToSave(v, ''), '')
  assert.equal(draftProfileToSave(v, 'video'), '')
})
