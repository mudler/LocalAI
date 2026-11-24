import test from 'node:test'
import assert from 'node:assert/strict'
import {
  splitAttachmentNote, messageParts, isImageAttachment, attachmentLayout,
} from '../../src/utils/chatAttachments.js'

const PHOTO = '/workspace/home/Chats/2026-10-30 ci sei?/photo_5985526360434217157_y.jpg'

test('chat attachments: an old message shows the words and the photo, not the list', () => {
  const got = splitAttachmentNote(`whats in this photo\n\nThe user attached these files:\n- ${PHOTO}`)
  assert.equal(got.text, 'whats in this photo')
  assert.deepEqual(got.attachments, [{ path: '/Chats/2026-10-30 ci sei?/photo_5985526360434217157_y.jpg', name: 'photo_5985526360434217157_y.jpg' }])
})

test('chat attachments: a message with only files has no words', () => {
  const got = splitAttachmentNote('The user attached these files:\n- /workspace/home/Chats/a/one.png\n- /workspace/home/Chats/a/two.txt')
  assert.equal(got.text, '')
  assert.deepEqual(got.attachments.map(a => a.name), ['one.png', 'two.txt'])
})

test('chat attachments: the text read from a file is not shown', () => {
  const text = 'my day\n\n[Voice note memo.m4a, transcribed: buy milk]\n\n[Text of q1.pdf:\nRevenue\n... 10 of 99 characters shown; read the rest with read_document.]\n\nThe user attached these files:\n- /workspace/home/Chats/a/memo.m4a\n- /workspace/home/Chats/a/q1.pdf'
  const got = splitAttachmentNote(text)
  assert.equal(got.text, 'my day')
  assert.deepEqual(got.attachments.map(a => a.name), ['memo.m4a', 'q1.pdf'])
})

test('chat attachments: waiting messages joined into one keep their words in order', () => {
  const text = 'first\n\nThe user attached these files:\n- /workspace/home/Chats/a/one.txt\n\nsecond\n\n[Document scan.pdf has no text layer. It may be a scan.]\n\nThe user attached these files:\n- /workspace/home/Chats/a/scan.pdf\n\nthird'
  const got = splitAttachmentNote(text)
  assert.equal(got.text, 'first\n\nsecond\n\nthird')
  assert.deepEqual(got.attachments.map(a => a.name), ['one.txt', 'scan.pdf'])
})

test('chat attachments: text that only looks like the list stays', () => {
  for (const text of [
    'The user attached these files:',
    'The user attached these files:\n- not a path',
    'say The user attached these files:\n- /workspace/home/x',
    'plain words',
    '',
  ]) {
    assert.deepEqual(splitAttachmentNote(text), { text, attachments: [] })
  }
})

test('chat attachments: the files the server sends apart win over the text', () => {
  const atts = [{ path: '/Chats/a/b.png', name: 'b.png', size: 10, mime: 'image/png' }]
  assert.deepEqual(messageParts({ role: 'user', text: 'hi', attachments: atts }), { text: 'hi', attachments: atts })
  assert.deepEqual(messageParts({ role: 'user', text: `hi\n\nThe user attached these files:\n- ${PHOTO}` }).text, 'hi')
  assert.deepEqual(messageParts({ role: 'assistant', text: `The user attached these files:\n- ${PHOTO}` }).attachments, [])
  assert.deepEqual(messageParts({ role: 'user' }), { text: '', attachments: [] })
})

test('chat attachments: an image is told by its type or its name', () => {
  assert.equal(isImageAttachment({ name: 'a.bin', mime: 'image/heic' }), true)
  assert.equal(isImageAttachment({ name: 'photo.JPG' }), true)
  assert.equal(isImageAttachment({ name: 'notes.txt', mime: 'text/plain' }), false)
})

test('chat attachments: at most four tiles, the last one counts the rest', () => {
  const img = (n) => ({ path: `/a/${n}.png`, name: `${n}.png`, mime: 'image/png' })
  const four = attachmentLayout([1, 2, 3, 4].map(img))
  assert.equal(four.images.length, 4)
  assert.equal(four.more.length, 0)
  const six = attachmentLayout([1, 2, 3, 4, 5, 6].map(img))
  assert.deepEqual(six.images.map(a => a.name), ['1.png', '2.png', '3.png'])
  assert.equal(six.more.length, 3)
  const mixed = attachmentLayout([img(1), { path: '/a/x.txt', name: 'x.txt' }, { ...img(2), missing: true }])
  assert.deepEqual(mixed.images.map(a => a.name), ['1.png'])
  assert.deepEqual(mixed.files.map(a => a.name), ['x.txt', '2.png'])
})

test('chat attachments: a copied chat names the files and never their paths', async () => {
  const { chatMarkdown, lastUserText } = await import('../../src/utils/chatThread.js')
  const messages = [
    { seq: 1, role: 'user', text: `whats in this photo\n\nThe user attached these files:\n- ${PHOTO}` },
    { seq: 2, role: 'user', text: '', attachments: [{ path: '/Chats/a/b.txt', name: 'b.txt', size: 2048 }] },
  ]
  const md = chatMarkdown({ title: 'T', model: 'm' }, messages)
  assert.ok(md.includes('## User\n\nwhats in this photo\n\nAttached: photo_5985526360434217157_y.jpg\n\n'), md)
  assert.ok(md.includes('## User\n\nAttached: b.txt (2.0 KB)\n\n'), md)
  assert.ok(!md.includes('/workspace/home'), md)
  assert.ok(!md.includes('The user attached'), md)
  assert.equal(lastUserText(messages.slice(0, 1)), 'whats in this photo')
})
