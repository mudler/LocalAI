import test from 'node:test'
import assert from 'node:assert/strict'
import { agentPayload, newAgent } from '../../src/utils/chatAgents.js'
import {
  WIZARD_STEPS, wizardForm, wizardPayload, purposeReady, stepReachable, whereItems, needsLine, kindSetupAddress, catalogAddress,
} from '../../src/utils/agentWizard.js'

// What the full editor sends for a new agent: the blank form with a name and
// instructions typed into it.
const blankFormSends = (name, instructions) => agentPayload({ ...newAgent(), name, instructions }, { initialFolders: [] })

test('wizard: it has the four steps, in order', () => {
  assert.deepEqual(WIZARD_STEPS, ['purpose', 'abilities', 'where', 'review'])
})

test('wizard: skipping every optional step sends what the blank form sends', () => {
  const form = { ...wizardForm(''), name: 'helper', instructions: 'Be brief.' }
  assert.deepEqual(wizardPayload(form), blankFormSends('helper', 'Be brief.'))
  // Spaces around the name are trimmed the same way.
  assert.deepEqual(wizardPayload({ ...form, name: '  helper ' }), blankFormSends('  helper ', 'Be brief.'))
})

test('wizard: the starting body restricts nothing', () => {
  const body = wizardPayload({ ...wizardForm(''), name: 'a', instructions: 'b' })
  assert.deepEqual(body.builtins_off, [])
  assert.deepEqual(body.builtins_on, [])
  assert.equal(body.model, '')
  assert.equal(body.network, 'off')
  assert.equal('folders' in body, false)
})

test('wizard: a template fills the text and leaves the tools as they are', () => {
  const form = wizardForm('researcher')
  assert.equal(form.name, 'research-assistant')
  const blank = newAgent()
  for (const k of ['builtins', 'builtins_on', 'builtins_off', 'folders', 'addons']) assert.deepEqual(form[k], blank[k])
  assert.deepEqual(wizardForm('nope'), newAgent())
})

test('wizard: only a name and instructions are required, and they open the later steps', () => {
  assert.equal(purposeReady({ name: 'a', instructions: '' }), false)
  assert.equal(purposeReady({ name: ' ', instructions: 'x' }), false)
  assert.equal(purposeReady({ name: 'a', instructions: 'x' }), true)
  assert.equal(stepReachable('purpose', { name: '', instructions: '' }), true)
  assert.equal(stepReachable('review', { name: '', instructions: '' }), false)
  assert.equal(stepReachable('review', { name: 'a', instructions: 'x' }), true)
})

const kinds = [
  { addon: 'chatty', kind: 'chatty', title: 'Chatty', description: 'Talk to an agent in Chatty.', page: 'bots', setup: 'A bot token.' },
  { addon: 'mailer', kind: 'mail', title: 'Mailer', description: 'Read mail.', agent_optional: true },
]
const toolAddons = [
  { id: 'notes', title: 'Notes', description: 'Keep notes.' },
  { id: 'gone', title: 'Gone', gone: true },
  { id: 'mailer', title: 'Mailer tools', credentialed: true },
]
const catalog = [{ id: 'newchat', title: 'New chat', description: 'A chat app.' }, { id: 'notes', title: 'Notes' }]

test('where: the groups are built from the kinds, the tool add-ons and the catalog', () => {
  const g = whereItems({ kinds, toolAddons, catalog })
  assert.deepEqual(g.chat.map((x) => x.key), ['kind:chatty/chatty'])
  // Credentials-only kinds go under tools; an add-on is not listed twice; a gone add-on is not listed.
  assert.deepEqual(g.tools.map((x) => x.key), ['kind:mailer/mail', 'tool:notes', 'catalog:newchat'])
  assert.equal(g.tools.find((x) => x.key === 'catalog:newchat').installed, false)
  assert.equal(g.chat[0].setup, 'A bot token.')
})

test('where: a new kind appears with no change in the code', () => {
  const extra = { addon: 'wave', kind: 'wave', title: 'Wave', description: 'Talk in Wave.', page: 'home' }
  const g = whereItems({ kinds: [...kinds, extra], toolAddons, catalog })
  assert.ok(g.chat.some((x) => x.key === 'kind:wave/wave' && x.title === 'Wave'))
})

test('where: the words come from the add-on, with a generic line when it has no setup text', () => {
  const generic = (title) => `Finish setting up ${title}`
  const g = whereItems({ kinds, toolAddons, catalog })
  assert.equal(needsLine(g.chat[0], generic), 'A bot token.')
  assert.equal(needsLine(g.tools.find((x) => x.key === 'tool:notes'), generic), 'Finish setting up Notes')
  const loc = whereItems({ kinds: [{ ...kinds[0], localized_title: 'Chiacchiere', localized_description: 'Parla.' }] })
  assert.equal(loc.chat[0].title, 'Chiacchiere')
  assert.equal(loc.chat[0].description, 'Parla.')
})

test('where: the addresses carry the agent and the catalog entry', () => {
  assert.equal(kindSetupAddress({ addon: 'chatty', page: 'bots' }, 'a 1'), '/app/addons/chatty/bots?new=1&agent=a+1')
  assert.equal(kindSetupAddress({ addon: 'chatty', page: '' }, 'a1'), '/app/addons?detail=chatty')
  assert.equal(catalogAddress('newchat'), '/app/addons?tab=browse&q=newchat')
})
