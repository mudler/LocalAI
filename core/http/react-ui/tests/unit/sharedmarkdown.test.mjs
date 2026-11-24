import test from 'node:test'
import assert from 'node:assert/strict'
import { JSDOM } from 'jsdom'
import createDOMPurify from 'dompurify'
import { renderSharedMarkdown } from '../../src/utils/sharedMarkdown.js'

const purify = createDOMPurify(new JSDOM('').window)
const render = (md) => renderSharedMarkdown(md, purify)
const dom = (html) => new JSDOM(`<body>${html}</body>`).window.document

// Nothing in the output may make the browser fetch an address.
function fetchers(html) {
  const doc = dom(html)
  const bad = []
  for (const el of doc.body.querySelectorAll('*')) {
    for (const a of ['src', 'srcset', 'poster', 'data', 'action', 'formaction', 'background', 'style', 'xlink:href']) if (el.hasAttribute(a)) bad.push(`${el.tagName}[${a}]`)
    if (/^(img|picture|source|video|audio|image|svg|iframe|object|embed|link|style|form|script|input|button)$/i.test(el.tagName)) bad.push(el.tagName)
    for (const a of el.getAttributeNames()) if (/^on/i.test(a)) bad.push(`${el.tagName}[${a}]`)
    if (el.hasAttribute('href') && el.tagName !== 'A') bad.push(`${el.tagName}[href]`)
  }
  return bad
}

const HOSTILE = {
  img: '<img src="https://evil.example/x.png">',
  markdownImage: '![t](https://evil.example/x.png)',
  picture: '<picture><source srcset="https://evil.example/s.png"><img src="https://evil.example/i.png"></picture>',
  video: '<video poster="https://evil.example/p.png" src="https://evil.example/v.mp4"></video>',
  audio: '<audio src="https://evil.example/a.mp3"></audio>',
  svg: '<svg><image href="https://evil.example/s.png"/></svg>',
  svgUse: '<svg><use xlink:href="https://evil.example/s.svg#a"/></svg>',
  iframe: '<iframe src="https://evil.example/"></iframe>',
  object: '<object data="https://evil.example/o"></object>',
  embed: '<embed src="https://evil.example/e">',
  link: '<link rel="stylesheet" href="https://evil.example/c.css">',
  style: '<style>body{background:url(https://evil.example/b.png)}</style>',
  inlineStyle: '<p style="background:url(https://evil.example/b.png)">x</p>',
  form: '<form action="https://evil.example/"><input name="a"><button>go</button></form>',
  onerror: '<p onclick="alert(1)">x</p><img src=x onerror="alert(1)">',
  script: '<script>alert(1)</script>',
}

for (const [name, md] of Object.entries(HOSTILE)) {
  test(`shared markdown: ${name} fetches nothing`, () => {
    const html = render(`Visible text.\n\n${md}\n`)
    assert.deepEqual(fetchers(html), [], html)
    assert.ok(!/evil\.example/.test(html.replace(/<a [^>]*>/g, '')), html)
    assert.match(html, /Visible text\./)
  })
}

test('shared markdown: ordinary formatting stays', () => {
  const html = render('# Title\n\nVoice: **plain**, `code`.\n\n- one\n- two\n\n| a | b |\n|---|---|\n| 1 | 2 |\n')
  for (const tag of ['h1', 'strong', 'code', 'ul', 'li', 'table']) assert.ok(dom(html).querySelector(tag), tag)
})

test('shared markdown: an https link opens safely in a new tab', () => {
  const a = dom(render('[docs](https://example.com/page)')).querySelector('a')
  assert.equal(a.getAttribute('href'), 'https://example.com/page')
  assert.equal(a.getAttribute('target'), '_blank')
  assert.equal(a.getAttribute('rel'), 'noopener noreferrer nofollow')
})

test('shared markdown: javascript:, protocol-relative and data: hrefs stay as text only', () => {
  for (const href of ['javascript:alert(1)', '//evil.example/x', 'data:text/html,hi', 'JaVaScRiPt:alert(1)', 'vbscript:x']) {
    const html = render(`<a href="${href}">click me</a>`)
    const a = dom(html).querySelector('a')
    assert.ok(!a || !a.hasAttribute('href'), `${href} -> ${html}`)
    assert.match(html, /click me/)
  }
})

test('shared markdown: empty gives empty', () => {
  assert.equal(render(''), '')
  assert.equal(render(null), '')
})
