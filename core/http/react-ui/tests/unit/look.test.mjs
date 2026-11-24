import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  LOOK_OPTIONS, LOOK_DEFAULTS, lookOf, lookAttributes, contrastRatio, accentCheck, onAccentRole, themePalette, MIN_CONTRAST,
  templateProblem, fillTemplate, STRING_KEYS, uiStrings, identityOf, avatarPath, IDENTITY_PLACEHOLDERS,
} from '../../src/utils/look.js'

const here = dirname(fileURLToPath(import.meta.url))
const schemas = join(here, '../../../../services/studio/builtinskills/tree/localai-workspace/schemas')
const readJSON = (p) => JSON.parse(readFileSync(p, 'utf8'))
const uiSchema = readJSON(join(schemas, 'ui.schema.json'))
const personaSchema = readJSON(join(schemas, 'persona.schema.json'))
const homeSchema = readJSON(join(schemas, 'home.schema.json'))
const theme = readJSON(join(here, '../../src/theme/theme-localai.json'))

test('look: the lists of the editor are the lists of the schema, and each default is in its list', () => {
  const props = uiSchema.properties.look.properties
  assert.deepEqual(Object.keys(LOOK_OPTIONS).sort(), Object.keys(props).sort())
  for (const [k, list] of Object.entries(LOOK_OPTIONS)) {
    assert.deepEqual([...list].sort(), [...props[k].enum].sort(), k)
    assert.ok(list.includes(LOOK_DEFAULTS[k]), k)
  }
})

test('look: lookOf keeps only listed values and lookAttributes leaves the defaults off the page', () => {
  assert.deepEqual(lookOf({ look: { font: 'serif', radius: '12px', bubble: 'tinted', codeTheme: 'solarized', x: 1 } }), { font: 'serif', bubble: 'tinted' })
  assert.deepEqual(lookOf({ look: 'serif' }), {})
  assert.deepEqual(lookOf(null), {})
  assert.deepEqual(lookAttributes({ font: 'serif', radius: 'soft', messageWidth: 'wide' }), {
    'data-look-font': 'serif', 'data-look-radius': null, 'data-look-bubble': null, 'data-look-messagewidth': 'wide', 'data-look-codetheme': null,
  })
  assert.equal(lookAttributes({ font: 'url(x)' })['data-look-font'], null, 'a value outside the list never reaches the page')
})

test('look: contrast follows WCAG and the accent check uses the page and the card of the theme', () => {
  assert.equal(Math.round(contrastRatio('#000000', '#ffffff') * 10) / 10, 21)
  assert.equal(contrastRatio('#ffffff', '#ffffff'), 1)
  assert.equal(contrastRatio('red', '#ffffff'), 0)
  const light = themePalette(theme, 'light')
  const dark = themePalette(theme, 'dark')
  assert.equal(light.canvas, '#fff4dc')
  // The default accents of the theme pass in their own mode.
  assert.ok(accentCheck(light.accent, light).ok)
  assert.ok(accentCheck(dark.accent, dark).ok)
  // A dark teal passes on the light page and fails on the dark one.
  const teal = '#0f766e'
  assert.equal(accentCheck(teal, light).ok, true)
  assert.equal(accentCheck(teal, dark).ok, false)
  assert.ok(accentCheck(teal, dark).ratio < MIN_CONTRAST)
  // A pale yellow fails on the light page.
  assert.equal(accentCheck('#ffe08a', light).ok, false)
  assert.equal(accentCheck('#zzzzzz', light).ok, false)
  // The badge rounds down, so it never claims a pass the colour does not give.
  const r = accentCheck(teal, light).ratio
  assert.ok(r <= contrastRatio(teal, light.canvas) && r <= contrastRatio(teal, light.card))
})

test('look: text on a filled accent takes the token that reads better', () => {
  const light = themePalette(theme, 'light')
  const dark = themePalette(theme, 'dark')
  assert.equal(onAccentRole('#0f766e', light), '--dk-canvas', 'cream on a dark teal')
  assert.equal(onAccentRole('#7fd3c8', dark), '--dk-canvas', 'the dark page colour on a light teal')
  assert.equal(onAccentRole('#7fd3c8', light), '--dk-text', 'ink on a light teal')
})

test('placeholders: an unknown name or a lone brace is a problem; the allowed ones are not', () => {
  assert.equal(templateProblem('Hi {user.name}, {time_of_day}', IDENTITY_PLACEHOLDERS), null)
  assert.equal(templateProblem('', IDENTITY_PLACEHOLDERS), null)
  assert.deepEqual(templateProblem('Hi {user.nmae}', IDENTITY_PLACEHOLDERS), { unknown: ['{user.nmae}'], brace: false })
  assert.deepEqual(templateProblem('Ask {model', ['{model}']), { unknown: [], brace: true })
  assert.deepEqual(templateProblem('Ask {{model}}', ['{model}']), { unknown: [], brace: true })
  assert.deepEqual(templateProblem('New {model}', []), { unknown: ['{model}'], brace: false })
})

test('placeholders: fillTemplate fills and tidies what an empty value leaves', () => {
  assert.equal(fillTemplate('{time_of_day}, {user.name}', { time_of_day: 'Good morning', 'user.name': 'Marta' }), 'Good morning, Marta')
  assert.equal(fillTemplate('{time_of_day}, {user.name}', { time_of_day: 'Good morning' }), 'Good morning')
  assert.equal(fillTemplate('Hi {user.name}, what are we working on?', {}), 'Hi, what are we working on?')
  assert.equal(fillTemplate('{user.name}, ready?', {}), 'ready?')
  assert.equal(fillTemplate(5), '')
})

test('strings: the allow-list matches the schema, with the same placeholders, and security keys are not on it', () => {
  const props = uiSchema.properties.strings.properties
  assert.equal(uiSchema.properties.strings.additionalProperties, false)
  assert.deepEqual(Object.keys(STRING_KEYS).sort(), Object.keys(props).sort())
  for (const [k, ph] of Object.entries(STRING_KEYS)) assert.deepEqual([...ph].sort(), [...props[k]['x-placeholders']].sort(), k)
  for (const k of Object.keys(STRING_KEYS)) assert.ok(!/permission|consent|security|trust|approve|allow/i.test(k), k)
  assert.deepEqual(personaSchema.properties.greeting['x-placeholders'], IDENTITY_PLACEHOLDERS)
  assert.deepEqual(homeSchema.properties.headline['x-placeholders'], IDENTITY_PLACEHOLDERS)
})

test('strings: uiStrings drops unknown keys, empty text and broken placeholders', () => {
  assert.deepEqual(uiStrings({ strings: {
    'nav.newChat': 'New thread', 'permission.allow': 'Always', 'composer.placeholder': 'Ask {modle}', 'nav.home': '  ', 'home.headline': 'Hi {user.name}',
  } }), { 'nav.newChat': 'New thread', 'home.headline': 'Hi {user.name}' })
  assert.deepEqual(uiStrings({ strings: [] }), {})
})

test('identity: the files win over the text overrides, and a broken text falls back', () => {
  const vars = { 'user.name': 'Marta', time_of_day: 'Good morning', date: 'Tue 10 Nov' }
  const id = identityOf({
    persona: { name: ' Juniper ', avatar: 'Pictures/j.png', greeting: 'Hi {user.name}', emptyChat: { title: 'Ask Juniper anything', subtitle: 'Say what you need.', showShortcuts: false } },
    home: { headline: '{time_of_day}, {user.name}' },
    ui: { strings: { 'home.headline': 'Rise and shine', 'chat.empty.title': 'Ready' } },
  }, vars)
  assert.deepEqual(id, {
    name: 'Juniper', avatar: 'Pictures/j.png', greeting: 'Hi Marta', headline: 'Good morning, Marta',
    emptyTitle: 'Ask Juniper anything', emptySubtitle: 'Say what you need.', showShortcuts: false,
  })
  const fallback = identityOf({ persona: { greeting: 'Hi {usr}', avatar: '../x.png' }, home: { headline: 'Hi {x' }, ui: { strings: { 'home.headline': 'Rise and shine', 'chat.empty.title': 'Ready' } } }, vars)
  assert.equal(fallback.greeting, '')
  assert.equal(fallback.avatar, '')
  assert.equal(fallback.headline, 'Rise and shine')
  assert.equal(fallback.emptyTitle, 'Ready')
  assert.equal(fallback.showShortcuts, true)
  assert.deepEqual(identityOf({}, vars), { name: '', avatar: '', greeting: '', headline: '', emptyTitle: '', emptySubtitle: '', showShortcuts: true })
})

test('identity: an avatar follows the path rule of the backgrounds, with no SVG', () => {
  assert.equal(avatarPath('Pictures/avatars/juniper.png'), 'Pictures/avatars/juniper.png')
  for (const bad of ['../a.png', 'a/../../b.png', '/a.png', 'https://x/a.png', 'data:image/png;base64,AA', 'a.svg', 'a\\b.png', 'a.png?x', '']) {
    assert.equal(avatarPath(bad), '', bad)
  }
})
