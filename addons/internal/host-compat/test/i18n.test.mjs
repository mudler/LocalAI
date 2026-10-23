import test from 'node:test'
import assert from 'node:assert/strict'
import { clearLocales, registerLocales, translator } from '../src/i18n.js'

registerLocales({
  media: {
    en: {
      studio: { tabs: { images: 'Images' }, work: { results_one: '{{count}} result', results_other: '{{count}} results', starters: ['a', 'b'] } },
      'flat.key': 'Flat',
      hello: 'Hello {{name}}, {{ name }}!',
    },
    it: { studio: { tabs: { images: 'Immagini' } } },
  },
  talk: { en: { title: 'Talk', picker: { none: 'None' } } },
})

test('a nested key resolves, and a flat key with dots resolves', () => {
  const t = translator('en', ['media'])
  assert.equal(t('studio.tabs.images'), 'Images')
  assert.equal(t('flat.key'), 'Flat')
})

test('the language wins, then its base language, then English', () => {
  assert.equal(translator('it', ['media'])('studio.tabs.images'), 'Immagini')
  assert.equal(translator('it-CH', ['media'])('studio.tabs.images'), 'Immagini')
  assert.equal(translator('de', ['media'])('studio.tabs.images'), 'Images')
  assert.equal(translator('it', ['media'])('studio.work.results_other', { count: 2 }), '2 results', 'a missing Italian key falls back to English')
})

test('a missing key gives the default value from either argument, else the key', () => {
  const t = translator('en', ['media'])
  assert.equal(t('nope.key'), 'nope.key')
  assert.equal(t('nope.key', 'Fallback {{x}}', { x: 1 }), 'Fallback 1')
  assert.equal(t('nope.key', { defaultValue: 'Opt' }), 'Opt')
})

test('plural keys follow the count', () => {
  const t = translator('en', ['media'])
  assert.equal(t('studio.work.results', { count: 1 }), '1 result')
  assert.equal(t('studio.work.results', { count: 5 }), '5 results')
  assert.equal(t('studio.work.results', { count: 0 }), '0 results')
})

test('returnObjects gives the list, and a list is never shown as text', () => {
  const t = translator('en', ['media'])
  assert.deepEqual(t('studio.work.starters', { returnObjects: true }), ['a', 'b'])
  assert.equal(t('studio.work', 'fallback'), 'fallback')
})

test('placeholders are filled, whitespace and repeats included, and unknown ones stay', () => {
  const t = translator('en', ['media'])
  assert.equal(t('hello', { name: 'Ada' }), 'Hello Ada, Ada!')
  assert.equal(t('hello'), 'Hello {{name}}, {{ name }}!')
})

test('namespaces: the first that has the key wins, and ns:key names one', () => {
  const t = translator('en', ['media', 'talk'])
  assert.equal(t('title'), 'Talk')
  assert.equal(t('talk:picker.none'), 'None')
  assert.equal(translator('en', 'talk')('picker.none'), 'None')
})

test('clearLocales empties the registry', () => {
  clearLocales()
  assert.equal(translator('en', ['media'])('studio.tabs.images'), 'studio.tabs.images')
})
