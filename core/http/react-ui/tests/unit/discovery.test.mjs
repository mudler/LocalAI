import test from 'node:test'
import assert from 'node:assert/strict'
import {
  seenOf, withSeen, firstName, exampleResults, experienced, examplesHidden, hideExamples, showExamples,
  forgetHints, dismissedCount, suggestionsOn, newLabel, markNewSeen, markNewOpened, exampleCards,
} from '../../src/utils/discovery.js'

const doc = (data, extra = {}) => ({ exists: true, valid: true, data, ...extra })
const none = { exists: false, valid: true, data: {} }

test('discovery: seen is read from user.json and is empty without it', () => {
  assert.deepEqual(seenOf(null), {})
  assert.deepEqual(seenOf(none), {})
  assert.deepEqual(seenOf(doc({ seen: { offers_off: true } })), { offers_off: true })
  assert.deepEqual(seenOf(doc({ seen: 'bad' })), {})
})

test('discovery: withSeen merges objects and removes a key set to null', () => {
  const out = withSeen({ offers: { profile: { state: 'declined' } }, last_offer: 'x' }, { offers: { shortcut: { state: 'later' } }, last_offer: null })
  assert.deepEqual(out, { offers: { profile: { state: 'declined' }, shortcut: { state: 'later' } } })
})

test('discovery: firstName takes the first word, or nothing', () => {
  assert.equal(firstName('  Marta Rossi '), 'Marta')
  assert.equal(firstName(''), '')
  assert.equal(firstName(undefined), '')
})

test('discovery: each example card has its own result', () => {
  const empty = exampleResults({ persona: none, user: none, home: none, pages: [] })
  assert.deepEqual(empty, { profile: false, page: false, schedule: false })
  // A name the server prefilled from the account is not a saved profile.
  const prefilled = exampleResults({ persona: none, user: { exists: false, data: { name: 'Marta' }, prefilled: { name: 'account' } }, home: none, pages: [] })
  assert.equal(prefilled.profile, false)
  // user.json that holds only the record of hints is not a saved profile either.
  assert.equal(exampleResults({ persona: none, user: doc({ seen: { offers_off: true } }), home: none, pages: [] }).profile, false)
  assert.equal(exampleResults({ persona: doc({ system_prompt: 'Short.' }), user: none, home: none, pages: [] }).profile, true)
  assert.equal(exampleResults({ persona: none, user: doc({ bio: 'Designer' }), home: none, pages: [] }).profile, true)
  assert.equal(exampleResults({ persona: none, user: none, home: none, pages: [{ name: 'tasks' }] }).page, true)
  // A widget with a data file is the result of the "every morning" card; a chats widget is not.
  assert.equal(exampleResults({ persona: none, user: none, home: doc({ widgets: [{ type: 'chats', title: 'Chats' }] }), pages: [] }).schedule, false)
  assert.equal(exampleResults({ persona: none, user: none, home: doc({ widgets: [{ type: 'list', title: 'Inbox', source: 'data/inbox.json' }] }), pages: [] }).schedule, true)
})

test('discovery: a user who already has several files never sees the examples', () => {
  assert.equal(experienced({ persona: none, user: none, shortcuts: none, home: none, ui: none }), false)
  // The files the examples make do not count: a first-time user who did card 1 and 3 still sees card 2.
  assert.equal(experienced({ persona: doc({ name: 'Ada' }), user: doc({ name: 'M' }), shortcuts: none, home: doc({ widgets: [] }), ui: none }), false)
  assert.equal(experienced({ persona: doc({ name: 'Ada' }), user: none, shortcuts: doc({ shortcuts: [] }), home: none, ui: none }), true)
  assert.equal(experienced({ persona: none, user: none, shortcuts: none, home: doc({}), ui: doc({ theme: 'dark' }) }), true)
  assert.equal(experienced({ persona: none, user: none, shortcuts: doc({ shortcuts: [] }), home: none, ui: none }), false)
})

test('discovery: hide and show the examples', () => {
  assert.equal(examplesHidden({}), false)
  const hidden = hideExamples({ offers_off: true }, '2026-10-30')
  assert.deepEqual(hidden, { offers_off: true, home_examples: { hidden: '2026-10-30' } })
  assert.equal(examplesHidden(hidden), true)
  assert.deepEqual(showExamples(hidden), { offers_off: true })
})

test('discovery: forgetting the hints keeps the switches and the examples', () => {
  const seen = { offers_off: false, new_off: true, home_examples: { hidden: 'd' }, last_offer: 't', offer_chats: ['a'], offers: { profile: { state: 'declined' }, shortcut: { state: 'later', count: 1 }, schedule: { state: 'accepted' } } }
  assert.equal(dismissedCount(seen), 2)
  assert.equal(dismissedCount({ profile: { state: 'declined' } }), 1, 'the older flat record counts too')
  assert.deepEqual(forgetHints(seen), { offers_off: false, new_off: true, home_examples: { hidden: 'd' } })
  assert.deepEqual(forgetHints({ profile: { state: 'declined' }, since: 's' }), { since: 's' })
})

test('discovery: suggestions follow the user, else the server default', () => {
  assert.equal(suggestionsOn({}, true), true)
  assert.equal(suggestionsOn({}, false), false)
  assert.equal(suggestionsOn({ offers_off: true }, true), false)
  assert.equal(suggestionsOn({ offers_off: false }, false), true)
})

test('discovery: the New label shows once, until opened or for 7 days', () => {
  const now = Date.parse('2026-10-30T10:00:00Z')
  assert.equal(newLabel({}, 'pages', now), 'first')
  assert.equal(newLabel({ new: { pages: '2026-10-28' } }, 'pages', now), 'show')
  assert.equal(newLabel({ new: { pages: '2026-10-20' } }, 'pages', now), 'hide')
  assert.equal(newLabel({ new: { pages: 'opened' } }, 'pages', now), 'hide')
  assert.equal(newLabel({ new_off: true }, 'pages', now), 'hide')
  assert.deepEqual(markNewSeen({}, 'pages', '2026-10-30'), { new: { pages: '2026-10-30' } })
  assert.deepEqual(markNewOpened({ new: { pages: '2026-10-30' } }, 'pages'), { new: { pages: 'opened' } })
})

test('discovery: the cards use the first name and the mail fallback', () => {
  const t = (k, o) => (o ? `${k}:${JSON.stringify(o)}` : k)
  const cards = exampleCards({ t, name: 'Marta Rossi', mail: false })
  assert.equal(cards.length, 3)
  assert.equal(cards[0].prompt, 'discovery.ex.card1.prompt:{"firstName":"Marta"}')
  assert.equal(cards[2].prompt, 'discovery.ex.card3.promptNoMail')
  assert.equal(exampleCards({ t, name: '', mail: true })[0].prompt, 'discovery.ex.card1.promptNoName')
  assert.equal(exampleCards({ t, name: 'M', mail: true })[2].prompt, 'discovery.ex.card3.prompt')
  assert.deepEqual(cards.map(c => c.id), ['profile', 'page', 'schedule'])
})
