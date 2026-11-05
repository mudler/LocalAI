import test from 'node:test'
import assert from 'node:assert/strict'
import { allowIssue, argsOf, classOf, confirmDomains, draftIssue, draftOf, hardIssue, isPhysical, patternIssue, pinIssue, same, splitLines } from '../src/policy.js'

const HARD = { domains: ['lock', 'alarm_control_panel', 'siren', 'valve', 'water_heater', 'camera', 'hassio', 'homeassistant', 'shell_command', 'python_script'], serviceNames: ['restart', 'stop'], servicePrefixes: ['reload'] }
const POLICY = { allow: ['light.turn_on', 'climate.set_temperature'], excludeDomains: ['lock', 'camera'], excludeAreas: [], excludePatterns: [], excludeLabel: 'sensitive', maxTargets: 5, pins: [] }

test('home-assistant policy: lines are split on newlines and commas, without repeats', () => {
  assert.deepEqual(splitLines('a.b\n a.b, c.d\r\n\n'), ['a.b', 'c.d'])
  assert.deepEqual(splitLines(''), [])
})

test('home-assistant policy: lights, switches, input booleans and media transport are quiet, the rest asks every time', () => {
  for (const e of ['light.turn_on', 'switch.toggle', 'input_boolean.*', 'media_player.media_pause']) assert.equal(classOf(e), 'quiet', e)
  for (const e of ['climate.set_temperature', 'cover.open_cover', 'script.turn_on', 'scene.turn_on', 'notify.*', 'media_player.play_media', 'fan.turn_on']) assert.equal(classOf(e), 'confirm', e)
  assert.equal(classOf('light'), '')
  assert.equal(classOf('Light.turn_on'), '')
  assert.equal(classOf('a.b.c'), '')
  assert.equal(isPhysical('media_player', 'select_source'), true)
})

test('home-assistant policy: a hard-excluded entry is named before the page saves', () => {
  assert.equal(hardIssue('lock.unlock', HARD), 'hard')
  assert.equal(hardIssue('lock.*', HARD), 'hard')
  assert.equal(hardIssue('homeassistant.restart', HARD), 'hard')
  assert.equal(hardIssue('light.reload', HARD), 'hard')
  assert.equal(hardIssue('light.restart', HARD), 'hard')
  assert.equal(hardIssue('light.turn_on', HARD), '')
  assert.deepEqual(allowIssue('light.turn_on\nlock.unlock', HARD), { key: 'allowHard', entry: 'lock.unlock' })
  assert.deepEqual(allowIssue('light.turn_on\nnot an entry', HARD), { key: 'allowShape', entry: 'not an entry' })
  assert.equal(allowIssue('light.turn_on\nclimate.*', HARD), null)
})

test('home-assistant policy: patterns, pins and the target cap are checked', () => {
  assert.equal(patternIssue('sensor\\..*_bedroom\nlight\\.guest'), null)
  assert.deepEqual(patternIssue('(('), { key: 'patternShape', entry: '((' })
  const fp = 'ab'.repeat(32)
  assert.equal(pinIssue(`ha.example:8123=${fp}`), null)
  assert.equal(pinIssue(`ha.example:8123=${fp.match(/../g).join(':')}`), null)
  assert.equal(pinIssue('ha.example=abc').key, 'pinShape')
  assert.equal(pinIssue(`nocolon=${fp}`).key, 'pinShape')
  const d = draftOf(POLICY)
  assert.equal(draftIssue(d, HARD), null)
  assert.equal(draftIssue({ ...d, maxTargets: '0' }, HARD).field, 'maxTargets')
  assert.equal(draftIssue({ ...d, maxTargets: '21' }, HARD).field, 'maxTargets')
  assert.equal(draftIssue({ ...d, maxTargets: '2.5' }, HARD).field, 'maxTargets')
  assert.equal(draftIssue({ ...d, allow: 'lock.unlock' }, HARD).field, 'allow')
})

test('home-assistant policy: the draft round-trips to the arguments of set-policy', () => {
  const d = draftOf(POLICY)
  assert.equal(d.maxTargets, '5')
  assert.deepEqual(argsOf(d), { allow: ['light.turn_on', 'climate.set_temperature'], excludeDomains: ['lock', 'camera'], excludeAreas: [], excludePatterns: [], excludeLabel: 'sensitive', maxTargets: 5, pins: [] })
  assert.equal(same(d, draftOf(POLICY)), true)
  assert.equal(same({ ...d, maxTargets: '6' }, d), false)
  assert.deepEqual(argsOf({ ...d, excludeLabel: '  ' }).excludeLabel, '')
})

test('home-assistant policy: the summary names the domains that ask every time', () => {
  assert.deepEqual(confirmDomains('light.turn_on\nclimate.set_temperature\nscript.turn_on\ncover.*'), ['climate', 'cover', 'script'])
  assert.deepEqual(confirmDomains('light.turn_on'), [])
})
