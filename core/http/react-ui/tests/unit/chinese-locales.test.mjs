// SPDX-License-Identifier: MIT
import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { createInstance } from 'i18next'

const root = new URL('../../../../../', import.meta.url)
const locales = new URL('core/http/react-ui/public/locales/', root)
const read = (url) => JSON.parse(readFileSync(url, 'utf8'))
const placeholders = (value) => [...value.matchAll(/{{.*?}}/g)].map(m => m[0]).sort()

function compare(en, zh, path) {
  assert.equal(typeof zh, typeof en, path)
  if (typeof en === 'string') {
    assert.ok(zh.trim(), `${path} is empty`)
    assert.deepEqual(placeholders(zh), placeholders(en), `${path} interpolation`)
    assert.deepEqual([...zh.matchAll(/<\/?[a-zA-Z][^>]*>/g)].map(m => m[0]).sort(),
      [...en.matchAll(/<\/?[a-zA-Z][^>]*>/g)].map(m => m[0]).sort(), `${path} markup`)
  } else if (en && typeof en === 'object') {
    assert.ok(zh, path)
    assert.equal(Array.isArray(zh), Array.isArray(en), path)
    if (Array.isArray(en)) assert.equal(zh.length, en.length, path)
    for (const [key, value] of Object.entries(en)) compare(value, zh[key], `${path}.${key}`)
  }
}

test('Chinese locales cover the host English keys, examples and interpolation', () => {
  for (const file of readdirSync(new URL('en/', locales)).filter(f => f.endsWith('.json'))) {
    compare(read(new URL(`en/${file}`, locales)), read(new URL(`zh-CN/${file}`, locales)), file)
  }
})

test('Chinese plurals and namespaced labels resolve without English fallback', async () => {
  const resources = { en: {}, 'zh-CN': {} }
  for (const file of readdirSync(new URL('en/', locales)).filter(f => f.endsWith('.json'))) {
    for (const language of Object.keys(resources)) resources[language][file.slice(0, -5)] = read(new URL(`${language}/${file}`, locales))
  }
  const i18n = createInstance()
  await i18n.init({ lng: 'zh-CN', fallbackLng: 'en', resources, interpolation: { escapeValue: false } })
  for (const count of [0, 1, 2, 50]) {
    assert.equal(i18n.t('home:statusLine.modelsLoaded', { count }), `已加载 ${count} 个模型`)
    assert.equal(i18n.t('swarm:models.replicaCount', { count }), `${count} 个副本`)
    assert.equal(i18n.t('operate:running.operations', { count }), `${count} 个操作进行中`)
  }
  assert.equal(i18n.t('nav:operate.observability'), '可观测性')
  assert.equal(i18n.t('modelEditor:title.edit'), '模型编辑器')
  assert.equal(i18n.t('traffic:overview.title', { window: '最近 7 天' }), '流量，最近 7 天')
})

test('Chinese built-in add-on bundles cover strings, lists and placeholders', async () => {
  for (const name of ['voice', 'face', 'fine-tuning', 'quantization']) {
    const base = new URL(`addons/${name}/ui/src/i18n/`, root)
    const en = (await import(new URL('en.js', base))).default
    const zh = (await import(new URL('zh-CN.js', base))).default
    compare(en, zh, name)
  }
  for (const [name, ns] of [['studio', 'media'], ['talk', 'talk']]) {
    const base = new URL(`addons/${name}/ui/src/i18n/`, root)
    compare(read(new URL(`${ns}.en.json`, base)), read(new URL(`${ns}.zh-CN.json`, base)), name)
  }
})
