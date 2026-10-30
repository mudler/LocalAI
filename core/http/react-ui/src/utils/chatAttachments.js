// The files of a chat message. The server keeps them apart from the words, in
// msg.attachments ({ path, name, size, mime, version, missing }), where path
// is inside the person's home and works with the files API. A message stored
// before that, or a server that does not split it, holds the list in its text:
// the words, the text the model read from the files, then
//
//   The user attached these files:
//   - /workspace/home/Chats/<folder>/<name>
//
// messageParts gives the words and the files either way, so the list and the
// read text are never shown. It mirrors session.SplitMessage on the server.

import { IMAGE_EXT } from './fileInfo.js'

const HEAD = 'The user attached these files:\n'
const GUEST_HOME = '/workspace/home'

// The starts of the notes the server adds for a file it read.
const readMarkers = (name) => [
  `[Voice note ${name}, transcribed: `,
  `[Text of ${name}:\n`,
  `[Document ${name} has no text layer.`,
]

// The first index of m in seg at its start or after a blank line, or -1.
function markerAt(seg, m) {
  for (let off = 0; off < seg.length;) {
    const j = seg.indexOf(m, off)
    if (j < 0) return -1
    if (j === 0 || seg.slice(0, j).endsWith('\n\n')) return j
    off = j + 1
  }
  return -1
}

// The lines "- <guest path>" from pos: the files and the end of the last line.
function listAt(text, pos) {
  const out = []
  let end = pos
  while (pos < text.length) {
    const nl = text.indexOf('\n', pos)
    const line = nl < 0 ? text.slice(pos) : text.slice(pos, nl)
    const next = nl < 0 ? text.length : nl + 1
    if (!line.startsWith('- ') || !line.slice(2).startsWith(`${GUEST_HOME}/`)) break
    const path = line.slice(2 + GUEST_HOME.length)
    out.push({ path, name: path.split('/').pop() })
    end = pos + line.length
    pos = next
  }
  return { files: out, end }
}

// The words of a message whose text holds lists of files, and the files.
// Text that only looks like a list stays.
export function splitAttachmentNote(text) {
  const s = String(text || '')
  const attachments = []
  let kept = ''
  let from = 0
  let search = 0
  for (;;) {
    const at = s.indexOf(HEAD, search)
    if (at < 0) break
    search = at + HEAD.length
    let start = at
    if (at > 0) {
      if (!s.slice(0, at).endsWith('\n\n') || at - 2 < from) continue
      start = at - 2
    }
    const { files, end } = listAt(s, at + HEAD.length)
    if (files.length === 0) continue
    const seg = s.slice(from, start)
    let cut = seg.length
    for (const f of files) {
      for (const m of readMarkers(f.name)) {
        const j = markerAt(seg, m)
        if (j >= 0 && j < cut) cut = j
      }
    }
    kept += cut < seg.length ? seg.slice(0, cut).replace(/\n+$/, '') : seg
    attachments.push(...files)
    from = end
    search = end
  }
  if (attachments.length === 0) return { text: s, attachments }
  kept += s.slice(from)
  return { text: kept.trim(), attachments }
}

// The words and the files of a message, as the person reads them.
export function messageParts(msg) {
  const text = msg?.text || ''
  if (msg?.role !== 'user') return { text, attachments: [] }
  if (Array.isArray(msg.attachments) && msg.attachments.length > 0) return { text, attachments: msg.attachments }
  return splitAttachmentNote(text)
}

export function isImageAttachment(a) {
  return (a?.mime || '').startsWith('image/') || IMAGE_EXT.test(a?.name || '')
}

// How a message shows its files: the images as thumbnails, at most max of
// them with "+N" for the rest, and every other file, or image that is gone,
// as a chip.
export function attachmentLayout(attachments, max = 4) {
  const list = attachments || []
  const images = list.filter(a => !a.missing && isImageAttachment(a))
  const files = list.filter(a => a.missing || !isImageAttachment(a))
  const shown = images.length > max ? images.slice(0, max - 1) : images
  return { images: shown, more: images.slice(shown.length), files }
}
