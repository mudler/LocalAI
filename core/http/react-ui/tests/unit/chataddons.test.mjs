import test from 'node:test'
import assert from 'node:assert/strict'
import {
  toolAddons, enabledAddonIds, toggleAddonSettings, toggleAllAddonSettings, addonRequestMetadata,
} from '../../src/utils/chatAddons.js'

const TOOL = { name: 'run' }
const apps = [
  { id: 'notes', title: 'Notes', icon: '/i/notes', enabled: true, state: 'running', toolDefinitions: [TOOL] },
  { id: 'indexer', name: 'indexer', enabled: true, state: 'running', toolDefinitions: [TOOL, TOOL] },
  { id: 'viewer', title: 'Viewer', enabled: true, state: 'running', toolDefinitions: [] },
  { id: 'bare', title: 'Bare', enabled: true, state: 'running' },
  { id: 'idle', title: 'Idle', enabled: false, state: 'stopped', toolDefinitions: [TOOL] },
  { id: 'broken', title: 'Broken', enabled: true, state: 'failed', toolDefinitions: [TOOL] },
]

test('chat add-ons: only running add-ons that ship tools are listed', () => {
  const list = toolAddons(apps)
  assert.deepEqual(list.map(a => a.id), ['notes', 'indexer'])
  assert.equal(list[0].title, 'Notes')
  assert.equal(list[1].title, 'indexer')
  assert.deepEqual(toolAddons(undefined), [])
})

test('chat add-ons: a chat with no list uses the older boolean for every add-on', () => {
  const list = toolAddons(apps)
  assert.deepEqual(enabledAddonIds({ addonTools: true }, list), ['notes', 'indexer'])
  assert.deepEqual(enabledAddonIds({ addonTools: false }, list), [])
  assert.deepEqual(enabledAddonIds({}, list), [])
  assert.deepEqual(enabledAddonIds({ addonToolIds: ['indexer', 'gone'] }, list), ['indexer'])
})

test('chat add-ons: toggling keeps the list and the boolean in step', () => {
  const list = toolAddons(apps)
  let chat = {}
  chat = { ...chat, ...toggleAddonSettings(chat, list, 'notes') }
  assert.deepEqual(chat, { addonToolIds: ['notes'], addonTools: true })
  chat = { ...chat, ...toggleAddonSettings(chat, list, 'notes') }
  assert.deepEqual(chat, { addonToolIds: [], addonTools: false })
  // Turning one off in a chat that only has the older boolean keeps the others.
  assert.deepEqual(toggleAddonSettings({ addonTools: true }, list, 'notes'), { addonToolIds: ['indexer'], addonTools: true })
})

test('chat add-ons: the master switch and /addons turn all on when none is, otherwise all off', () => {
  const list = toolAddons(apps)
  assert.deepEqual(toggleAllAddonSettings({}, list), { addonToolIds: ['notes', 'indexer'], addonTools: true })
  assert.deepEqual(toggleAllAddonSettings({ addonToolIds: ['notes'] }, list), { addonToolIds: [], addonTools: false })
  assert.deepEqual(toggleAllAddonSettings({ addonTools: true }, list), { addonToolIds: [], addonTools: false })
})

test('chat add-ons: the request carries the boolean for all, the list for some, nothing for none', () => {
  const list = toolAddons(apps)
  assert.deepEqual(addonRequestMetadata({}, list), {})
  assert.deepEqual(addonRequestMetadata({ addonToolIds: [] }, list), {})
  assert.deepEqual(addonRequestMetadata({ addonToolIds: ['notes', 'indexer'] }, list), { addon_tools: 'true' })
  assert.deepEqual(addonRequestMetadata({ addonTools: true }, list), { addon_tools: 'true' })
  assert.deepEqual(addonRequestMetadata({ addonToolIds: ['indexer'] }, list), { addon_tools: 'true', addon_tool_ids: 'indexer' })
  // An add-on that stopped since the choice is dropped from the list.
  assert.deepEqual(addonRequestMetadata({ addonToolIds: ['notes', 'idle'] }, list), { addon_tools: 'true', addon_tool_ids: 'notes' })
  // No usable add-on at all: nothing is sent.
  assert.deepEqual(addonRequestMetadata({ addonTools: true }, []), {})
})
