import test from 'node:test'
import assert from 'node:assert/strict'
import { itemLink, itemLinkTarget, widgetData } from '../../src/utils/wellKnown.js'

test('widget items: a url is an http(s) address or a path of the app, nothing else', () => {
  assert.deepEqual(itemLink('https://news.example.com/a?b=1'), { external: true, href: 'https://news.example.com/a?b=1', host: 'news.example.com' })
  assert.deepEqual(itemLink('/app/pages/news'), { external: false, to: '/app/pages/news' })
  assert.deepEqual(itemLink('/app/chats'), { external: false, to: '/app/chats' })
  for (const bad of ['javascript:alert(1)', 'data:text/html,x', '//example.com', 'https://u:p@example.com', 'https://example.com\\@evil.test',
    '/app/users', '/app/pagesx', '/app/pages/../users', '/app/pages//x', '/api/x', 'pages/news', 'https://a b', '', null, 3]) {
    assert.equal(itemLink(bad), null, String(bad))
  }
})

test('widget items: the data carries the checked link of each item', () => {
  const d = widgetData({ exists: true, valid: true, data: { items: [{ label: 'a', url: 'https://example.com' }, { label: 'b', url: 'javascript:x' }, { label: 'c' }] } })
  assert.equal(d.items[0].link.href, 'https://example.com/')
  assert.equal(d.items[1].link, null)
  assert.equal(d.items[2].link, null)
})

test('widget items: an external link goes to the confirmation bar of page links, an in-app link does not', () => {
  const t = itemLinkTarget(itemLink('https://news.example.com/a'))
  assert.equal(t.href, 'https://news.example.com/a')
  assert.equal(t.site, 'example.com')
  assert.equal(t.lookalike, false)
  const fake = itemLinkTarget(itemLink('https://xn--pypal-4ve.com/login'))
  assert.equal(fake.lookalike, true, 'a punycode host is flagged as it is for a page link')
  assert.equal(itemLinkTarget(itemLink('/app/pages')), null)
  assert.equal(itemLinkTarget(null), null)
})
