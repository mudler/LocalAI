import test from 'node:test'
import assert from 'node:assert/strict'
import { chatFileTarget, findChatFile, embedsImage, undrawnImages, spaceSafeLinks } from '../../src/utils/chatMedia.js'

const F = (name, dir = '/Chats/budget') => ({ name, path: `${dir}/${name}`, guest_path: `/workspace/home${dir}/${name}` })
const files = [F('chart.png'), F('plot.svg'), F('page.html')]

test('chat media: a path of the model, of the person or a bare name finds the file of the chat', () => {
  assert.deepEqual(chatFileTarget('/workspace/home/Chats/budget/chart.png', files), { path: '/Chats/budget/chart.png', name: 'chart.png', known: true })
  assert.deepEqual(chatFileTarget('/Chats/budget/plot.svg', files), { path: '/Chats/budget/plot.svg', name: 'plot.svg', known: true })
  assert.equal(chatFileTarget('chart.png', files).path, '/Chats/budget/chart.png')
  assert.equal(chatFileTarget('./chart.png?x=1#top', files).path, '/Chats/budget/chart.png')
  assert.equal(chatFileTarget('sandbox:/workspace/home/Chats/budget/chart.png', files).path, '/Chats/budget/chart.png')
  assert.equal(chatFileTarget('file:///workspace/home/Chats/budget/page.html', files).path, '/Chats/budget/page.html')
  assert.equal(chatFileTarget('My%20chart.png', [F('My chart.png')]).name, 'My chart.png')
})

test('chat media: a file the list does not hold yet is read by its path under the person\'s files', () => {
  assert.deepEqual(chatFileTarget('/workspace/home/out/new.png', []), { path: '/out/new.png', name: 'new.png', known: false })
  assert.equal(chatFileTarget('/etc/passwd', []), null)
  assert.equal(chatFileTarget('missing.png', files), null)
})

test('chat media: an address, a data URI and an anchor are not files of the chat', () => {
  for (const ref of ['https://example.com/a.png', 'http://x/chart.png', 'data:image/png;base64,AAAA', 'blob:abc', '//cdn/x.png', '#top', '', null]) {
    assert.equal(chatFileTarget(ref, files), null, String(ref))
  }
  assert.equal(findChatFile(files, ''), null)
})

test('chat media: an image the answer does not draw is listed for a thumbnail', () => {
  assert.equal(embedsImage('Here: ![sales](/workspace/home/Chats/budget/chart.png)', 'chart.png'), true)
  assert.equal(embedsImage('I saved chart.png for you.', 'chart.png'), false)
  const list = [{ name: 'chart.png', path: '/a/chart.png' }, { name: 'plot.svg', path: '/a/plot.svg' }, { name: 'page.html', path: '/a/page.html' }]
  assert.deepEqual(undrawnImages(list, 'Done.').map(f => f.name), ['chart.png', 'plot.svg'])
  assert.deepEqual(undrawnImages(list, '![c](chart.png)').map(f => f.name), ['plot.svg'])
})

test('chat media: an address with spaces is written so that markdown reads the image and the link', () => {
  const dir = '/workspace/home/Chats/2026-10-30 Quarterly budget'
  assert.equal(spaceSafeLinks(`![Sales](${dir}/chart.svg) and [page](${dir}/page.html)`), '![Sales](/workspace/home/Chats/2026-10-30%20Quarterly%20budget/chart.svg) and [page](/workspace/home/Chats/2026-10-30%20Quarterly%20budget/page.html)')
  // A title, an address without spaces, angle brackets and code stay as written.
  for (const same of ['[a](/x/y.png "A title")', "![a](/x/y.png 'A title')", '![a](/x/y.png)', '![a](</x y/z.png>)', 'plain text (with parens) and [no link]', 'use `![a](/x y/z.png)` as is', '```md\n![a](/x y/z.png)\n```']) {
    assert.equal(spaceSafeLinks(same), same, same)
  }
  assert.equal(spaceSafeLinks('![a](/x y/z.png) and `![b](/u v/w.png)`'), '![a](/x%20y/z.png) and `![b](/u v/w.png)`')
  assert.equal(chatFileTarget('/workspace/home/Chats/2026-10-30%20Quarterly%20budget/chart.png', [{ name: 'chart.png', path: '/Chats/2026-10-30 Quarterly budget/chart.png', guest_path: `${dir}/chart.png` }]).path, '/Chats/2026-10-30 Quarterly budget/chart.png')
  assert.equal(spaceSafeLinks(''), '')
})
