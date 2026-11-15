// How a chat answer shows the files it made: an image or a chart written as a
// file, named in the answer by the path the model knows (a path of its
// sandbox, a path of the person's files, a bare name). No DOM and no network
// here, so node can test it; the components pass the files in.
import { viewOf } from './previewPane.js'

// The folder the model sees as the person's files.
const GUEST_HOME = '/workspace/home'

// The file of the chat list a path names: by the path as the pane or the model
// knows it, or else by its name.
export function findChatFile(files, path) {
  if (!path) return null
  const name = path.split('/').pop()
  return files.find(f => f.path === path || f.guest_path === path)
    || files.find(f => f.name === name)
    || null
}

const EXTERNAL = /^(?:[a-z][a-z0-9+.-]*:|\/\/|#)/i

// What a src or href of an answer points at in the chat's files, or null when
// it is not a file of the chat: { path, name, known }. known is false for a
// path under the person's files that the list does not hold (yet): the file
// can still be read by its path.
export function chatFileTarget(ref, files = []) {
  let raw = String(ref || '').trim()
  if (!raw) return null
  // The model often writes sandbox:/path or file:///path for a file it made.
  raw = raw.replace(/^(?:sandbox|file):(?:\/\/)?(?=\/)/i, '')
  if (EXTERNAL.test(raw)) return null
  raw = raw.replace(/[?#].*$/, '')
  try { raw = decodeURI(raw) } catch { /* keep the text as written */ }
  raw = raw.replace(/^\.\//, '')
  const file = findChatFile(files, raw)
  if (file) return { path: file.path, name: file.name, known: true }
  if (raw.startsWith(`${GUEST_HOME}/`)) {
    const path = raw.slice(GUEST_HOME.length)
    return { path, name: path.split('/').pop(), known: false }
  }
  return null
}

export const isImageName = (name) => viewOf(name) === 'image'

const escapeRe = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

// Whether the answer's markdown already draws the image: ![alt](...name).
export function embedsImage(text, name) {
  if (!text || !name) return false
  return new RegExp(`!\\[[^\\]]*\\]\\([^)]*${escapeRe(name)}`, 'i').test(String(text))
}

// The images a turn made that its answer does not draw: they show under the
// answer, so a reply that only says "done" is not an empty block.
export function undrawnImages(files, text) {
  return (files || []).filter(f => isImageName(f.name) && !embedsImage(text, f.name))
}

// A chat folder is named after the day and the title ("2026-10-30 Quarterly
// budget"), so the path of a file has spaces, and markdown does not read an
// image or a link whose address has one: it stays as raw text. This writes the
// spaces as %20 in such an address, outside code. An address that is followed
// by a title ("url" 'title') or is already in angle brackets is left alone.
const LINK_WITH_SPACES = /(!?\[[^\]\n]*\])\(([^()<>\n]*? [^()<>\n]*?)\)/g
const CODE = /(`{3,}[\s\S]*?(?:`{3,}|$)|~{3,}[\s\S]*?(?:~{3,}|$)|`[^`\n]+`)/

export function spaceSafeLinks(text) {
  if (!text || !text.includes('](')) return text
  return String(text).split(CODE).map((part, i) => {
    if (i % 2 === 1) return part
    return part.replace(LINK_WITH_SPACES, (all, label, dest) => {
      if (/\s["'(].*["')]\s*$/.test(dest) && /^\S+\s+["']/.test(dest.trim())) return all
      const d = dest.trim()
      return `${label}(${d.replace(/ /g, '%20')})`
    })
  }).join('')
}
